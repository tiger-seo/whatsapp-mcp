package wa

import (
	"context"
	"io"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

var _ sendSession = (*Session)(nil)

// The session's send methods are reached only through the owner, which admits each call before retirement starts
// and waits for it before this generation's storage is blocked. They therefore do not count a call that is still
// running at the retire bit as a late call: its context is cancelled right after.

// SendIdentity returns the account's own addresses without a device part.
func (s *Session) SendIdentity() SendIdentity {
	return SendIdentity{Phone: s.client.Store.GetJID().ToNonAD(), LID: s.client.Store.GetLID().ToNonAD()}
}

// LookupPhone asks whether the number is registered. A registered number's LID is stored by the library, so the
// send that follows does not look it up again.
func (s *Session) LookupPhone(ctx context.Context, phone string) (Registration, error) {
	query := "+" + phone
	answers, err := s.client.IsOnWhatsApp(ctx, []string{query})
	if err != nil {
		return Registration{}, err
	}
	for _, answer := range answers {
		// One number was asked for, so a single answer is about it even when the server rewrote the number.
		if len(answers) != 1 && answer.Query != query && answer.PhoneNumber.User != phone {
			continue
		}
		out := Registration{Registered: answer.IsIn}
		if answer.IsIn && answer.JID.Server == types.HiddenUserServer {
			out.LID = answer.JID
		}
		return out, nil
	}
	return Registration{}, ErrLookupUnanswered
}

// UploadFile streams the plaintext through the library's encryption into scratch and uploads it.
func (s *Session) UploadFile(ctx context.Context, plaintext io.Reader, scratch io.ReadWriteSeeker, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	return s.client.UploadReader(ctx, plaintext, scratch, kind)
}

// Deliver sends one message and waits for the acknowledgement until ctx ends; the library's own shorter wait for
// it is turned off.
func (s *Session) Deliver(ctx context.Context, to types.JID, message *waE2E.Message) (Delivery, error) {
	id := s.client.GenerateMessageID()
	response, err := s.client.SendMessage(ctx, to, message, whatsmeow.SendRequestExtra{ID: id, Timeout: -1})
	if err != nil {
		return Delivery{ID: id}, err
	}
	at := response.Timestamp
	if at.IsZero() || at.Unix() <= 0 {
		at = time.Now()
	}
	return Delivery{ID: id, Time: at}, nil
}

// RecordSent writes an acknowledged message through ingest's serialized live-write path.
func (s *Session) RecordSent(ctx context.Context, sent SentMessage) error {
	return s.ingest.recordSent(ctx, sent, s.client.Store.GetJID().ToNonAD())
}
