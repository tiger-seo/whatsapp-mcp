package media

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// The budget of one call, measured from its admission: WhatsApp must have acknowledged the message by acceptLimit,
// and the local record, which gets at most recordLimit, ends by replyLimit.
const (
	acceptLimit = 2 * time.Minute
	recordLimit = 5 * time.Second
	replyLimit  = acceptLimit + recordLimit
)

// The free space a send needs beyond the store's resume threshold: a file send writes the plaintext copy and the
// ciphertext, a conversion the input copy, the converter's output and the ciphertext of that output.
const (
	encryptionOverhead = 1 << 20
	conversionSpace    = 2 * maxFileSize
)

// History is what the service reads from the local store.
type History interface {
	Aliases
	// ChatExists reports whether a chat is stored, named or not.
	ChatExists(ctx context.Context, jid string) (bool, error)
	// FreshFreeBytes measures the free space of the data directory's volume now.
	FreshFreeBytes() (uint64, error)
}

var _ History = (*store.Store)(nil)

// Options builds a Service.
type Options struct {
	Port  wa.SendPort
	Store History
	// Limits is the process-wide send state, shared by every service of the process.
	Limits *Limits
	Clock  Clock
	// Recipients is the owner's allow-list.
	Recipients []string
	// Dirs are the allowed directories, opened at startup.
	Dirs    []AllowedDir
	DataDir string
	// FFmpeg is the configured converter; empty means the one on PATH.
	FFmpeg string
}

// Service runs the send pipeline. Its steps run in a fixed order and a refusal ends the call before any later one.
type Service struct {
	port    wa.SendPort
	store   History
	limits  *Limits
	clock   Clock
	policy  Policy
	dirs    []AllowedDir
	dataDir string
	files   fileAccess
	convert *converter
}

// NewService builds the send service.
func NewService(o Options) (*Service, error) {
	if o.Port == nil || o.Store == nil || o.Limits == nil || o.Clock == nil || !filepath.IsAbs(o.DataDir) {
		return nil, errors.New("the send service is missing a dependency")
	}
	policy, err := NewPolicy(o.Recipients)
	if err != nil {
		return nil, err
	}
	return &Service{port: o.Port, store: o.Store, limits: o.Limits, clock: o.Clock, policy: policy,
		dirs: slices.Clone(o.Dirs), dataDir: o.DataDir, files: systemFiles(), convert: newConverter(o.FFmpeg)}, nil
}

// call is the state of one send.
type call struct {
	// parent is the request's context; the client's cancellation reaches it.
	parent context.Context
	// ctx is parent bounded by the budget.
	ctx context.Context
}

// stopped says why a call may not go on before the hand-over, or returns nil.
func (c call) stopped() *Error {
	switch {
	case c.parent.Err() != nil:
		return fail(CodeCancelled, "the request was cancelled; nothing was sent")
	case c.ctx.Err() != nil:
		return fail(CodeTimeout, "the send did not finish in time; nothing was sent")
	}
	return nil
}

// or returns why the call was stopped, or else the given refusal.
func (c call) or(refusal *Error) *Error {
	if stopped := c.stopped(); stopped != nil {
		return stopped
	}
	return refusal
}

// budget bounds a context by what is left of the acceptance time on the service's clock.
func (s *Service) budget(parent context.Context, admitted time.Time) (context.Context, context.CancelFunc) {
	left := admitted.Add(acceptLimit).Sub(s.clock.Now())
	ctx, cancel := context.WithTimeout(parent, left)
	expired, stop := s.clock.NewTimer(left)
	go func() {
		select {
		case <-expired:
			cancel()
		case <-ctx.Done():
		}
		stop()
	}()
	return ctx, cancel
}

// validate checks the arguments by themselves.
func validate(req Request) *Error {
	text := func(field, value string, minimum int) *Error {
		if len(value) < minimum || len(value) > MaxTextBytes || !utf8.ValidString(value) {
			if minimum > 0 {
				return invalidInput(field, "must be 1 to 65536 bytes of valid UTF-8")
			}
			return invalidInput(field, "must be at most 65536 bytes of valid UTF-8")
		}
		return nil
	}
	path := func() *Error {
		if req.MediaPath == "" || len(req.MediaPath) > MaxPathBytes || !utf8.ValidString(req.MediaPath) {
			return invalidInput("media_path", "must be an absolute path of 1 to 4096 bytes")
		}
		return nil
	}
	switch req.Tool {
	case ToolMessage:
		return text("message", req.Message, 1)
	case ToolFile:
		if err := path(); err != nil {
			return err
		}
		if err := text("caption", req.Caption, 0); err != nil {
			return err
		}
		if req.Caption != "" && typeOfFile(req.MediaPath).voice {
			return invalidInput("caption", "is not accepted for a voice note, which shows none")
		}
		return nil
	case ToolAudio:
		if err := path(); err != nil {
			return err
		}
		if _, ok := demuxers[extension(req.MediaPath)]; !ok {
			return invalidInput("media_path", "must name an ogg, opus, mp3, wav, m4a, aac, flac or amr file")
		}
		return nil
	}
	return invalidInput("arguments", "unsupported tool")
}

// Send runs one send: arguments, recipient, policy, rate window and slot, connection, registration lookup, the
// file, the upload, the hand-over to WhatsApp and the local record. Nothing is uploaded, sent or stored by a call
// refused before the hand-over.
func (s *Service) Send(parent context.Context, req Request) (Result, *Error) {
	result := Result{Kind: KindUnknown}
	// The type is settled by the arguments alone, so a refused call can name it too.
	var kind fileType
	switch req.Tool {
	case ToolFile:
		kind = typeOfFile(req.MediaPath)
	case ToolAudio:
		kind = voiceType
	}
	result.MediaType, result.VoiceNote = kind.kind, kind.voice
	if err := validate(req); err != nil {
		return result, err
	}
	recipient, err := ParseRecipient(req.Recipient)
	if err != nil {
		return result, invalidInput("recipient", ErrRecipient.Error())
	}
	result.Kind = recipient.Kind()

	ctx, cancel := s.budget(parent, req.Admitted)
	defer cancel()
	c := call{parent: parent, ctx: ctx}

	target, err := s.policy.Resolve(ctx, s.store, recipient)
	switch {
	case errors.Is(err, ErrNotPermitted):
		return result, fail(CodeNotPermitted, "sending to this recipient is not permitted; the owner lists recipients in send_allowed_recipients, which is empty by default")
	case err != nil:
		return result, c.or(fail(CodeLookupFailed, ErrAliases.Error()+"; nothing was sent"))
	}

	slot, limit, busy, stuck := s.limits.Acquire()
	switch {
	case limit != nil:
		return result, rateLimited(*limit)
	case busy && stuck:
		return result, fail(CodeBusy, "a previous file read has not finished; retry later")
	case busy:
		return result, fail(CodeBusy, "another send is in progress; retry later")
	}
	// The slot is held for the whole call, unless an abandoned file stage takes it over.
	held := true
	defer func() {
		if held {
			slot.Release()
		}
	}()

	if err := s.port.Ready(); err != nil {
		return result, c.or(notConnected())
	}
	if own := s.port.OwnIdentity(); isSame(target.JID, own.Phone) || isSame(target.JID, own.LID) {
		result.Kind = KindSelf
	}
	if err := s.checkRegistered(c, recipient, target); err != nil {
		return result, err
	}

	var message *waE2E.Message
	if req.Tool == ToolMessage {
		message = &waE2E.Message{Conversation: proto.String(req.Message)}
	} else {
		work, stageErr, abandoned := s.runStage(c, slot, req)
		if abandoned {
			held = false
		}
		if stageErr != nil {
			return result, stageErr
		}
		defer work.remove()
		upload := workCopy
		if kind.voice {
			name, seconds, err := s.voiceNote(c, work.dir, req)
			if err != nil {
				return result, err
			}
			upload, result.Seconds = name, seconds
		}
		uploaded, uploadErr := s.upload(c, work.dir, upload, kind)
		if uploadErr != nil {
			return result, uploadErr
		}
		message = mediaMessage(kind, uploaded, filepath.Base(req.MediaPath), req.Caption, result.Seconds)
	}

	if stopped := c.stopped(); stopped != nil {
		return result, stopped
	}
	handed := false
	delivery, err := s.port.Deliver(ctx, target.JID, message, func() {
		handed = true
		s.limits.RecordSend()
	})
	if err != nil {
		switch {
		case !handed && errors.Is(err, wa.ErrNotConnected):
			return result, c.or(notConnected())
		case !handed:
			return result, c.or(fail(CodeSendFailed, "the message was not handed to WhatsApp"))
		case parent.Err() == nil && errors.Is(err, wa.ErrSendFailed):
			return result, fail(CodeSendFailed, "WhatsApp did not accept the message; it was not sent")
		}
		return result, fail(CodeOutcomeUnknown, "the message may have been delivered; it is not retried, so check the chat before sending it again")
	}
	result.ChatJID, result.ID, result.Time = target.String(), delivery.ID, delivery.Time
	result.Recorded = s.record(parent, req.Admitted, wa.SentMessage{To: target.JID, ID: delivery.ID, Time: delivery.Time, Message: message})
	if parent.Err() != nil {
		sent := result
		return result, &Error{Code: CodeSentCancelled, Message: "the request was cancelled after the message was sent; do not send it again", Sent: &sent}
	}
	return result, nil
}

// record writes the sent message to the local history, within five seconds and what is left of the reply budget.
// The request's cancellation does not reach it.
func (s *Service) record(parent context.Context, admitted time.Time, sent wa.SentMessage) bool {
	left := min(recordLimit, admitted.Add(replyLimit).Sub(s.clock.Now()))
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), left)
	defer cancel()
	return s.port.Record(ctx, sent) == nil
}

func isSame(a, b types.JID) bool { return !b.IsEmpty() && a.User == b.User && a.Server == b.Server }

func notConnected() *Error {
	return fail(CodeNotConnected, "not connected to WhatsApp; nothing was sent or queued")
}

func rateLimited(r RateLimit) *Error {
	what, perMinute, perDay := "sends", sendsPerMinute, sendsPerDay
	if r.Lookup {
		what, perMinute, perDay = "lookups", lookupsPerMinute, lookupsPerDay
	}
	count, period := perMinute, "minute"
	if r.Window == LimitDay {
		count, period = perDay, "24 hours"
	}
	name := "send limit"
	if r.Lookup {
		name = "registration lookup limit"
	}
	return &Error{
		Code:              CodeRateLimited,
		Message:           "the " + name + " of " + strconv.Itoa(count) + " per " + period + " is reached; nothing was sent",
		RetryAfterSeconds: int(max(1, math.Ceil(r.RetryAfter.Seconds()))),
		Limit:             what + "_per_" + string(r.Window),
	}
}

// checkRegistered asks WhatsApp about a phone recipient that has no stored direct chat, so that a number that is
// not on WhatsApp is refused before any file is read.
func (s *Service) checkRegistered(c call, given, target Recipient) *Error {
	if given.Kind() != KindPhone {
		return nil
	}
	readFailed := fail(CodeLookupFailed, "the local history could not be read; nothing was sent")
	chats := []string{target.String()}
	lids, err := s.store.LIDsForPhone(c.ctx, target.String())
	if err != nil {
		return c.or(readFailed)
	}
	for _, chat := range append(chats, lids...) {
		exists, err := s.store.ChatExists(c.ctx, chat)
		if err != nil {
			return c.or(readFailed)
		}
		if exists {
			return nil
		}
	}
	unregistered := fail(CodeNotOnWhatsApp, "the phone number is not on WhatsApp; nothing was sent")
	phone := target.JID.User
	if known, ok := s.limits.cachedLookup(phone); ok {
		if !known.registered {
			return unregistered
		}
		return nil
	}
	if stopped := c.stopped(); stopped != nil {
		return stopped
	}
	if limit := s.limits.startLookup(); limit != nil {
		return rateLimited(*limit)
	}
	registration, err := s.port.LookupPhone(c.ctx, phone)
	switch {
	case errors.Is(err, wa.ErrNotConnected):
		return c.or(notConnected())
	case err != nil:
		return c.or(fail(CodeLookupFailed, "WhatsApp did not say whether the number is registered; nothing was sent"))
	}
	lid := ""
	if !registration.LID.IsEmpty() {
		lid = registration.LID.String()
	}
	s.limits.storeLookup(phone, registration.Registered, lid)
	if !registration.Registered {
		return unregistered
	}
	return nil
}

// workingDir is the private directory holding the copy of the user's file.
type workingDir struct{ dir string }

type stageResult struct {
	work workingDir
	err  *Error
}

func (w workingDir) remove() {
	if w.dir != "" {
		_ = removeDir(w.dir)
	}
}

// runStage runs everything that touches the user's file in a goroutine that the call can abandon, because a drive
// that is slow or stuck can hold any of those steps in the kernel. When the call ends first, abandoned is true: the
// goroutine then owns the send slot, removes what it wrote and releases the slot once it really returns.
func (s *Service) runStage(c call, slot *Slot, req Request) (work workingDir, err *Error, abandoned bool) {
	if stopped := c.stopped(); stopped != nil {
		return workingDir{}, stopped, false
	}
	var mu sync.Mutex
	gone := false // guarded by mu; set when the call stopped waiting
	done := make(chan stageResult, 1)
	go func() {
		work, err := s.stage(c, req)
		mu.Lock()
		defer mu.Unlock()
		if !gone {
			done <- stageResult{work, err}
			return
		}
		work.remove()
		slot.Release()
	}()
	var out stageResult
	select {
	case out = <-done:
	case <-c.ctx.Done():
		mu.Lock()
		select {
		case out = <-done:
			mu.Unlock()
		default:
			gone = true
			slot.Abandon()
			mu.Unlock()
			return workingDir{}, c.stopped(), true
		}
	}
	if stopped := c.stopped(); stopped != nil {
		out.work.remove()
		return workingDir{}, stopped, false
	}
	return out.work, out.err, false
}

// stage opens and checks the file, checks the free space and copies the file into a new working directory. On a
// refusal it leaves nothing behind. It makes no send port call.
func (s *Service) stage(c call, req Request) (workingDir, *Error) {
	none := workingDir{}
	source, err := openSource(s.files, s.dirs, req.MediaPath)
	if err != nil {
		return none, fail(CodeFileRefused, err.Error())
	}
	defer source.file.Close()
	// Whether an audio file needs converting is known only once it is read, so that call counts the conversion.
	needed := store.DiskResumeBytes + 2*uint64(source.size) + encryptionOverhead
	if req.Tool == ToolAudio {
		needed = store.DiskResumeBytes + uint64(source.size) + conversionSpace
	}
	if free, err := s.store.FreshFreeBytes(); err != nil || free < needed {
		return none, fail(CodeInsufficientSpace, "the data directory's volume does not have enough free space for this send, or it could not be measured; nothing was sent")
	}
	if stopped := c.stopped(); stopped != nil {
		return none, stopped
	}
	workFailed := fail(CodeUploadFailed, "the private working copy could not be prepared; nothing was sent")
	dir, err := newWorkDir(s.dataDir)
	if err != nil {
		return none, workFailed
	}
	complete := false
	defer func() {
		if !complete {
			_ = removeDir(dir)
		}
	}()
	copied, err := createWorkFile(dir, workCopy)
	if err != nil {
		return none, workFailed
	}
	var in io.Reader = source.file
	if s.files.reader != nil {
		in = s.files.reader(source.file)
	}
	err = copyChecked(c.ctx, copied, in, source.size)
	if closeErr := copied.Close(); err == nil && closeErr != nil {
		err = errWorkFile
	}
	switch {
	case errors.Is(err, errWorkFile):
		return none, workFailed
	case err != nil:
		return none, c.or(fail(CodeFileRefused, err.Error()))
	}
	complete = true
	return workingDir{dir: dir}, nil
}

// openWorkFile opens a private working file for reading.
func openWorkFile(dir, name string) (*os.File, error) {
	return private.OpenFile(filepath.Join(dir, name), os.O_RDONLY)
}

func (s *Service) analyse(ctx context.Context, dir, name string) (int, error) {
	f, err := openWorkFile(dir, name)
	if err != nil {
		return 0, errWorkFile
	}
	defer f.Close()
	return opusDuration(ctx, f, maxFileSize)
}

// voiceNote returns the working file to send as a voice note and its duration. send_audio_message converts audio
// that is not Ogg Opus; send_file refuses it.
func (s *Service) voiceNote(c call, dir string, req Request) (string, int, *Error) {
	invalid := fail(CodeInvalidAudio, "the file is not valid Ogg Opus audio; nothing was sent")
	seconds, err := s.analyse(c.ctx, dir, workCopy)
	switch {
	case err == nil:
		return workCopy, seconds, nil
	case errors.Is(err, errWorkFile):
		return "", 0, fail(CodeUploadFailed, "the private working copy could not be read; nothing was sent")
	case req.Tool != ToolAudio || !errors.Is(err, errNotOpus):
		return "", 0, c.or(invalid)
	}
	failed := fail(CodeConversionFailed, "ffmpeg could not convert the file within its time and size limits; nothing was sent")
	switch err := s.convert.convert(c.ctx, dir, demuxers[extension(req.MediaPath)]); {
	case errors.Is(err, errConverterMissing):
		return "", 0, fail(CodeFFmpegMissing, "ffmpeg was not found; install it on PATH or set the ffmpeg setting to its absolute path")
	case err != nil:
		return "", 0, c.or(failed)
	}
	seconds, err = s.analyse(c.ctx, dir, workConverted)
	if err != nil {
		return "", 0, c.or(failed)
	}
	return workConverted, seconds, nil
}

// upload encrypts the named working file into a second one and uploads it.
func (s *Service) upload(c call, dir, name string, kind fileType) (uploaded whatsmeow.UploadResponse, refusal *Error) {
	if stopped := c.stopped(); stopped != nil {
		return uploaded, stopped
	}
	failed := fail(CodeUploadFailed, "the upload failed; nothing was sent")
	plain, err := openWorkFile(dir, name)
	if err != nil {
		return uploaded, failed
	}
	defer plain.Close()
	scratch, err := createWorkFile(dir, workEncrypted)
	if err != nil {
		return uploaded, failed
	}
	defer scratch.Close()
	uploaded, err = s.port.UploadFile(c.ctx, plain, scratch, kind.upload)
	switch {
	case errors.Is(err, wa.ErrNotConnected):
		return uploaded, c.or(notConnected())
	case err != nil:
		return uploaded, c.or(failed)
	}
	return uploaded, nil
}
