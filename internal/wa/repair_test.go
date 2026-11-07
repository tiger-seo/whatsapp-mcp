package wa

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// fakeBarrier records the repair protocol without a database.
type fakeBarrier struct {
	mu        sync.Mutex
	pending   []string
	deferred  []string
	fault     atomic.Bool
	beginErr  error
	completed []string
	cancelled int
	began     []string
	completeF func(name string) error
	tickets   map[*store.RepairTicket]string
}

func newFakeBarrier(pending ...string) *fakeBarrier {
	return &fakeBarrier{pending: pending, tickets: map[*store.RepairTicket]string{}}
}

func (b *fakeBarrier) PendingRepairs(context.Context) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.pending...), nil
}

func (b *fakeBarrier) BeginRepair(ctx context.Context, name string) (context.Context, *store.RepairTicket, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.beginErr != nil {
		return nil, nil, b.beginErr
	}
	t := &store.RepairTicket{}
	b.tickets[t] = name
	b.began = append(b.began, name)
	return ctx, t, nil
}

func (b *fakeBarrier) CompleteRepair(_ context.Context, t *store.RepairTicket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	name := b.tickets[t]
	if b.completeF != nil {
		if err := b.completeF(name); err != nil {
			return err
		}
	}
	b.completed = append(b.completed, name)
	rest := b.pending[:0]
	for _, p := range b.pending {
		if p != name {
			rest = append(rest, p)
		}
	}
	b.pending = rest
	return nil
}

func (b *fakeBarrier) CancelRepair(*store.RepairTicket) {
	b.mu.Lock()
	b.cancelled++
	b.mu.Unlock()
}

func (b *fakeBarrier) DeferredCollections() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := b.deferred
	b.deferred = nil
	return d
}

func (b *fakeBarrier) StorageFault() bool { return b.fault.Load() }

type fetchCall struct {
	name string
	full bool
}

// recorder is a fetch function that logs its calls and returns scripted results.
type recorder struct {
	mu    sync.Mutex
	calls []fetchCall
	fn    func(ctx context.Context, name string, full bool) error
}

func (r *recorder) fetch(ctx context.Context, name string, full bool) error {
	r.mu.Lock()
	r.calls = append(r.calls, fetchCall{name, full})
	fn := r.fn
	r.mu.Unlock()
	if fn != nil {
		return fn(ctx, name, full)
	}
	return nil
}

func (r *recorder) snapshot() []fetchCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fetchCall(nil), r.calls...)
}

func TestRepairClearsEachCollectionAfterOneFullResync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBarrier("critical_block", "regular_low")
		r := &recorder{}
		c := newRepairController(b, r.fetch, nil, nil)
		if err := c.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		calls := r.snapshot()
		if len(calls) != 2 || !calls[0].full || !calls[1].full {
			t.Fatalf("calls = %+v; want one full resync per collection", calls)
		}
		if len(b.completed) != 2 || len(b.pending) != 0 {
			t.Fatalf("completed %v, pending %v", b.completed, b.pending)
		}
		// A second pass in the same generation uses no more full resyncs.
		b.pending = []string{"critical_block"}
		if err := c.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(r.snapshot()) != 2 {
			t.Errorf("a second full resync of a collection ran in the same generation: %+v", r.snapshot())
		}
	})
}

func TestRepairDeadlineKeepsMarkerAndHoldsSlotUntilInvocationReturns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBarrier("critical_block", "regular_low")
		release := make(chan struct{})
		returned := make(chan struct{})
		r := &recorder{fn: func(ctx context.Context, _ string, _ bool) error {
			defer close(returned)
			<-release // ignores the context, like a call stuck behind the SDK's sync lock
			return nil
		}}
		slot := make(chan struct{}, 1)
		c := newRepairController(b, r.fetch, slot, nil)
		start := time.Now()
		err := c.run(context.Background())
		if !errors.Is(err, errRepairDeadline) {
			t.Fatalf("run = %v, want the deadline", err)
		}
		if got := time.Since(start); got != repairDeadline {
			t.Errorf("caller waited %v, want %v", got, repairDeadline)
		}
		if len(b.completed) != 0 || b.cancelled == 0 {
			t.Fatalf("a timed-out repair was cleared or not invalidated: completed=%v cancelled=%d", b.completed, b.cancelled)
		}
		if len(slot) != 1 {
			t.Fatal("the slot was released before the invocation returned")
		}
		// No replacement call may bypass the held slot, from this generation or another.
		other := newRepairController(newFakeBarrier("regular_low"), r.fetch, slot, nil)
		if err := other.run(context.Background()); !errors.Is(err, errRepairBusy) {
			t.Fatalf("second generation run = %v, want busy", err)
		}
		if n := len(r.snapshot()); n != 1 {
			t.Fatalf("%d fetch invocations; a replacement call bypassed the slot", n)
		}
		// The late success is discarded: nothing is cleared.
		close(release)
		<-returned
		synctest.Wait()
		if len(b.completed) != 0 {
			t.Fatalf("late success cleared a marker: %v", b.completed)
		}
		if len(slot) != 0 {
			t.Fatal("the slot was not released after the invocation returned")
		}
	})
}

func TestRepairFailureKeepsMarkerAndContinuesWithOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBarrier("critical_block", "regular_low")
		r := &recorder{fn: func(_ context.Context, name string, _ bool) error {
			if name == "critical_block" {
				return errors.New("synthetic failure")
			}
			return nil
		}}
		c := newRepairController(b, r.fetch, nil, nil)
		if err := c.run(context.Background()); err == nil {
			t.Fatal("a failed repair reported success")
		}
		if len(b.completed) != 1 || b.completed[0] != "regular_low" {
			t.Fatalf("completed = %v; only the successful collection may clear", b.completed)
		}
		if len(b.pending) != 1 || b.pending[0] != "critical_block" {
			t.Fatalf("pending = %v; the failed marker must stay", b.pending)
		}
	})
}

func TestRepairWriteFaultDuringCallNeverClears(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBarrier("critical_block", "regular_low")
		r := &recorder{fn: func(context.Context, string, bool) error {
			b.fault.Store(true) // a guarded write failed but upstream discarded the error
			return nil
		}}
		c := newRepairController(b, r.fetch, nil, nil)
		if err := c.run(context.Background()); !errors.Is(err, errRepairFault) {
			t.Fatalf("run = %v, want the fault", err)
		}
		if len(b.completed) != 0 || b.cancelled == 0 {
			t.Fatalf("completed=%v cancelled=%d", b.completed, b.cancelled)
		}
		if n := len(r.snapshot()); n != 1 {
			t.Errorf("%d fetches after a fault; the run must stop", n)
		}
	})
}

func TestRepairBeginFaultStopsWithoutCalling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBarrier("critical_block")
		b.beginErr = store.ErrRepairFault
		r := &recorder{}
		c := newRepairController(b, r.fetch, nil, nil)
		if err := c.run(context.Background()); !errors.Is(err, store.ErrRepairFault) {
			t.Fatalf("run = %v", err)
		}
		if len(r.snapshot()) != 0 {
			t.Error("a fetch ran although the repair could not start")
		}
	})
}

func TestRepairClearRejectionKeepsMarker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBarrier("critical_block")
		b.completeF = func(string) error { return store.ErrRepairInvalid }
		r := &recorder{}
		c := newRepairController(b, r.fetch, nil, nil)
		if err := c.run(context.Background()); !errors.Is(err, store.ErrRepairInvalid) {
			t.Fatalf("run = %v", err)
		}
		if len(b.pending) != 1 {
			t.Error("a rejected clear removed the marker")
		}
		// No replacement fetch and no replay while the marker remains.
		b.deferred = []string{"regular_high"}
		if err := c.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if n := len(r.snapshot()); n != 1 {
			t.Errorf("%d fetches; the spent budget must not allow another full resync", n)
		}
	})
}

func TestRepairReplaysDeferredDemandOnlyAfterRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBarrier("critical_block")
		b.deferred = []string{"regular_high", "regular_low"}
		r := &recorder{fn: func(_ context.Context, name string, full bool) error {
			if name == "regular_high" {
				return errors.New("synthetic replay failure")
			}
			return nil
		}}
		c := newRepairController(b, r.fetch, nil, nil)
		if err := c.run(context.Background()); err != nil {
			t.Fatal(err)
		}
		calls := r.snapshot()
		want := []fetchCall{{"critical_block", true}, {"regular_high", false}, {"regular_low", false}}
		if len(calls) != len(want) {
			t.Fatalf("calls = %+v, want %+v", calls, want)
		}
		for i := range want {
			if calls[i] != want[i] {
				t.Fatalf("calls = %+v, want %+v", calls, want)
			}
		}
		// A failed replay is not retried by a spin: nothing more runs without new demand.
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		if len(r.snapshot()) != 3 {
			t.Errorf("autonomous retry: %+v", r.snapshot())
		}
	})
}

func TestRepairDoesNotReplayWhileAnotherMarkerRemains(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBarrier("critical_block", "regular_low")
		b.deferred = []string{"regular_high"}
		r := &recorder{fn: func(_ context.Context, name string, _ bool) error {
			if name == "regular_low" {
				return errors.New("synthetic failure")
			}
			return nil
		}}
		c := newRepairController(b, r.fetch, nil, nil)
		_ = c.run(context.Background())
		for _, call := range r.snapshot() {
			if !call.full {
				t.Fatalf("ordinary replay ran while a marker remained: %+v", r.snapshot())
			}
		}
	})
}

func TestRepairCancellationStopsWithoutClearing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBarrier("critical_block", "regular_low")
		ctx, cancel := context.WithCancel(context.Background())
		r := &recorder{fn: func(ctx context.Context, _ string, _ bool) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		}}
		c := newRepairController(b, r.fetch, nil, nil)
		if err := c.run(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("run = %v, want cancellation", err)
		}
		if len(b.completed) != 0 {
			t.Errorf("cancelled repair cleared %v", b.completed)
		}
		if n := len(r.snapshot()); n != 1 {
			t.Errorf("%d fetches after cancellation", n)
		}
	})
}

// repairingSession is a fake generation whose repair pass the test controls.
type repairingSession struct {
	fakeSession
	pendingNow atomic.Bool
	runs       atomic.Int64
	enter      chan struct{}
	release    chan struct{}
	finish     func()
}

func (s *repairingSession) RecoveryPending() bool { return s.pendingNow.Load() }

func (s *repairingSession) RunRepairs(ctx context.Context) error {
	s.runs.Add(1)
	if s.enter != nil {
		close(s.enter)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.finish != nil {
		s.finish()
	}
	return nil
}

func newRepairingOwner(t *testing.T, s *repairingSession) *ownerTest {
	t.Helper()
	ot := newOwnerTest(t, true, true, nil)
	ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) {
		s.cfg = cfg
		s.identity = true
		return s, nil
	}
	return ot
}

func TestOwnerRunsRepairOnceAfterConnectedAndClearsHealth(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &repairingSession{}
		s.pendingNow.Store(true)
		s.finish = func() { s.pendingNow.Store(false) }
		ot := newRepairingOwner(t, s)
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if s.runs.Load() != 0 {
			t.Fatal("repair started before a confirmed connection")
		}
		if !ot.o.Health().RecoveryPending {
			t.Fatal("pending repair was not reported at open")
		}
		s.send(SessionConnected)
		synctest.Wait()
		s.send(SessionDisconnected)
		s.send(SessionConnected)
		synctest.Wait()
		if s.runs.Load() != 1 {
			t.Errorf("repair passes = %d; one pass per generation", s.runs.Load())
		}
		if h := ot.o.Health(); h.RecoveryPending {
			t.Errorf("health after a completed repair: %+v", h)
		}
		stopInBubble(t, ot)
	})
}

func TestOwnerSkipsRepairWithoutPendingMarkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &repairingSession{}
		ot := newRepairingOwner(t, s)
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		s.send(SessionConnected)
		synctest.Wait()
		if s.runs.Load() != 0 {
			t.Error("a repair pass ran without pending markers")
		}
		stopInBubble(t, ot)
	})
}

func TestOwnerRetirementJoinsRunningRepair(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &repairingSession{enter: make(chan struct{}), release: make(chan struct{})}
		s.pendingNow.Store(true)
		ot := newRepairingOwner(t, s)
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		s.send(SessionConnected)
		<-s.enter
		ot.o.BeginStop()
		synctest.Wait()
		select {
		case <-ot.o.Done():
		default:
			t.Fatal("stop did not finish; the repair pass must end with the generation lifetime")
		}
		if s.retiredN.Load() != 1 {
			t.Errorf("retired %d times", s.retiredN.Load())
		}
		if !ot.o.Health().RecoveryPending {
			t.Error("an interrupted repair must leave recovery pending")
		}
	})
}
