package wa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

var histTime = time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)

// libraryParser is the pinned library's own ParseWebMessage for an account linked as device 3, so history tests
// get the library's sender rules, not a copy of them.
func libraryParser() func(types.JID, *waWeb.WebMessageInfo) (*events.Message, error) {
	own := types.NewADJID(testOwnPhone, 0, 3)
	lid := types.NewADJID(testOwnPhone, 0, 3)
	lid.Server = types.HiddenUserServer
	cli := whatsmeow.NewClient(&wastore.Device{ID: &own, LID: lid}, nil)
	return cli.ParseWebMessage
}

// within fails the test when ch does not close or deliver within 10 s, so a missing worker never hangs the run.
func within[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func historyNotice(chunk uint32) *events.Message {
	evt := historyNotificationEvent(true)
	evt.Message.ProtocolMessage.HistorySyncNotification.ChunkOrder = proto.Uint32(chunk)
	return evt
}

func webText(id, text string, ts time.Time) *waWeb.WebMessageInfo {
	return &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{ID: proto.String(id)},
		MessageTimestamp: proto.Uint64(uint64(ts.Unix())),
		Message:          &waE2E.Message{Conversation: proto.String(text)},
	}
}

func conversation(jid types.JID, name string, msgs ...*waWeb.WebMessageInfo) *waHistorySync.Conversation {
	c := &waHistorySync.Conversation{ID: proto.String(jid.String())}
	if name != "" {
		c.DisplayName = proto.String(name)
	}
	for _, m := range msgs {
		c.Messages = append(c.Messages, &waHistorySync.HistorySyncMsg{Message: m})
	}
	return c
}

func historyOf(convs ...*waHistorySync.Conversation) *waHistorySync.HistorySync {
	return &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_RECENT.Enum(), Conversations: convs}
}

// historyRig is an ingest rig whose fake downloads return the queued histories in order and whose parser is the
// library's. done receives one value per finished notification.
type historyRig struct {
	*ingestRig
	f    *faultyStore
	done chan struct{}
	mu   sync.Mutex
	hist []*waHistorySync.HistorySync
}

func newHistoryRig(t *testing.T, hist ...*waHistorySync.HistorySync) *historyRig {
	t.Helper()
	h := &historyRig{f: &faultyStore{}, done: make(chan struct{}, 64), hist: hist}
	h.ingestRig = newIngestRig(t, withFaultyStore(h.f), func(cfg *IngestConfig) {
		cfg.afterHistory = func() { h.done <- struct{}{} }
	})
	h.client.parse = libraryParser()
	h.client.download = func(ctx context.Context, _ *waE2E.HistorySyncNotification, _ bool) (*waHistorySync.HistorySync, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.hist) == 0 {
			return nil, errors.New("fake: no history left")
		}
		next := h.hist[0]
		h.hist = h.hist[1:]
		return next, nil
	}
	return h
}

// deliver hands the notification to ingest and waits until the worker finished it.
func (h *historyRig) deliver(t *testing.T, chunk uint32) {
	t.Helper()
	if !h.in.HandleMessage(historyNotice(chunk)) {
		t.Fatal("a history notification returned false")
	}
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the history worker did not finish the notification")
	}
}

func messageCount(t *testing.T, st *store.Store) int {
	t.Helper()
	total := 0
	for {
		hits, err := st.ListMessages(context.Background(), store.MessageFilter{}, store.Context{}, store.Page{Limit: 200, Offset: total})
		if err != nil {
			t.Fatal(err)
		}
		total += len(hits)
		if len(hits) < 200 {
			return total
		}
	}
}

func TestIngestHistoryQueuesOnlyOwnNotifications(t *testing.T) {
	h := newHistoryRig(t, historyOf(conversation(peerJID(), "", webText("H1", "old", histTime))))
	var gotLib, gotSync bool
	inner := h.client.download
	h.client.download = func(ctx context.Context, n *waE2E.HistorySyncNotification, sync bool) (*waHistorySync.HistorySync, error) {
		gotLib, gotSync = isLibContext(ctx), sync
		return inner(ctx, n, sync)
	}
	if !h.in.HandleMessage(historyNotificationEvent(false)) {
		t.Fatal("a notification from another sender returned false")
	}
	if c := h.in.Counters(); c.Ignored != 1 {
		t.Fatalf("a notification from another sender was not ignored: %+v", c)
	}
	h.deliver(t, 1)
	if !gotSync || !gotLib {
		t.Errorf("download synchronous=%v under the library context=%v, want both", gotSync, gotLib)
	}
	if n := len(h.client.downloads); n != 1 {
		t.Fatalf("%d downloads, want 1 (the other sender's notification must not be downloaded)", n)
	}
	if got := getMessage(t, h.st, peerJID().String(), "H1"); got.Text != "old" {
		t.Errorf("history message stored as %+v", got)
	}
	if len(h.liveMessages()) != 0 {
		t.Error("history called Live")
	}
}

func TestIngestHistoryQueueHoldsEight(t *testing.T) {
	h := newHistoryRig(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.client.download = func(ctx context.Context, _ *waE2E.HistorySyncNotification, _ bool) (*waHistorySync.HistorySync, error) {
		once.Do(func() { close(entered) })
		<-release
		return historyOf(), nil
	}
	defer close(release)
	if !h.in.HandleMessage(historyNotice(0)) {
		t.Fatal("returned false")
	}
	within(t, entered, "the download to start")
	for i := uint32(1); i <= 9; i++ {
		if !h.in.HandleMessage(historyNotice(i)) {
			t.Fatalf("notification %d returned false; it must be acknowledged even when the queue is full", i)
		}
	}
	evs := h.logs.events("history_incomplete")
	if len(evs) != 1 || evs[0]["reason"] != "queue_full" || evs[0]["chunk_order"] != "9" || evs[0]["sync_type"] != "RECENT" {
		t.Fatalf("history_incomplete = %v, want one queue_full for chunk 9", evs)
	}
	if c := h.in.Counters(); c.HistoryIncomplete != 1 || h.in.State() != ingestRunning {
		t.Fatalf("counters %+v state %v", c, h.in.State())
	}
}

func TestIngestHistoryUsesTheLiveSenderRules(t *testing.T) {
	own := webText("OWN", "mine", histTime)
	own.Key.FromMe = proto.Bool(true)
	inGroup := webText("GRP", "group", histTime)
	inGroup.Participant = proto.String(types.NewADJID(testPeer, 0, 4).String())
	direct := webText("DIR", "direct", histTime)
	h := newHistoryRig(t, historyOf(
		conversation(peerJID(), "", own, direct),
		conversation(groupJID(), "", inGroup),
	))
	h.deliver(t, 1)
	if got := getMessage(t, h.st, peerJID().String(), "OWN"); got.SenderJID != ownJID().String() || !got.FromMe {
		t.Errorf("own history message sender %q fromMe %v", got.SenderJID, got.FromMe)
	}
	if got := getMessage(t, h.st, peerJID().String(), "DIR"); got.SenderJID != peerJID().String() {
		t.Errorf("direct sender %q", got.SenderJID)
	}
	if got := getMessage(t, h.st, groupJID().String(), "GRP"); got.SenderJID != peerJID().String() {
		t.Errorf("group participant %q, want %q", got.SenderJID, peerJID())
	}
}

// bareEdit is a history entry for a MESSAGE_EDIT of target: the library replaces its ID and content with the
// edited ones without marking it an edit.
func bareEdit(target string) *waWeb.WebMessageInfo {
	return &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{ID: proto.String("EDIT-STANZA")},
		MessageTimestamp: proto.Uint64(uint64(histTime.Add(time.Minute).Unix())),
		Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
			Key:           &waCommon.MessageKey{ID: proto.String(target)},
			EditedMessage: &waE2E.Message{Conversation: proto.String("edited text")},
		}},
	}
}

func TestIngestHistoryBareEditNeverOverwritesTheOriginal(t *testing.T) {
	for _, order := range []string{"original first", "edit first"} {
		t.Run(order, func(t *testing.T) {
			orig, edit := webText("ORIG", "original text", histTime), bareEdit("ORIG")
			first, second := historyOf(conversation(peerJID(), "", orig)), historyOf(conversation(peerJID(), "", edit))
			if order == "edit first" {
				first, second = second, first
			}
			h := newHistoryRig(t, first, second)
			h.deliver(t, 1)
			h.deliver(t, 2)
			if got := getMessage(t, h.st, peerJID().String(), "ORIG"); got.Text != "original text" {
				t.Fatalf("original now reads %q", got.Text)
			}
			if n := messageCount(t, h.st); n != 1 {
				t.Fatalf("%d messages, want only the original", n)
			}
		})
	}
}

func TestIngestHistoryFlushesInBoundedBatches(t *testing.T) {
	var msgs []*waWeb.WebMessageInfo
	for i := range 600 {
		msgs = append(msgs, webText(fmt.Sprintf("B%03d", i), "text", histTime.Add(time.Duration(i)*time.Second)))
	}
	h := newHistoryRig(t, historyOf(conversation(peerJID(), "Peer Name", msgs...)))
	h.deliver(t, 1)
	if got := h.f.committed(); fmt.Sprint(got) != "[250 250 100]" {
		t.Fatalf("flushes %v, want [250 250 100]", got)
	}
	// The conversation name is offered once per batch, as a real name.
	h.f.mu.Lock()
	names := h.f.names
	h.f.mu.Unlock()
	if len(names) != 3 {
		t.Fatalf("%d chat updates, want one per flush", len(names))
	}
	for _, u := range names {
		if len(u) != 1 || u[0].Name != "Peer Name" || u[0].NameQuality != store.NameReal || u[0].JID != peerJID().String() {
			t.Fatalf("name offer %+v", u)
		}
	}
	chat, err := h.st.GetChat(context.Background(), peerJID().String())
	if err != nil || chat.Name != "Peer Name" {
		t.Fatalf("chat %+v, %v", chat, err)
	}
	if c := h.in.Counters(); c.Stored != 600 {
		t.Errorf("stored = %d, want 600", c.Stored)
	}
}

func TestIngestHistoryCutsBatchesAtFourMiB(t *testing.T) {
	big := strings.Repeat("x", 200<<10)
	var msgs []*waWeb.WebMessageInfo
	for i := range 30 {
		msgs = append(msgs, webText(fmt.Sprintf("BIG%02d", i), big, histTime))
	}
	h := newHistoryRig(t, historyOf(conversation(peerJID(), "", msgs...)))
	var sizes []int
	h.f.onWrite = func(_ int, batch []store.Message) {
		total := 0
		for _, m := range batch {
			total += store.MessageFieldBytes(m)
		}
		sizes = append(sizes, total)
	}
	h.deliver(t, 1)
	if got := h.f.committed(); fmt.Sprint(got) != "[20 10]" {
		t.Fatalf("flushes %v, want [20 10] (cut before 4 MiB)", got)
	}
	for _, s := range sizes {
		if s > 4<<20 {
			t.Fatalf("a flush of %d field bytes exceeds 4 MiB", s)
		}
	}
}

// A live write that waits on the ingest lock during a flush goes before the next flush.
func TestIngestHistoryLetsAWaitingLiveWriteGoFirst(t *testing.T) {
	var msgs []*waWeb.WebMessageInfo
	for i := range 600 {
		msgs = append(msgs, webText(fmt.Sprintf("Q%03d", i), "text", histTime))
	}
	h := newHistoryRig(t, historyOf(conversation(peerJID(), "", msgs...)))
	liveDone := make(chan bool, 1)
	h.f.onWrite = func(n int, _ []store.Message) {
		if n != 1 {
			return
		}
		go func() {
			liveDone <- h.in.HandleMessage(liveEvent("LIVE", &waE2E.Message{Conversation: proto.String("now")}))
		}()
		for deadline := time.Now().Add(10 * time.Second); h.in.liveWaiting.Load() == 0 && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
	}
	h.deliver(t, 1)
	var ok bool
	select {
	case ok = <-liveDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the live write never ran")
	}
	if !ok {
		t.Fatal("the live write failed")
	}
	if got := h.f.committed(); fmt.Sprint(got) != "[250 1 250 100]" {
		t.Fatalf("write order %v, want the live write between the first two flushes", got)
	}
}

func TestIngestHistoryRejectsAMessageWithoutTime(t *testing.T) {
	noTime := webText("NOTIME", "x", histTime)
	noTime.MessageTimestamp = nil
	h := newHistoryRig(t, historyOf(conversation(peerJID(), "",
		webText("BEFORE", "a", histTime), noTime, webText("AFTER", "b", histTime))))
	h.deliver(t, 1)
	if n := messageCount(t, h.st); n != 2 {
		t.Fatalf("%d stored, want the two neighbours", n)
	}
	if c := h.in.Counters(); c.Rejected != 1 {
		t.Fatalf("rejected = %d, want 1", c.Rejected)
	}
}

func TestIngestHistoryFailuresAreReportedAndNotTerminal(t *testing.T) {
	cases := map[string]struct {
		reason   string
		download error
		parseErr bool
	}{
		"too large":   {reason: "too_large", download: fmt.Errorf("wrapped: %w", whatsmeow.ErrHistorySyncTooLarge)},
		"download":    {reason: "download", download: errors.New("failed to download: 403")},
		"parse error": {reason: "parse", parseErr: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHistoryRig(t, historyOf(conversation(peerJID(), "", webText("P1", "a", histTime), webText("P2", "b", histTime))))
			if c.download != nil {
				h.client.download = func(context.Context, *waE2E.HistorySyncNotification, bool) (*waHistorySync.HistorySync, error) {
					return nil, c.download
				}
			}
			if c.parseErr {
				parse := h.client.parse
				h.client.parse = func(chat types.JID, w *waWeb.WebMessageInfo) (*events.Message, error) {
					if w.GetKey().GetID() == "P1" {
						return nil, errors.New("couldn't find sender")
					}
					return parse(chat, w)
				}
			}
			h.deliver(t, 7)
			evs := h.logs.events("history_incomplete")
			if len(evs) != 1 || evs[0]["reason"] != c.reason || evs[0]["chunk_order"] != "7" || evs[0]["sync_type"] != "RECENT" {
				t.Fatalf("history_incomplete = %v, want one %s for chunk 7", evs, c.reason)
			}
			if h.in.State() != ingestRunning || h.failed.Load() != 0 {
				t.Fatalf("state %v after a %s; ingest must keep running", h.in.State(), c.reason)
			}
			if c.parseErr && messageCount(t, h.st) != 1 {
				t.Fatal("the message after the unparsable one was not stored")
			}
		})
	}
}

func TestIngestHistoryStoreErrorKeepsCommittedBatches(t *testing.T) {
	var msgs []*waWeb.WebMessageInfo
	for i := range 600 {
		msgs = append(msgs, webText(fmt.Sprintf("F%03d", i), "text", histTime))
	}
	h := newHistoryRig(t, historyOf(conversation(peerJID(), "", msgs...)))
	h.f.failOn, h.f.failErr = 2, fmt.Errorf("synthetic: %w", store.ErrDiskLow)
	h.deliver(t, 3)
	if n := messageCount(t, h.st); n != 250 {
		t.Fatalf("%d stored, want the first batch of 250", n)
	}
	if h.in.State() != ingestFailed || h.failed.Load() != 1 {
		t.Fatalf("state %v, Failed calls %d", h.in.State(), h.failed.Load())
	}
	evs := h.logs.events("ingest_failed")
	if len(evs) != 1 || evs[0]["operation"] != "history" || evs[0]["committed"] != "250" || evs[0]["chunk_order"] != "3" {
		t.Fatalf("ingest_failed = %v", evs)
	}
	if h.f.writes != 2 {
		t.Fatalf("%d writes, want none after the failure", h.f.writes)
	}
}

// A live message newer than the history keeps the chat's last activity, whichever arrives first.
func TestIngestHistoryNeverMovesLastActivityBack(t *testing.T) {
	liveAt := histTime.Add(24 * time.Hour)
	for _, order := range []string{"live first", "history first"} {
		t.Run(order, func(t *testing.T) {
			h := newHistoryRig(t, historyOf(conversation(peerJID(), "", webText("OLD", "old", histTime))))
			live := liveEvent("NEW", &waE2E.Message{Conversation: proto.String("new")})
			live.Info.Timestamp = liveAt
			if order == "live first" {
				h.in.HandleMessage(live)
				h.deliver(t, 1)
			} else {
				h.deliver(t, 1)
				h.in.HandleMessage(live)
			}
			chat, err := h.st.GetChat(context.Background(), peerJID().String())
			if err != nil {
				t.Fatal(err)
			}
			if chat.LastActivity == nil || !chat.LastActivity.Equal(liveAt) {
				t.Fatalf("last activity %v, want %v", chat.LastActivity, liveAt)
			}
		})
	}
}

func TestIngestHistoryStopIsObservedAtEachCheckpoint(t *testing.T) {
	t.Run("before a download", func(t *testing.T) {
		h := newHistoryRig(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		h.client.download = func(context.Context, *waE2E.HistorySyncNotification, bool) (*waHistorySync.HistorySync, error) {
			once.Do(func() { close(entered) })
			<-release
			return historyOf(conversation(peerJID(), "", webText("S1", "x", histTime))), nil
		}
		h.in.HandleMessage(historyNotice(1))
		within(t, entered, "the download to start")
		h.in.HandleMessage(historyNotice(2))
		h.in.HandleMessage(historyNotice(3))
		h.in.Stop()
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.in.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		if n := len(h.client.downloads); n != 1 {
			t.Fatalf("%d downloads, want only the one in progress at Stop", n)
		}
		stopped := 0
		for _, ev := range h.logs.events("history_incomplete") {
			if ev["reason"] == "stopped" {
				stopped++
			}
		}
		if stopped != 3 {
			t.Fatalf("%d stopped reports, want the chunk in progress and the two queued", stopped)
		}
		if messageCount(t, h.st) != 0 {
			t.Fatal("a flush ran after Stop")
		}
	})
	t.Run("between conversations and before a flush", func(t *testing.T) {
		h := newHistoryRig(t, historyOf(
			conversation(peerJID(), "", webText("C1", "x", histTime)),
			conversation(groupJID(), "", webText("C2", "y", histTime)),
		))
		parse := h.client.parse
		h.client.parse = func(chat types.JID, w *waWeb.WebMessageInfo) (*events.Message, error) {
			if w.GetKey().GetID() == "C1" {
				h.in.Stop()
			}
			return parse(chat, w)
		}
		h.in.HandleMessage(historyNotice(1))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.in.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		if n := h.client.parses.Load(); n != 1 {
			t.Fatalf("%d messages parsed, want the second conversation skipped", n)
		}
		if h.f.writes != 0 {
			t.Fatal("a flush ran after Stop")
		}
	})
}
