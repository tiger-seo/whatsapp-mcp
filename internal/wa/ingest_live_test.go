package wa

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// faultyStore is the real store with message writes that fail on demand.
type faultyStore struct {
	ingestStore
	mu      sync.Mutex
	err     error // returned by every message write while set
	writes  int   // message writes attempted
	failOn  int   // when non-zero, only this write (1-based) fails, with failErr
	failErr error
	batches []int                // the size of every message write that committed
	names   [][]store.ChatUpdate // every chat update call
	// onWrite runs inside each message write, before it reaches the store, while ingest holds its lock.
	onWrite func(n int, msgs []store.Message)
}

func (f *faultyStore) setErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func (f *faultyStore) StoreMessage(ctx context.Context, m store.Message) error {
	return f.StoreMessages(ctx, []store.Message{m})
}

func (f *faultyStore) StoreMessages(ctx context.Context, msgs []store.Message) error {
	f.mu.Lock()
	f.writes++
	n, err, hook := f.writes, f.err, f.onWrite
	if f.failOn != 0 && n == f.failOn {
		err = f.failErr
	}
	f.mu.Unlock()
	if hook != nil {
		hook(n, msgs)
	}
	if err != nil {
		return err
	}
	if err := f.ingestStore.StoreMessages(ctx, msgs); err != nil {
		return err
	}
	f.mu.Lock()
	f.batches = append(f.batches, len(msgs))
	f.mu.Unlock()
	return nil
}

func (f *faultyStore) UpdateChats(ctx context.Context, u []store.ChatUpdate) error {
	f.mu.Lock()
	f.names = append(f.names, append([]store.ChatUpdate(nil), u...))
	f.mu.Unlock()
	return f.ingestStore.UpdateChats(ctx, u)
}

func (f *faultyStore) committed() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.batches...)
}

// withFaultyStore puts a faultyStore between the rig's ingest and its store.
func withFaultyStore(f *faultyStore) func(*IngestConfig) {
	return func(cfg *IngestConfig) {
		f.ingestStore = cfg.Store
		cfg.store = f
	}
}

func getMessage(t *testing.T, st *store.Store, chat, id string) store.MessageView {
	t.Helper()
	v, err := st.GetMessage(context.Background(), chat, id)
	if err != nil {
		t.Fatalf("message %s/%s: %v", chat, id, err)
	}
	return v
}

func TestIngestLiveSenders(t *testing.T) {
	r := newIngestRig(t)
	own := liveEvent("OWN", &waE2E.Message{Conversation: proto.String("mine")})
	own.Info.IsFromMe = true
	own.Info.Sender = types.NewADJID(testOwnPhone, 0, 3)
	group := liveEvent("GRP", &waE2E.Message{Conversation: proto.String("to the group")})
	group.Info.Chat, group.Info.IsGroup = groupJID(), true
	group.Info.Sender = types.NewADJID(testPeer, 0, 7)
	direct := liveEvent("DIR", &waE2E.Message{Conversation: proto.String("hi")})
	direct.Info.Sender = types.NewADJID(testPeer, 0, 2)
	for _, evt := range []*events.Message{own, group, direct} {
		if !r.in.HandleMessage(evt) {
			t.Fatalf("%s returned false", evt.Info.ID)
		}
	}
	if got := getMessage(t, r.st, peerJID().String(), "OWN"); got.SenderJID != ownJID().String() || !got.FromMe {
		t.Errorf("own message sender %q fromMe %v, want the account's phone JID", got.SenderJID, got.FromMe)
	}
	if got := getMessage(t, r.st, groupJID().String(), "GRP"); got.SenderJID != peerJID().String() {
		t.Errorf("group sender %q, want %q without the device part", got.SenderJID, peerJID())
	}
	if got := getMessage(t, r.st, peerJID().String(), "DIR"); got.SenderJID != peerJID().String() || got.Text != "hi" {
		t.Errorf("direct sender %q text %q", got.SenderJID, got.Text)
	}
	if c := r.in.Counters(); c.Stored != 3 {
		t.Errorf("stored = %d, want 3", c.Stored)
	}
}

func TestIngestLiveDuplicatesAndChats(t *testing.T) {
	r := newIngestRig(t)
	if !r.in.HandleMessage(liveEvent("DUP", imageMessage("first"))) {
		t.Fatal("first write failed")
	}
	again := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("second"), Mimetype: proto.String("image/jpeg")}}
	if !r.in.HandleMessage(liveEvent("DUP", again)) {
		t.Fatal("duplicate write failed")
	}
	other := liveEvent("DUP", &waE2E.Message{Conversation: proto.String("same ID, other chat")})
	other.Info.Chat, other.Info.IsGroup = groupJID(), true
	if !r.in.HandleMessage(other) {
		t.Fatal("same ID in another chat failed")
	}
	hits, err := r.st.ListMessages(context.Background(), store.MessageFilter{}, store.Context{}, store.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("%d rows, want one per (chat, ID)", len(hits))
	}
	_, media, err := r.st.MediaInfo(context.Background(), peerJID().String(), "DUP")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(media.MediaKey, key32(1)) || media.URL == "" {
		t.Errorf("the duplicate erased stored media metadata: %+v", media)
	}
}

func TestIngestLiveResultsAndLiveCallback(t *testing.T) {
	r := newIngestRig(t)
	if !r.in.HandleMessage(liveEvent("OK1", &waE2E.Message{Conversation: proto.String("one")})) {
		t.Fatal("stored message returned false")
	}
	for _, evt := range ignoredEvents() {
		if !r.in.HandleMessage(evt) {
			t.Fatalf("ignored %s returned false", evt.Info.ID)
		}
	}
	for _, evt := range rejectedEvents() {
		if !r.in.HandleMessage(evt) {
			t.Fatalf("rejected %q returned false", evt.Info.ID)
		}
	}
	live := r.liveMessages()
	if len(live) != 1 || live[0].ID != "OK1" || live[0].Text != "one" || live[0].ChatJID != peerJID().String() {
		t.Fatalf("Live calls %+v, want exactly the stored message", live)
	}
}

func TestIngestLiveStoreErrorIsTerminal(t *testing.T) {
	f := &faultyStore{}
	r := newIngestRig(t, withFaultyStore(f))
	f.setErr(fmt.Errorf("synthetic: %w", store.ErrDiskLow))
	id := "ID-é"
	evt := liveEvent(id, &waE2E.Message{Conversation: proto.String("secret words")})
	if r.in.HandleMessage(evt) {
		t.Fatal("a failed write returned true; the message would be acknowledged")
	}
	if r.in.State() != ingestFailed || r.failed.Load() != 1 {
		t.Fatalf("state %v, Failed calls %d", r.in.State(), r.failed.Load())
	}
	f.setErr(nil)
	for i := range 3 {
		if r.in.HandleMessage(liveEvent(fmt.Sprintf("L%d", i), &waE2E.Message{Conversation: proto.String("later")})) {
			t.Fatal("a message after the failure was acknowledged")
		}
	}
	if c := r.in.Counters(); c.DroppedMessages != 3 || c.Stored != 0 {
		t.Fatalf("counters %+v, want 3 dropped and none stored", c)
	}
	if f.writes != 1 {
		t.Fatalf("%d writes, want only the one that failed", f.writes)
	}
	evs := r.logs.events("ingest_failed")
	if len(evs) != 1 {
		t.Fatalf("ingest_failed logged %d times", len(evs))
	}
	ev := evs[0]
	if ev["operation"] != "live" || ev["error_class"] != "disk_low" || ev["level"] != "WARN" {
		t.Errorf("ingest_failed = %v", ev)
	}
	if ev["chat"] != strconv.QuoteToASCII(peerJID().String()) || ev["message_id"] != strconv.QuoteToASCII(id) {
		t.Errorf("chat %q message_id %q, want escaped values", ev["chat"], ev["message_id"])
	}
	if strings.Contains(r.logs.text(slog.LevelInfo), "secret words") {
		t.Error("the message text reached the log")
	}
	if len(r.liveMessages()) != 0 {
		t.Error("Live was called for a message that was not stored")
	}
}

func TestIngestLiveIndeterminateIsReportedAsUncertain(t *testing.T) {
	f := &faultyStore{}
	r := newIngestRig(t, withFaultyStore(f))
	f.setErr(store.ErrIndeterminate)
	if r.in.HandleMessage(liveEvent("IND", &waE2E.Message{Conversation: proto.String("x")})) {
		t.Fatal("an indeterminate write returned true")
	}
	evs := r.logs.events("ingest_failed")
	if len(evs) != 1 || !strings.Contains(evs[0]["error_class"], "may or may not be stored") {
		t.Fatalf("ingest_failed = %v", evs)
	}
}
