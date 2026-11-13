package wa

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

const testLID = "100000000042"

func lidJID() types.JID { return types.NewJID(testLID, types.HiddenUserServer) }

// lidRig is an ingest rig with the device's LID store, whose mapping changes reach ingest as in the session.
type lidRig struct {
	*ingestRig
	f   *faultyStore
	dev *wastore.Device
}

func newLIDRig(t *testing.T, opts ...func(*IngestConfig)) *lidRig {
	t.Helper()
	r := &lidRig{f: &faultyStore{}}
	r.ingestRig = newIngestRig(t, append([]func(*IngestConfig){withFaultyStore(r.f)}, opts...)...)
	r.in.hintRetry = 20 * time.Millisecond
	dev, barrier := savedDevice(t, r.st)
	t.Cleanup(func() { _ = barrier.Retire(context.Background()) })
	barrier.SetMappingObserver(r.in.NoteMapping)
	r.dev = dev
	return r
}

// savedDevice loads the store's device and saves it as a linked account, so its guarded stores accept writes.
func savedDevice(t *testing.T, st *store.Store) (*wastore.Device, *store.DeviceBarrier) {
	t.Helper()
	dev, barrier, err := st.LoadDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	jid := types.NewJID(testOwnPhone, types.DefaultUserServer)
	dev.ID = &jid
	dev.PushName = "Test Account"
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("details"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := dev.Save(context.Background()); err != nil {
		t.Fatalf("device save: %v", err)
	}
	return dev, barrier
}

func (r *lidRig) mapLID(t *testing.T, lid, pn types.JID) {
	t.Helper()
	if err := r.dev.LIDs.PutLIDMapping(context.Background(), lid, pn); err != nil {
		t.Fatal(err)
	}
}

// waitCounter waits until get reports at least want.
func waitCounter(t *testing.T, what string, get func() uint64, want uint64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for get() < want {
		if time.Now().After(deadline) {
			t.Fatalf("%s = %d, want %d", what, get(), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func lidMessage(id string, chat, sender types.JID) *events.Message {
	evt := liveEvent(id, &waE2E.Message{Conversation: proto.String("via lid")})
	evt.Info.Chat, evt.Info.Sender = chat, sender
	evt.Info.IsGroup = chat.Server == types.GroupServer
	return evt
}

func TestIngestLIDNormalisesMappedIdentities(t *testing.T) {
	r := newLIDRig(t)
	r.mapLID(t, lidJID(), peerJID())
	if !r.in.HandleMessage(lidMessage("N1", lidJID(), lidJID())) {
		t.Fatal("returned false")
	}
	got := getMessage(t, r.st, peerJID().String(), "N1")
	if got.SenderJID != peerJID().String() || got.SenderPhone != testPeer {
		t.Fatalf("stored as chat %s sender %q phone %q", got.ChatJID, got.SenderJID, got.SenderPhone)
	}
	if !r.in.HandleMessage(lidMessage("N2", groupJID(), lidJID())) {
		t.Fatal("returned false")
	}
	if got := getMessage(t, r.st, groupJID().String(), "N2"); got.SenderJID != peerJID().String() {
		t.Fatalf("group sender %q, want the phone JID", got.SenderJID)
	}
}

func TestIngestLIDKeepsAnUnmappedLID(t *testing.T) {
	r := newLIDRig(t)
	if !r.in.HandleMessage(lidMessage("U1", lidJID(), lidJID())) {
		t.Fatal("returned false")
	}
	got := getMessage(t, r.st, lidJID().String(), "U1")
	if got.SenderJID != lidJID().String() || got.SenderPhone != "" {
		t.Fatalf("sender %q phone %q, want the LID and no phone", got.SenderJID, got.SenderPhone)
	}
}

// slowLookups makes every LID lookup fail the way a slow or broken store does.
type slowLookups struct {
	ingestStore
	err   error
	block bool
}

func (s *slowLookups) PhoneForLID(ctx context.Context, lid string) (string, bool, error) {
	if s.block {
		<-ctx.Done()
		return "", false, ctx.Err()
	}
	if s.err != nil {
		return "", false, s.err
	}
	return s.ingestStore.PhoneForLID(ctx, lid)
}

func TestIngestLIDLookupFailureKeepsTheLID(t *testing.T) {
	for name, s := range map[string]*slowLookups{
		"error":    {err: errors.New("synthetic lookup failure")},
		"deadline": {block: true},
	} {
		t.Run(name, func(t *testing.T) {
			r := newIngestRig(t, func(cfg *IngestConfig) { s.ingestStore = cfg.Store; cfg.store = s })
			r.in.lidLookup = 30 * time.Millisecond
			began := time.Now()
			if !r.in.HandleMessage(lidMessage("K1", lidJID(), lidJID())) {
				t.Fatal("a failed lookup made the message fail")
			}
			if d := time.Since(began); d > 2*time.Second {
				t.Fatalf("the message took %v; each lookup has a deadline", d)
			}
			if got := getMessage(t, r.st, lidJID().String(), "K1"); got.SenderJID != lidJID().String() {
				t.Fatalf("sender %q, want the LID kept", got.SenderJID)
			}
		})
	}
}

func TestIngestLIDNoteMappingMergesWithoutRestart(t *testing.T) {
	r := newLIDRig(t)
	r.in.HandleMessage(lidMessage("M1", lidJID(), lidJID()))
	r.mapLID(t, lidJID(), peerJID())
	waitCounter(t, "merged", func() uint64 { return r.in.Counters().Merged }, 1)
	got := getMessage(t, r.st, peerJID().String(), "M1")
	if got.SenderJID != peerJID().String() {
		t.Fatalf("after the merge the message sender is %q", got.SenderJID)
	}
}

func TestIngestLIDHintRetriedUntilTheMappingIsVisible(t *testing.T) {
	r := newLIDRig(t)
	r.in.HandleMessage(lidMessage("V1", lidJID(), lidJID()))
	// The library reports the mapping inside a transaction that has not committed yet.
	r.in.NoteMapping(lidJID().String(), "", peerJID().String())
	time.Sleep(3 * r.in.hintRetry)
	r.mapLID(t, lidJID(), peerJID())
	waitCounter(t, "merged", func() uint64 { return r.in.Counters().Merged }, 1)
	if c := r.in.Counters(); c.HintsDropped != 0 {
		t.Fatalf("hint dropped although the mapping appeared within the attempts: %+v", c)
	}
}

func TestIngestLIDHintDroppedAfterFiveAttempts(t *testing.T) {
	r := newLIDRig(t)
	r.in.NoteMapping(lidJID().String(), "", peerJID().String())
	waitCounter(t, "hintsDropped", func() uint64 { return r.in.Counters().HintsDropped }, 1)
	if c := r.in.Counters(); c.Merged != 0 {
		t.Fatalf("merged %d without a mapping", c.Merged)
	}
}

func TestIngestLIDStopDuringRetriesEndsTheWorker(t *testing.T) {
	r := newLIDRig(t)
	r.in.hintRetry = time.Hour
	r.in.NoteMapping(lidJID().String(), "", peerJID().String())
	waitCounter(t, "hint attempts", func() uint64 { return uint64(r.in.hintAttempts.Load()) }, 1)
	r.in.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.in.Wait(ctx); err != nil {
		t.Fatalf("the reconcile worker did not stop during its retry wait: %v", err)
	}
}

// failingMerge fails every merge.
type failingMerge struct{ ingestStore }

func (failingMerge) MergeLID(context.Context, string, string) error {
	return fmt.Errorf("synthetic: %w", store.ErrDiskLow)
}

func TestIngestLIDFailedMergeIsNotTerminal(t *testing.T) {
	r := newLIDRig(t, func(cfg *IngestConfig) { cfg.store = failingMerge{cfg.store} })
	r.in.HandleMessage(lidMessage("F1", lidJID(), lidJID()))
	r.mapLID(t, lidJID(), peerJID())
	waitCounter(t, "mergeFailed", func() uint64 { return r.in.Counters().MergeFailed }, 1)
	if r.in.State() != ingestRunning || r.failed.Load() != 0 {
		t.Fatalf("a failed merge made ingest %v", r.in.State())
	}
	evs := r.logs.events("lid_merge_failed")
	if len(evs) != 1 || evs[0]["lid"] != strconv.QuoteToASCII(lidJID().String()) || evs[0]["phone"] != strconv.QuoteToASCII(peerJID().String()) {
		t.Fatalf("lid_merge_failed = %v", evs)
	}
	if got := getMessage(t, r.st, lidJID().String(), "F1"); got.SenderJID != lidJID().String() {
		t.Fatal("a failed merge changed the data")
	}
}

func TestIngestLIDUnchangedUpsertGivesNoHint(t *testing.T) {
	r := newLIDRig(t)
	r.mapLID(t, lidJID(), peerJID())
	waitCounter(t, "hint attempts", func() uint64 { return uint64(r.in.hintAttempts.Load()) }, 1)
	before := r.in.hintAttempts.Load()
	r.mapLID(t, lidJID(), peerJID())
	r.in.NoteMapping("", "", "") // an empty report changes nothing either
	time.Sleep(5 * r.in.hintRetry)
	if after := r.in.hintAttempts.Load(); after != before {
		t.Fatalf("an unchanged upsert produced %d more lookups", after-before)
	}
}

func TestIngestLIDReassignmentIsLoggedNotUndone(t *testing.T) {
	r := newLIDRig(t)
	other := types.NewJID("491700000000", types.DefaultUserServer)
	r.mapLID(t, lidJID(), peerJID())
	r.mapLID(t, lidJID(), other)
	evs := r.logs.events("lid_reassigned")
	if len(evs) != 1 || evs[0]["lid"] != strconv.QuoteToASCII(lidJID().String()) ||
		evs[0]["old_phone"] != strconv.QuoteToASCII(peerJID().String()) || evs[0]["new_phone"] != strconv.QuoteToASCII(other.String()) {
		t.Fatalf("lid_reassigned = %v", evs)
	}
}

func TestIngestLIDHintSetIsBounded(t *testing.T) {
	r := newLIDRig(t)
	r.in.hintRetry = time.Hour
	// The worker holds the first hint in its retry wait; the set then fills.
	for i := range 1026 {
		r.in.NoteMapping(types.NewJID(strconv.Itoa(200000+i), types.HiddenUserServer).String(), "", peerJID().String())
	}
	for i := range 3 {
		r.in.NoteMapping(types.NewJID(strconv.Itoa(200000+i), types.HiddenUserServer).String(), "", peerJID().String())
	}
	r.in.mu.Lock()
	size := len(r.in.hints)
	r.in.mu.Unlock()
	if size > 1024 {
		t.Fatalf("hint set holds %d, bound is 1,024", size)
	}
	if c := r.in.Counters(); c.HintsDropped < 1 {
		t.Fatalf("hintsDropped = %d, want the hints over the bound counted", c.HintsDropped)
	}
}

// A message normalised before its mapping existed, whose mapping is written and merged before the message takes
// the lock, is still written under the phone JID: the lookup is repeated under the lock.
func TestIngestLIDRelookupUnderTheLock(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		r := newLIDRig(t)
		var once sync.Once
		r.in.beforeWriteLock = func() {
			once.Do(func() {
				r.mapLID(t, lidJID(), peerJID())
				waitCounter(t, "merged", func() uint64 { return r.in.Counters().Merged }, 1)
			})
		}
		r.in.HandleMessage(lidMessage("R1", lidJID(), lidJID()))
		got := getMessage(t, r.st, peerJID().String(), "R1")
		if got.SenderJID != peerJID().String() {
			t.Fatalf("written as %s/%s after the merge", got.ChatJID, got.SenderJID)
		}
	})
	t.Run("history", func(t *testing.T) {
		h := newHistoryRig(t, historyOf(conversation(lidJID(), "", webText("R2", "x", histTime))))
		h.in.hintRetry = 20 * time.Millisecond
		dev, barrier := savedDevice(t, h.st)
		defer barrier.Retire(context.Background())
		barrier.SetMappingObserver(h.in.NoteMapping)
		var once sync.Once
		h.in.beforeWriteLock = func() {
			once.Do(func() {
				if err := dev.LIDs.PutLIDMapping(context.Background(), lidJID(), peerJID()); err != nil {
					t.Error(err)
				}
				waitCounter(t, "merged", func() uint64 { return h.in.Counters().Merged }, 1)
			})
		}
		h.deliver(t, 1)
		if got := getMessage(t, h.st, peerJID().String(), "R2"); got.SenderJID != peerJID().String() {
			t.Fatalf("history written under %q after the merge", got.SenderJID)
		}
	})
}

// A flush with many unmapped LIDs and a slow store re-looks them up under one deadline and writes the rest as LIDs.
func TestIngestLIDRelookupOfAFlushHasOneDeadline(t *testing.T) {
	var convs []*waHistorySync.Conversation
	for i := range 250 {
		lid := types.NewJID(strconv.Itoa(300000+i), types.HiddenUserServer)
		convs = append(convs, conversation(lid, "", webText(fmt.Sprintf("D%03d", i), "x", histTime)))
	}
	h := newHistoryRig(t, historyOf(convs...))
	slow := &slowLookups{ingestStore: h.in.st, block: true}
	h.in.st = slow
	h.in.lidLookup = 10 * time.Millisecond
	h.deliver(t, 1)
	if n := messageCount(t, h.st); n != 250 {
		t.Fatalf("%d stored, want all 250 under their LIDs", n)
	}
	// One lookup deadline per LID under the lock would hold it 250 times 10 ms; the flush has one deadline.
	if held := time.Duration(h.in.maxLockHold.Load()); held > time.Second+h.in.lidLookup {
		t.Fatalf("the lock was held %v for one flush; the re-lookup has one deadline of %v", held, h.in.lidLookup)
	}
}

// The first generation continues an unfinished startup pass from its cursor, one merge at a time.
func TestIngestLIDWorkerContinuesFromTheStartupCursor(t *testing.T) {
	var lids []types.JID
	for i := range 3 {
		lids = append(lids, types.NewJID(strconv.Itoa(400000+i), types.HiddenUserServer))
	}
	r := newIngestRig(t, func(cfg *IngestConfig) { cfg.ReconcileFrom = &LIDCursor{After: lids[0].String()} })
	for i, lid := range lids {
		if err := r.st.StoreMessage(context.Background(), store.Message{ChatJID: lid.String(), ID: fmt.Sprintf("C%d", i),
			SenderJID: lid.String(), Time: histTime, Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	// The mappings were written before this generation, so no hint reports them.
	dev, barrier := savedDevice(t, r.st)
	for i, lid := range lids {
		if err := dev.LIDs.PutLIDMapping(context.Background(), lid, types.NewJID(strconv.Itoa(491600000000+i), types.DefaultUserServer)); err != nil {
			t.Fatal(err)
		}
	}
	_ = barrier.Retire(context.Background())
	r.in.startReconcile()
	waitCounter(t, "merged from the cursor", func() uint64 { return r.in.Counters().Merged }, 2)
	if got := getMessage(t, r.st, lids[0].String(), "C0"); got.SenderJID != lids[0].String() {
		t.Fatal("the LID before the cursor was merged by the continuation")
	}
	if got := getMessage(t, r.st, "491600000002@s.whatsapp.net", "C2"); got.SenderJID != "491600000002@s.whatsapp.net" {
		t.Fatalf("the last LID was not merged: %+v", got)
	}
}

func TestOwnerHandsTheReconcileCursorToTheFirstGeneration(t *testing.T) {
	f := newTerminalFake()
	o := NewOwner(OwnerConfig{
		Versions:      bundledProvider(context.Background()),
		ReconcileFrom: &LIDCursor{After: "100000000009@lid"},
		Sessions:      func(cfg SessionConfig) (SessionHandle, error) { f.cfg = cfg; return f, nil },
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { o.BeginStop(); <-o.Done() }()
	waitFor(t, func() bool { return f.connectN.Load() == 1 })
	if f.cfg.ReconcileFrom == nil || f.cfg.ReconcileFrom.After != "100000000009@lid" {
		t.Fatalf("first generation cursor %+v", f.cfg.ReconcileFrom)
	}
}
