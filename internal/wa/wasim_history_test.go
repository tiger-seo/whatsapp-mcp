package wa

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"google.golang.org/protobuf/proto"
)

// historyPhone is the account's own phone as a sender: history notifications come from it.
func (r *ingestSimRig) historyPhone(t testing.TB) *wasim.Peer {
	t.Helper()
	p, err := wasim.NewPeer(ownJID())
	if err != nil {
		t.Fatal(err)
	}
	p.Category = "peer"
	return p
}

// simHistory is a history sync of conversations chats with perChat text messages each, ids prefixed by tag.
func simHistory(tag string, conversations, perChat int, base time.Time) *waHistorySync.HistorySync {
	hs := &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum()}
	for c := 0; c < conversations; c++ {
		chat := fmt.Sprintf("49151%07d@s.whatsapp.net", c)
		conv := &waHistorySync.Conversation{ID: proto.String(chat), DisplayName: proto.String(fmt.Sprintf("Chat %s %d", tag, c))}
		for m := 0; m < perChat; m++ {
			conv.Messages = append(conv.Messages, &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
				Key:              &waCommon.MessageKey{RemoteJID: proto.String(chat), FromMe: proto.Bool(false), ID: proto.String(fmt.Sprintf("%s-%d-%d", tag, c, m))},
				Message:          &waE2E.Message{Conversation: proto.String(fmt.Sprintf("history %s %d/%d", tag, c, m))},
				MessageTimestamp: proto.Uint64(uint64(base.Add(time.Duration(m) * time.Second).Unix())),
			}})
		}
		hs.Conversations = append(hs.Conversations, conv)
	}
	return hs
}

// sendHistory serves hs on the media path and sends the notification from the account's own phone.
func (r *ingestSimRig) sendHistory(ctx context.Context, phone *wasim.Peer, id string, hs *waHistorySync.HistorySync, order uint32) {
	r.t.Helper()
	n, err := r.srv.HistoryNotification(hs, order)
	if err != nil {
		r.t.Fatal(err)
	}
	node, err := phone.MessageNode(ctx, r.keys, id, time.Now(), wasim.HistoryMessage(n))
	if err != nil {
		r.t.Fatal(err)
	}
	r.send(ctx, node)
}

func TestSimulatorIngestStoresAHistoryChunkDownloadedFromTheMediaPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := newIngestSimRig(t, ctx, nil)
	done := make(chan struct{})
	r.session().ingest.cfg.afterHistory = func() { close(done) }
	r.sendHistory(ctx, r.historyPhone(t), "HIST-1", simHistory("h", 3, 4, time.Now().Add(-time.Hour)), 1)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("the history worker never finished a notification\n%v", r.log.lines)
	}
	if c := r.session().ingest.Counters(); c.Stored != 12 || c.HistoryIncomplete != 0 {
		t.Errorf("counters = %+v, want 12 stored and no incomplete history", c)
	}
	if got := getMessage(t, r.st, "491510000001@s.whatsapp.net", "h-1-2"); got.Text != "history h 1/2" {
		t.Errorf("history message = %+v", got)
	}
	if err := r.stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
}
