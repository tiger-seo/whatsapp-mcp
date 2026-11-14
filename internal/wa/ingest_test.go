package wa

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// textEvent is a live text message in a direct chat with the peer.
func textEvent(id, text string) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: peerJID(), Sender: peerJID()},
			ID:            id, Timestamp: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		},
		Message: &waE2E.Message{Conversation: proto.String(text)},
	}
}

func TestIngestStatesFollowTheTransitions(t *testing.T) {
	r := newIngestRig(t)
	if got := r.in.State(); got != ingestRunning {
		t.Fatalf("new state = %v, want running", got)
	}
	if !r.in.fail("live", errors.New("synthetic store failure")) {
		t.Fatal("a store error while running was not terminal")
	}
	if got := r.in.State(); got != ingestFailed {
		t.Fatalf("after a store error = %v, want failed", got)
	}
	r.in.fail("live", errors.New("second failure"))
	if n := r.failed.Load(); n != 1 {
		t.Fatalf("Failed called %d times, want exactly once", n)
	}
	if evs := r.logs.events("ingest_failed"); len(evs) != 1 {
		t.Fatalf("ingest_failed logged %d times, want once", len(evs))
	}
	if r.in.HandleMessage(textEvent("A", "after failure")) {
		t.Error("a message after the failure was acknowledged")
	}
	r.in.Stop()
	if got := r.in.State(); got != ingestRetiring {
		t.Fatalf("failed then Stop = %v, want retiring", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.in.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.in.State(); got != ingestStopped {
		t.Fatalf("after Wait = %v, want stopped", got)
	}
	r.in.fail("live", errors.New("late"))
	if got := r.in.State(); got != ingestStopped {
		t.Fatalf("a failure after the join moved the state to %v", got)
	}
	if c := r.in.Counters(); c.DroppedMessages != 1 {
		t.Errorf("dropped = %d, want 1", c.DroppedMessages)
	}
}

func TestIngestRunningToRetiringNeverBack(t *testing.T) {
	r := newIngestRig(t)
	if !r.in.HandleMessage(&events.Message{Info: types.MessageInfo{ID: "x"}}) {
		t.Fatal("a running ingest refused an ignorable event")
	}
	r.in.Stop()
	r.in.Stop()
	if got := r.in.State(); got != ingestRetiring {
		t.Fatalf("state = %v, want retiring", got)
	}
	if r.in.fail("live", errors.New("after stop")) {
		t.Error("a failure after Stop was terminal")
	}
	if got := r.in.State(); got != ingestRetiring {
		t.Fatalf("state = %v, want retiring (never failed or running again)", got)
	}
	if r.failed.Load() != 0 {
		t.Error("Failed was called after Stop")
	}
	if r.in.HandleMessage(textEvent("B", "late")) {
		t.Error("a message after Stop was acknowledged")
	}
}

// A context error caused by the generation's own retirement is not an ingest failure.
func TestIngestRetirementContextErrorIsNotAFailure(t *testing.T) {
	r := newIngestRig(t)
	r.in.Stop()
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, store.ErrClosed} {
		if r.in.fail("history", err) {
			t.Errorf("%v after Stop was terminal", err)
		}
	}
	if r.failed.Load() != 0 || r.in.State() == ingestFailed {
		t.Fatal("ingest failed because of its own retirement")
	}
}

func TestIngestStopNeverBlocksAndWaitHonoursItsDeadline(t *testing.T) {
	r := newIngestRig(t)
	release := make(chan struct{})
	r.in.goWorker(func() { <-release })
	done := make(chan struct{})
	go func() { r.in.Stop(); r.in.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked on a running worker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := r.in.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait with a stuck worker = %v, want the deadline", err)
	}
	if got := r.in.State(); got != ingestRetiring {
		t.Fatalf("state before the join = %v, want retiring", got)
	}
	close(release)
	if err := r.in.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.in.State(); got != ingestStopped {
		t.Fatalf("state after the join = %v, want stopped", got)
	}
	// After the join no new worker starts.
	started := make(chan struct{})
	r.in.goWorker(func() { close(started) })
	select {
	case <-started:
		t.Fatal("a worker started after the join")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestIngestCountersStartAtZeroAndOnlyGrow(t *testing.T) {
	r := newIngestRig(t)
	if c := r.in.Counters(); c != (IngestCounters{}) {
		t.Fatalf("initial counters = %+v", c)
	}
	r.in.HandleMessage(&events.Message{Info: types.MessageInfo{ID: "x"}})
	first := r.in.Counters()
	r.in.Stop()
	r.in.HandleMessage(textEvent("y", "late"))
	second := r.in.Counters()
	if second.Ignored < first.Ignored || second.DroppedMessages <= first.DroppedMessages {
		t.Errorf("counters went backwards or did not count: %+v then %+v", first, second)
	}
}
