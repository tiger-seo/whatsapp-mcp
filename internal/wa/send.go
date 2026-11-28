package wa

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

var (
	// ErrNotConnected refuses a send call: there is no connected generation that could take it.
	ErrNotConnected = errors.New("not connected to WhatsApp")
	// ErrSendFailed is a message that certainly did not go out: WhatsApp refused it, or the send failed before
	// anything was written to the connection.
	ErrSendFailed = errors.New("the message was not sent")
	// ErrOutcomeUnknown is a send that may or may not have been delivered. It is never retried.
	ErrOutcomeUnknown = errors.New("the message may or may not have been delivered")
	// ErrRecordMissing is a sent message that could not be written to the local history.
	ErrRecordMissing = errors.New("the sent message was not recorded locally")
	// ErrLookupUnanswered is a registration lookup whose answer does not cover the number.
	ErrLookupUnanswered = errors.New("the registration lookup was not answered")
)

// recordLimit bounds the local record of a sent message, within whatever is left of the caller's deadline.
const recordLimit = 5 * time.Second

// SendIdentity is the account's own addresses, without a device part. A zero address is unknown.
type SendIdentity struct{ Phone, LID types.JID }

// Registration is the answer to a registration lookup.
type Registration struct {
	Registered bool
	LID        types.JID
}

// Delivery is what WhatsApp acknowledged: the message ID and the server time.
type Delivery struct {
	ID   types.MessageID
	Time time.Time
}

// SentMessage is an acknowledged message to record in the local history.
type SentMessage struct {
	To      types.JID
	ID      types.MessageID
	Time    time.Time
	Message *waE2E.Message
}

// SendPort is the narrow send boundary of the connection owner. Each call is admitted only on the current,
// connected generation, runs under that generation's work context and network fence, and is waited for by its
// retirement. No call connects, reconnects, pairs or starts recovery, and no call reads a file the user owns.
type SendPort interface {
	// Ready returns ErrNotConnected unless a call could be admitted now.
	Ready() error
	// OwnIdentity returns the account's addresses, or zero values when no call could be admitted.
	OwnIdentity() SendIdentity
	// LookupPhone asks WhatsApp whether a phone number, given as digits, is registered.
	LookupPhone(ctx context.Context, phone string) (Registration, error)
	// UploadFile encrypts the private working copy into scratch and uploads it. ctx must carry a deadline.
	UploadFile(ctx context.Context, plaintext io.Reader, scratch io.ReadWriteSeeker, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
	// Deliver hands one message to WhatsApp and waits for its acknowledgement. handOver is called once, just before
	// the library's send starts. Until then a cancellation of ctx is returned as is and nothing is sent; afterwards
	// only the deadline of ctx and the generation's work context stop the send, and an error is ErrSendFailed or
	// ErrOutcomeUnknown.
	Deliver(ctx context.Context, to types.JID, message *waE2E.Message, handOver func()) (Delivery, error)
	// Record writes an acknowledged message to the local history. The cancellation of ctx does not stop it; its
	// deadline, five seconds and the generation's work context do.
	Record(ctx context.Context, sent SentMessage) error
}

// sendSession is what a client generation offers to the send port.
type sendSession interface {
	SendIdentity() SendIdentity
	LookupPhone(ctx context.Context, phone string) (Registration, error)
	UploadFile(ctx context.Context, plaintext io.Reader, scratch io.ReadWriteSeeker, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
	Deliver(ctx context.Context, to types.JID, message *waE2E.Message) (Delivery, error)
	RecordSent(ctx context.Context, sent SentMessage) error
}

var _ SendPort = (*connectionOwner)(nil)

// SendPortOf returns the owner's send port. An owner without one refuses every call as not connected.
func SendPortOf(owner Owner) SendPort {
	if port, ok := owner.(SendPort); ok {
		return port
	}
	return noSendPort{}
}

type noSendPort struct{}

func (noSendPort) Ready() error              { return ErrNotConnected }
func (noSendPort) OwnIdentity() SendIdentity { return SendIdentity{} }
func (noSendPort) LookupPhone(context.Context, string) (Registration, error) {
	return Registration{}, ErrNotConnected
}
func (noSendPort) UploadFile(context.Context, io.Reader, io.ReadWriteSeeker, whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	return whatsmeow.UploadResponse{}, ErrNotConnected
}
func (noSendPort) Deliver(context.Context, types.JID, *waE2E.Message, func()) (Delivery, error) {
	return Delivery{}, ErrNotConnected
}
func (noSendPort) Record(context.Context, SentMessage) error {
	return errors.Join(ErrRecordMissing, ErrNotConnected)
}

// sendCall is one admitted call.
type sendCall struct {
	session sendSession
	ctx     context.Context
	release func()
}

// sendTargetLocked returns the generation a call can be admitted on. Call it with o.mu held.
func (o *connectionOwner) sendTargetLocked() (*genState, sendSession, bool) {
	gen := o.gen
	if o.stopping || gen == nil || gen.retiring || !o.connected || o.health.StorageFault || o.phase != PhaseConnected {
		return nil, nil, false
	}
	session, ok := gen.handle.(sendSession)
	return gen, session, ok
}

// admitSend admits and counts one call. The call's context is a child of the generation's work context with the
// caller's deadline; with followCaller the caller's cancellation reaches it too, and a caller already done is
// refused with its own error.
func (o *connectionOwner) admitSend(caller context.Context, followCaller bool) (sendCall, error) {
	if followCaller {
		if err := caller.Err(); err != nil {
			return sendCall{}, err
		}
	}
	o.mu.Lock()
	gen, session, ok := o.sendTargetLocked()
	if !ok {
		o.mu.Unlock()
		return sendCall{}, ErrNotConnected
	}
	gen.sendsRunning++
	gen.sends.Add(1)
	o.mu.Unlock()

	ctx, cancel := context.WithCancel(gen.work)
	if deadline, ok := caller.Deadline(); ok {
		var stop context.CancelFunc
		ctx, stop = context.WithDeadline(ctx, deadline)
		inner := cancel
		cancel = func() { stop(); inner() }
	}
	unfollow := func() bool { return true }
	if followCaller {
		unfollow = context.AfterFunc(caller, cancel)
	}
	return sendCall{session: session, ctx: ctx, release: func() {
		unfollow()
		cancel()
		o.mu.Lock()
		gen.sendsRunning--
		o.mu.Unlock()
		gen.sends.Done()
	}}, nil
}

// Ready implements SendPort.
func (o *connectionOwner) Ready() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, _, ok := o.sendTargetLocked(); !ok {
		return ErrNotConnected
	}
	return nil
}

// OwnIdentity implements SendPort.
func (o *connectionOwner) OwnIdentity() SendIdentity {
	call, err := o.admitSend(context.Background(), false)
	if err != nil {
		return SendIdentity{}
	}
	defer call.release()
	return call.session.SendIdentity()
}

// LookupPhone implements SendPort.
func (o *connectionOwner) LookupPhone(ctx context.Context, phone string) (Registration, error) {
	call, err := o.admitSend(ctx, true)
	if err != nil {
		return Registration{}, err
	}
	defer call.release()
	return call.session.LookupPhone(call.ctx, phone)
}

// UploadFile implements SendPort.
func (o *connectionOwner) UploadFile(ctx context.Context, plaintext io.Reader, scratch io.ReadWriteSeeker, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	call, err := o.admitSend(ctx, true)
	if err != nil {
		return whatsmeow.UploadResponse{}, err
	}
	defer call.release()
	return call.session.UploadFile(call.ctx, plaintext, scratch, kind)
}

// Deliver implements SendPort.
func (o *connectionOwner) Deliver(ctx context.Context, to types.JID, message *waE2E.Message, handOver func()) (Delivery, error) {
	call, err := o.admitSend(ctx, false)
	if err != nil {
		return Delivery{}, err
	}
	defer call.release()
	// The last moment at which a cancelled request sends nothing.
	if err := ctx.Err(); err != nil {
		return Delivery{}, err
	}
	if err := call.ctx.Err(); err != nil {
		return Delivery{}, ErrNotConnected
	}
	if handOver != nil {
		handOver()
	}
	delivery, err := call.session.Deliver(call.ctx, to, message)
	if err != nil {
		return delivery, classifySendError(err)
	}
	return delivery, nil
}

// Record implements SendPort.
func (o *connectionOwner) Record(ctx context.Context, sent SentMessage) error {
	call, err := o.admitSend(ctx, false)
	if err != nil {
		logRecordMissing(o.log, err)
		return errors.Join(ErrRecordMissing, err)
	}
	defer call.release()
	bounded, cancel := context.WithTimeout(call.ctx, recordLimit)
	defer cancel()
	return call.session.RecordSent(bounded, sent)
}

// failedBeforeWrite are the library's refusals that mean nothing was written to the connection.
var failedBeforeWrite = []error{
	whatsmeow.ErrRecipientNotOnWhatsApp,
	whatsmeow.ErrNoPermissionInGroup,
	whatsmeow.ErrNotLoggedIn,
	whatsmeow.ErrRecipientADJID,
	whatsmeow.ErrUnknownServer,
	whatsmeow.ErrBroadcastListUnsupported,
}

// failedBeforeWriteText are the starts of the library's errors, which have no value of their own, for the steps of
// a send that come before the message node is written: the group, user and device queries, the sessions and the
// encryption. They are tied to the pinned library version.
var failedBeforeWriteText = []string{
	"failed to get group members: ",
	"failed to get user info for ",
	"failed to get LID for PN ",
	"failed to get device list: ",
	"failed to marshal message",
	"failed to fetch LID mappings: ",
	"failed to prefetch sessions: ",
	"failed to save cached sessions: ",
	"failed to create sender key distribution message ",
	"failed to marshal sender key distribution message ",
	"failed to encrypt group message ",
	"failed to send message node: failed to marshal node: ",
}

// classifySendError sorts an error of the library's send, once it has started, into the two outcomes a caller may
// see. Only a refusal in WhatsApp's acknowledgement and the listed errors from before the write are a failure.
// Everything else, including a cancellation, a deadline and a disconnect, leaves the outcome unknown.
func classifySendError(err error) error {
	failed := func() error { return fmt.Errorf("%w: %w", ErrSendFailed, err) }
	if errors.Is(err, whatsmeow.ErrServerReturnedError) {
		return failed()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrOutcomeUnknown, err)
	}
	for _, known := range failedBeforeWrite {
		if errors.Is(err, known) {
			return failed()
		}
	}
	text := err.Error()
	for _, prefix := range failedBeforeWriteText {
		if strings.HasPrefix(text, prefix) {
			return failed()
		}
	}
	return fmt.Errorf("%w: %w", ErrOutcomeUnknown, err)
}
