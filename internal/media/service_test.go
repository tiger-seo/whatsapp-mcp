package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

const (
	ownPhone  = "490000000001"
	listed    = "491511000001"
	listedJID = listed + "@s.whatsapp.net"
)

// fakeHistory is the store the service reads: a LID map, stored chats and a free-space answer.
type fakeHistory struct {
	mu      sync.Mutex
	aliases mapAliases
	chats   map[string]bool
	chatErr error
	free    uint64
	freeErr error
	reads   []string
}

func (h *fakeHistory) PhoneForLID(ctx context.Context, lid string) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.aliases.PhoneForLID(ctx, lid)
}

func (h *fakeHistory) LIDsForPhone(ctx context.Context, phone string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.aliases.LIDsForPhone(ctx, phone)
}

func (h *fakeHistory) ChatExists(_ context.Context, jid string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reads = append(h.reads, jid)
	return h.chats[jid], h.chatErr
}

func (h *fakeHistory) FreshFreeBytes() (uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.free, h.freeErr
}

type sendRig struct {
	t            *testing.T
	svc          *Service
	port         *wa.FakeSendPort
	history      *fakeHistory
	clock        *fakeClock
	limits       *Limits
	data, outbox string
}

func ownIdentity() wa.SendIdentity {
	return wa.SendIdentity{Phone: types.NewJID(ownPhone, types.DefaultUserServer), LID: types.NewJID("100000000009", types.HiddenUserServer)}
}

// privateDataDir returns a new private data directory with an outbox, opened as the allowed directory.
func privateDataDir(t *testing.T) (data, outbox string, dirs []AllowedDir) {
	t.Helper()
	data = filepath.Join(t.TempDir(), "data")
	if err := private.MkdirAll(data); err != nil {
		t.Fatal(err)
	}
	outbox = filepath.Join(data, "outbox")
	if err := private.MkdirAll(outbox); err != nil {
		t.Fatal(err)
	}
	return data, outbox, []AllowedDir{openAllowed(t, outbox)}
}

func openAllowed(t *testing.T, dir string) AllowedDir {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(resolved)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return AllowedDir{Path: resolved, Root: root}
}

func newSendRig(t *testing.T, adjust ...func(*Options)) *sendRig {
	t.Helper()
	data, outbox, dirs := privateDataDir(t)
	r := &sendRig{t: t, port: wa.NewFakeSendPort(ownIdentity()), clock: newFakeClock(), data: data, outbox: outbox,
		history: &fakeHistory{chats: map[string]bool{listedJID: true}, free: 100 << 30}}
	r.limits = NewLimits(r.clock)
	options := Options{Port: r.port, Store: r.history, Limits: r.limits, Clock: r.clock, Recipients: []string{"*"}, Dirs: dirs, DataDir: data}
	for _, a := range adjust {
		a(&options)
	}
	var err error
	if r.svc, err = NewService(options); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *sendRig) text(recipient, message string) Request {
	return Request{Tool: ToolMessage, Recipient: recipient, Message: message, Admitted: r.clock.Now()}
}

func (r *sendRig) file(tool Tool, name string, content []byte) Request {
	r.t.Helper()
	path := filepath.Join(r.outbox, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		r.t.Fatal(err)
	}
	return Request{Tool: tool, Recipient: listed, MediaPath: path, Admitted: r.clock.Now()}
}

func (r *sendRig) send(req Request) (Result, *Error) { return r.svc.Send(context.Background(), req) }

func wantCode(t *testing.T, err *Error, code Code) {
	t.Helper()
	if err == nil || err.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

// workDirs lists the working directories that exist.
func (r *sendRig) workDirs() []string {
	r.t.Helper()
	entries, err := os.ReadDir(filepath.Join(r.data, workDirName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// wantNothingSent checks that no upload, send or record happened and that no working file is left.
func (r *sendRig) wantNothingSent() {
	r.t.Helper()
	calls := r.port.Calls()
	if len(calls.Uploads) != 0 || len(calls.Deliveries) != 0 || len(calls.Records) != 0 {
		r.t.Fatalf("calls = %d uploads, %d sends, %d records", len(calls.Uploads), len(calls.Deliveries), len(calls.Records))
	}
	if dirs := r.workDirs(); len(dirs) != 0 {
		r.t.Fatalf("working directories left: %v", dirs)
	}
}

// slotFree reports whether another send could take the slot.
func (r *sendRig) slotFree() bool {
	slot, _, _, _ := r.limits.Acquire()
	if slot == nil {
		return false
	}
	slot.Release()
	return true
}

func TestSendTextMessage(t *testing.T) {
	r := newSendRig(t)
	res, err := r.send(r.text("+49 151 100-00-01", "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != KindPhone || res.ChatJID != listedJID || res.ID == "" || res.Time.IsZero() || res.MediaType != "" || res.VoiceNote || !res.Recorded {
		t.Errorf("result = %+v", res)
	}
	calls := r.port.Calls()
	if len(calls.Deliveries) != 1 || len(calls.Uploads) != 0 || len(calls.Lookups) != 0 || len(calls.Records) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	sent := calls.Deliveries[0]
	if sent.To.String() != listedJID || sent.Message.GetConversation() != "hello" || sent.Message.ExtendedTextMessage != nil {
		t.Errorf("sent %s %v", sent.To, sent.Message)
	}
	record := calls.Records[0]
	if record.To.String() != listedJID || record.ID != res.ID || !record.Time.Equal(res.Time) || record.Message.GetConversation() != "hello" {
		t.Errorf("record = %+v", record)
	}
	if n := len(r.limits.sends.stamps); n != 1 {
		t.Errorf("sends counted = %d", n)
	}
	if len(r.workDirs()) != 0 || !r.slotFree() {
		t.Error("a text send left a working directory or the slot")
	}
}

func TestSendTextRegistrationLookup(t *testing.T) {
	const stranger, unregistered = "491511000002", "491511000003"
	r := newSendRig(t)
	r.port.SetRegistration(stranger, wa.Registration{Registered: true})
	// A stored direct chat, a LID and a group skip the lookup, and so does a chat stored under the phone's LID.
	r.history.aliases.lids = map[string]string{"100000000004@lid": "491511000004@s.whatsapp.net"}
	r.history.chats["100000000004@lid"] = true
	for _, recipient := range []string{listed, "100000000005@lid", "120363000000000001@g.us", "491511000004"} {
		if _, err := r.send(r.text(recipient, "x")); err != nil {
			t.Fatalf("%s: %v", recipient, err)
		}
	}
	if got := r.port.Calls().Lookups; len(got) != 0 {
		t.Fatalf("lookups = %v", got)
	}
	counted := len(r.limits.sends.stamps)
	// A phone without a stored chat is looked up once; the result is reused, and no lookup counts as a send.
	for range 2 {
		if _, err := r.send(r.text(stranger, "x")); err != nil {
			t.Fatal(err)
		}
	}
	_, first := r.send(r.text(unregistered, "x"))
	wantCode(t, first, CodeNotOnWhatsApp)
	_, again := r.send(r.text(unregistered, "x"))
	wantCode(t, again, CodeNotOnWhatsApp)
	if got := r.port.Calls().Lookups; !slices.Equal(got, []string{stranger, unregistered}) {
		t.Errorf("lookups = %v", got)
	}
	if got := len(r.limits.sends.stamps); got != counted+2 {
		t.Errorf("sends counted = %d, want %d", got, counted+2)
	}
}

func TestSendLookupFailureAndLimit(t *testing.T) {
	r := newSendRig(t)
	r.port.SetFailure(wa.FakeLookup, errors.New("broken"))
	for range 2 {
		_, err := r.send(r.text("491511000002", "x"))
		wantCode(t, err, CodeLookupFailed)
	}
	if got := r.port.Calls().Lookups; len(got) != 2 {
		t.Errorf("a failed lookup was cached: %v", got)
	}
	r.port.SetFailure(wa.FakeLookup, nil)
	for i := range lookupsPerMinute - 2 {
		_, err := r.send(r.text("49151200000"+strconv.Itoa(i), "x"))
		wantCode(t, err, CodeNotOnWhatsApp)
	}
	_, err := r.send(r.text("491513000000", "x"))
	wantCode(t, err, CodeRateLimited)
	if err.Limit != "lookups_per_minute" || err.RetryAfterSeconds != 60 || !strings.Contains(err.Message, "lookup") {
		t.Errorf("error = %+v", err)
	}
	// A cached number needs no lookup, so it is still answered.
	_, err = r.send(r.text("491512000000", "x"))
	wantCode(t, err, CodeNotOnWhatsApp)
	r.wantNothingSent()

	r.history.chatErr = errors.New("broken")
	_, err = r.send(r.text("491514000000", "x"))
	wantCode(t, err, CodeLookupFailed)
}

func TestSendRefusalsStopEveryLaterStep(t *testing.T) {
	stranger := "491511000002"
	for _, tt := range []struct {
		name  string
		setup func(r *sendRig)
		code  Code
		reads bool
	}{
		{"bad recipient", func(r *sendRig) {}, CodeInvalidInput, false},
		{"not permitted", func(r *sendRig) {}, CodeNotPermitted, false},
		{"rate limited", func(r *sendRig) {
			for range sendsPerMinute {
				r.limits.RecordSend()
			}
		}, CodeRateLimited, false},
		{"busy", func(r *sendRig) { slot, _, _, _ := r.limits.Acquire(); r.t.Cleanup(slot.Release) }, CodeBusy, false},
		{"not connected", func(r *sendRig) { r.port.SetFailure(wa.FakeReady, wa.ErrNotConnected) }, CodeNotConnected, false},
		{"unregistered", func(r *sendRig) {}, CodeNotOnWhatsApp, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newSendRig(t, func(o *Options) { o.Recipients = []string{listed, stranger} })
			tt.setup(r)
			// The file does not exist: reaching the file stage would refuse it.
			req := Request{Tool: ToolFile, Recipient: stranger, MediaPath: filepath.Join(r.outbox, "missing.jpg"), Admitted: r.clock.Now()}
			if tt.name == "bad recipient" {
				req.Recipient = "status@broadcast"
			}
			if tt.name == "not permitted" {
				req.Recipient = "491511000009"
			}
			counted := len(r.limits.sends.stamps)
			res, err := r.send(req)
			wantCode(t, err, tt.code)
			if res.MediaType != typeImage {
				t.Errorf("media type = %q", res.MediaType)
			}
			if (len(r.history.reads) > 0) != tt.reads {
				t.Errorf("chat reads = %v", r.history.reads)
			}
			if got := len(r.port.Calls().Lookups); (got > 0) != tt.reads {
				t.Errorf("lookups = %d", got)
			}
			if len(r.limits.sends.stamps) != counted {
				t.Error("a refused call was counted")
			}
			r.wantNothingSent()
		})
	}
}

func TestSendNoteToSelfKind(t *testing.T) {
	r := newSendRig(t)
	r.history.chats[ownPhone+"@s.whatsapp.net"] = true
	for _, recipient := range []string{ownPhone, "100000000009@lid"} {
		res, err := r.send(r.text(recipient, "note"))
		if err != nil || res.Kind != KindSelf {
			t.Errorf("%s: %+v, %v", recipient, res, err)
		}
	}
}

func TestSendOutcomes(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		code Code
	}{
		{"refused", wa.ErrSendFailed, CodeSendFailed},
		{"unknown", wa.ErrOutcomeUnknown, CodeOutcomeUnknown},
		{"unrecognised", errors.New("broken"), CodeOutcomeUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newSendRig(t)
			r.port.SetFailure(wa.FakeDeliver, tt.err)
			res, err := r.send(r.text(listed, "x"))
			wantCode(t, err, tt.code)
			if res.ID != "" || res.ChatJID != "" {
				t.Errorf("result = %+v", res)
			}
			calls := r.port.Calls()
			if len(calls.Deliveries) != 1 || len(calls.Records) != 0 || len(r.limits.sends.stamps) != 1 {
				t.Errorf("calls = %+v, counted %d", calls, len(r.limits.sends.stamps))
			}
		})
	}
	t.Run("disconnected at the hand-over", func(t *testing.T) {
		r := newSendRig(t)
		r.svc.port = portWithDeliver{r.port, func() error { return wa.ErrNotConnected }}
		_, err := r.send(r.text(listed, "x"))
		wantCode(t, err, CodeNotConnected)
		if len(r.limits.sends.stamps) != 0 {
			t.Error("a send that was not handed over was counted")
		}
	})
	t.Run("record missing", func(t *testing.T) {
		r := newSendRig(t)
		r.port.SetFailure(wa.FakeRecord, wa.ErrRecordMissing)
		res, err := r.send(r.text(listed, "x"))
		if err != nil || res.Recorded || res.ID == "" {
			t.Errorf("result = %+v, %v", res, err)
		}
	})
}

// portWithDeliver refuses at the hand-over, before anything is handed to WhatsApp.
type portWithDeliver struct {
	wa.SendPort
	refuse func() error
}

func (p portWithDeliver) Deliver(context.Context, types.JID, *waE2E.Message, func()) (wa.Delivery, error) {
	return wa.Delivery{}, p.refuse()
}

func TestSendClientCancellation(t *testing.T) {
	t.Run("before the hand-over", func(t *testing.T) {
		for _, op := range []string{wa.FakeLookup, wa.FakeUpload} {
			r := newSendRig(t)
			ctx, cancel := context.WithCancel(context.Background())
			r.port.SetRegistration(listed, wa.Registration{Registered: true})
			r.history.chats = map[string]bool{}
			r.port.SetHook(op, func(context.Context) error { cancel(); return context.Canceled })
			req := r.file(ToolFile, "a.jpg", []byte("image"))
			_, err := r.svc.Send(ctx, req)
			wantCode(t, err, CodeCancelled)
			calls := r.port.Calls()
			if len(calls.Deliveries) != 0 || len(calls.Records) != 0 || len(r.limits.sends.stamps) != 0 || len(r.workDirs()) != 0 || !r.slotFree() {
				t.Errorf("%s: calls = %+v", op, calls)
			}
		}
	})
	t.Run("already cancelled", func(t *testing.T) {
		r := newSendRig(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := r.svc.Send(ctx, r.text(listed, "x"))
		wantCode(t, err, CodeCancelled)
		r.wantNothingSent()
	})
	t.Run("after the acknowledgement", func(t *testing.T) {
		r := newSendRig(t)
		ctx, cancel := context.WithCancel(context.Background())
		var recordErr error
		r.port.SetHook(wa.FakeDeliver, func(sendCtx context.Context) error {
			cancel()
			return sendCtx.Err()
		})
		r.port.SetHook(wa.FakeRecord, func(recordCtx context.Context) error {
			recordErr = recordCtx.Err()
			return nil
		})
		res, err := r.svc.Send(ctx, r.text(listed, "x"))
		wantCode(t, err, CodeSentCancelled)
		if err.Sent == nil || err.Sent.ChatJID != listedJID || err.Sent.ID == "" || err.Sent.Time.IsZero() || !err.Sent.Recorded {
			t.Fatalf("sent = %+v", err.Sent)
		}
		if res.ID != err.Sent.ID || recordErr != nil || len(r.port.Calls().Records) != 1 || len(r.limits.sends.stamps) != 1 {
			t.Errorf("result %+v, record context %v", res, recordErr)
		}
	})
	t.Run("after the hand-over without an acknowledgement", func(t *testing.T) {
		for _, outcome := range []error{wa.ErrOutcomeUnknown, wa.ErrSendFailed} {
			r := newSendRig(t)
			ctx, cancel := context.WithCancel(context.Background())
			r.port.SetHook(wa.FakeDeliver, func(context.Context) error { cancel(); return outcome })
			_, err := r.svc.Send(ctx, r.text(listed, "x"))
			wantCode(t, err, CodeOutcomeUnknown)
			if len(r.port.Calls().Records) != 0 || len(r.limits.sends.stamps) != 1 {
				t.Error("an unacknowledged send was recorded or not counted")
			}
		}
	})
}

func TestSendBudget(t *testing.T) {
	r := newSendRig(t)
	admitted := r.clock.Now()
	var deliverLeft, recordLeft time.Duration
	r.port.SetHook(wa.FakeDeliver, func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("no deadline")
		}
		deliverLeft = time.Until(deadline)
		r.clock.Advance(acceptLimit - 30*time.Second - 100*time.Millisecond)
		return nil
	})
	r.port.SetHook(wa.FakeRecord, func(ctx context.Context) error {
		deadline, _ := ctx.Deadline()
		recordLeft = time.Until(deadline)
		return nil
	})
	// Thirty seconds of the budget passed before the call reached the service.
	r.clock.Advance(30 * time.Second)
	req := r.text(listed, "x")
	req.Admitted = admitted
	if _, err := r.send(req); err != nil {
		t.Fatal(err)
	}
	near := func(got, want time.Duration) bool { return got <= want && got > want-2*time.Second }
	if !near(deliverLeft, 90*time.Second) {
		t.Errorf("the send had %v left, want what remains of two minutes", deliverLeft)
	}
	if !near(recordLeft, recordLimit) {
		t.Errorf("the record had %v, want five seconds", recordLeft)
	}
	// An acknowledgement late enough leaves the record only what remains of the reply budget.
	r.port.SetHook(wa.FakeDeliver, func(context.Context) error { r.clock.Advance(replyLimit - 2*time.Second); return nil })
	if _, err := r.send(r.text(listed, "x")); err != nil {
		t.Fatal(err)
	}
	if !near(recordLeft, 2*time.Second) {
		t.Errorf("the record had %v, want the remaining two seconds", recordLeft)
	}
	// Nothing starts once the two minutes are over.
	r.port.SetHook(wa.FakeDeliver, nil)
	late := r.text(listed, "x")
	r.clock.Advance(acceptLimit)
	sends := len(r.port.Calls().Deliveries)
	_, err := r.send(late)
	wantCode(t, err, CodeTimeout)
	if len(r.port.Calls().Deliveries) != sends {
		t.Error("a send past its budget was handed over")
	}
}

// The budget is enforced on the service's clock: when it runs out during an upload, the call is stopped.
func TestSendBudgetStopsAnUpload(t *testing.T) {
	r := newSendRig(t)
	r.port.SetHook(wa.FakeUpload, func(ctx context.Context) error {
		r.clock.Advance(acceptLimit)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
			return errors.New("the upload was not stopped")
		}
	})
	_, err := r.send(r.file(ToolFile, "a.jpg", []byte("image")))
	wantCode(t, err, CodeTimeout)
	r.port.SetHook(wa.FakeUpload, nil)
	if len(r.port.Calls().Deliveries) != 0 || len(r.workDirs()) != 0 {
		t.Error("a stopped upload was sent or left files")
	}
}

func TestSendDiskSpace(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 3<<20)
	size := uint64(len(content))
	for _, tt := range []struct {
		name   string
		tool   Tool
		file   string
		needed uint64
	}{
		{"file", ToolFile, "a.jpg", store.DiskResumeBytes + 2*size + 1<<20},
		{"voice note through send_file", ToolFile, "a.ogg", store.DiskResumeBytes + 2*size + 1<<20},
		// A small .ogg that may or may not need converting is counted as a conversion.
		{"audio", ToolAudio, "a.ogg", store.DiskResumeBytes + size + 200<<20},
		{"audio to convert", ToolAudio, "a.mp3", store.DiskResumeBytes + size + 200<<20},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newSendRig(t)
			req := r.file(tt.tool, tt.file, content)
			r.history.free = tt.needed - 1
			_, err := r.send(req)
			wantCode(t, err, CodeInsufficientSpace)
			r.wantNothingSent()
			if _, err := os.Stat(filepath.Join(r.data, workDirName)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the working directory was created: %v", err)
			}
			if !r.slotFree() {
				t.Error("the slot is held")
			}
			r.history.free = tt.needed
			if _, err := r.send(req); err != nil && err.Code == CodeInsufficientSpace {
				t.Error("exactly the needed space was refused")
			}
		})
	}
	t.Run("not measurable", func(t *testing.T) {
		r := newSendRig(t)
		r.history.freeErr = errors.New("no measurement")
		_, err := r.send(r.file(ToolFile, "a.jpg", content))
		wantCode(t, err, CodeInsufficientSpace)
		r.wantNothingSent()
	})
	t.Run("a text send needs none", func(t *testing.T) {
		r := newSendRig(t)
		r.history.free, r.history.freeErr = 0, errors.New("no measurement")
		if _, err := r.send(r.text(listed, "x")); err != nil {
			t.Error(err)
		}
	})
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestSendFileThatChangesWhileRead(t *testing.T) {
	for name, change := range map[string]func(path string) error{
		"grown": func(path string) error {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.WriteString("more")
			return err
		},
		"grown past the limit": func(path string) error { return os.Truncate(path, maxFileSize+10) },
		"shrunk":               func(path string) error { return os.Truncate(path, 2) },
		"emptied":              func(path string) error { return os.Truncate(path, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			r := newSendRig(t)
			req := r.file(ToolFile, "a.jpg", []byte("original content"))
			var copied int64
			r.svc.files.reader = func(f *os.File) io.Reader {
				if err := change(req.MediaPath); err != nil {
					t.Fatal(err)
				}
				return readerFunc(func(p []byte) (int, error) {
					n, err := f.Read(p)
					copied += int64(n)
					return n, err
				})
			}
			_, err := r.send(req)
			wantCode(t, err, CodeFileRefused)
			if copied > int64(len("original content"))+1 {
				t.Errorf("%d bytes were read, more than the opened size plus one", copied)
			}
			r.wantNothingSent()
		})
	}
}

// heldStage blocks one step of the source stage until released, as a stuck drive would.
type heldStage struct{ reached, release chan struct{} }

func holdStage(r *sendRig, step string) heldStage {
	h := heldStage{reached: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	wait := func() {
		once.Do(func() { close(h.reached) })
		<-h.release
	}
	real := systemFiles()
	switch step {
	case "resolve":
		r.svc.files.resolve = func(path string) (string, error) { wait(); return real.resolve(path) }
	case "open":
		r.svc.files.open = func(root *os.Root, name string) (*os.File, error) { wait(); return real.open(root, name) }
	case "read":
		r.svc.files.reader = func(f *os.File) io.Reader {
			return readerFunc(func(p []byte) (int, error) { wait(); return f.Read(p) })
		}
	}
	return h
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !done(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestSendAbandonedStage(t *testing.T) {
	for _, step := range []string{"resolve", "open", "read"} {
		t.Run(step, func(t *testing.T) {
			r := newSendRig(t)
			held := holdStage(r, step)
			req := r.file(ToolFile, "a.jpg", []byte("image"))
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan *Error, 1)
			go func() {
				_, err := r.svc.Send(ctx, req)
				done <- err
			}()
			<-held.reached
			// While the stage is stuck the call holds the slot.
			_, busy := r.send(r.text(listed, "x"))
			wantCode(t, busy, CodeBusy)
			start := time.Now()
			cancel()
			select {
			case err := <-done:
				wantCode(t, err, CodeCancelled)
			case <-time.After(time.Second):
				t.Fatal("the call did not return within a second of its cancellation")
			}
			if time.Since(start) > time.Second {
				t.Error("the return took too long")
			}
			// The abandoned work keeps the slot, so stuck reads cannot pile up.
			_, stuck := r.send(r.text(listed, "x"))
			wantCode(t, stuck, CodeBusy)
			if !strings.Contains(stuck.Message, "previous file read") {
				t.Errorf("message = %q", stuck.Message)
			}
			calls := r.port.Calls()
			if len(calls.Uploads)+len(calls.Deliveries)+len(calls.Lookups) != 0 {
				t.Errorf("port calls = %+v", calls)
			}
			close(held.release)
			// The late stage removes what it wrote and only then frees the slot.
			waitFor(t, "the slot", r.slotFree)
			if dirs := r.workDirs(); len(dirs) != 0 {
				t.Errorf("working directories left: %v", dirs)
			}
			r.wantNothingSent()
		})
	}
}

func TestSendBudgetAbandonsAStuckStage(t *testing.T) {
	r := newSendRig(t)
	held := holdStage(r, "resolve")
	req := r.file(ToolFile, "a.jpg", []byte("image"))
	done := make(chan *Error, 1)
	go func() {
		_, err := r.send(req)
		done <- err
	}()
	<-held.reached
	r.clock.Advance(acceptLimit)
	select {
	case err := <-done:
		wantCode(t, err, CodeTimeout)
	case <-time.After(time.Second):
		t.Fatal("the call outlived its budget")
	}
	close(held.release)
	waitFor(t, "the slot", r.slotFree)
	r.wantNothingSent()
}

func TestSendSweepWorkDirs(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	if err := private.MkdirAll(data); err != nil {
		t.Fatal(err)
	}
	if err := SweepWorkDirs(data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, workDirName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the sweep created the parent: %v", err)
	}
	dir, err := newWorkDir(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := private.CheckDir(dir); err != nil {
		t.Errorf("working directory is not private: %v", err)
	}
	f, err := createWorkFile(dir, workCopy)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(filepath.Dir(outside), filepath.Join(data, workDirName, "link"))
	if err := SweepWorkDirs(data); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(filepath.Join(data, workDirName)); err != nil || len(entries) != 0 {
		t.Errorf("left %v, %v", entries, err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the sweep followed a link: %v", err)
	}
}
