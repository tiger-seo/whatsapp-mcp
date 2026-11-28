package wa

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/types"
)

var errNoContent = errors.New("the sent message has no storable content")

// sentStoreMessage is the stored form of a message this account sent, extracted as an incoming message is.
func sentStoreMessage(sent SentMessage, own types.JID) (store.Message, error) {
	m := store.Message{
		ChatJID: sent.To.ToNonAD().String(),
		ID:      sent.ID,
		Time:    sent.Time,
		FromMe:  true,
	}
	if !own.IsEmpty() {
		m.SenderJID = own.ToNonAD().String()
	}
	if sent.Message == nil || !extractContent(sent.Message, &m) {
		return store.Message{}, errNoContent
	}
	return m, nil
}

// recordSent stores a sent message as a live message is stored: under the ingest lock, with the LID mapping looked
// up again under it, so a chat that a merge moved away is not recreated. The wait for the lock, the lookup and the
// write all end with ctx. A failure is reported as a missing record and logged without content; it is not a
// failure of ingest.
func (in *ingest) recordSent(ctx context.Context, sent SentMessage, own types.JID) error {
	err := func() error {
		m, err := sentStoreMessage(sent, own)
		if err != nil {
			return err
		}
		if in.beforeWriteLock != nil {
			in.beforeWriteLock()
		}
		if err := in.lockForLiveCtx(ctx); err != nil {
			return err
		}
		defer in.unlock()
		batch := []store.Message{m}
		in.relookupWithin(ctx, batch, nil)
		return in.st.StoreMessage(ctx, batch[0])
	}()
	if err != nil {
		logRecordMissing(in.log, err)
		return errors.Join(ErrRecordMissing, err)
	}
	return nil
}

// logRecordMissing warns that a sent message is not in the local history. It names no chat, message or text.
func logRecordMissing(log *slog.Logger, err error) {
	class := errorClass(err)
	if errors.Is(err, ErrNotConnected) {
		class = "not_connected"
	}
	log.Warn("a sent message was not recorded locally", "event", "send_record_missing", "error_class", class)
}

// lockForLiveCtx takes the ingest lock as lockForLive does, but gives up when ctx ends.
func (in *ingest) lockForLiveCtx(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	started := time.Now()
	in.liveWaiting.Add(1)
	defer in.liveWaiting.Add(-1)
	select {
	case in.lock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	in.lockedAt = time.Now()
	if in.liveWaited != nil {
		in.liveWaited(in.lockedAt.Sub(started))
	}
	return nil
}
