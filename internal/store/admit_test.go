package store

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// shortWaits shortens the gate's waits for one test.
func shortWaits(t *testing.T, admission, grace, cancel time.Duration) {
	t.Helper()
	oa, og, oc := admissionWait, closeGrace, closeCancelWait
	admissionWait, closeGrace, closeCancelWait = admission, grace, cancel
	t.Cleanup(func() { admissionWait, closeGrace, closeCancelWait = oa, og, oc })
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// fill admits n operations and returns their release functions.
func fill(t *testing.T, g *gate, n int) []func() {
	t.Helper()
	var rel []func()
	for i := 0; i < n; i++ {
		r, _, err := g.enter(context.Background())
		if err != nil {
			t.Fatalf("enter %d: %v", i, err)
		}
		rel = append(rel, r)
	}
	return rel
}

func TestGateSeventeenthWaits(t *testing.T) {
	g := newGate()
	rel := fill(t, g, maxActive)
	done := make(chan error, 1)
	go func() {
		r, _, err := g.enter(context.Background())
		if err == nil {
			r()
		}
		done <- err
	}()
	eventually(t, "the 17th caller to wait", func() bool { return g.waitingCount() == 1 })
	select {
	case err := <-done:
		t.Fatalf("the 17th caller was admitted or failed at once: %v", err)
	default:
	}
	rel[0]()
	if err := <-done; err != nil {
		t.Errorf("17th caller: %v", err)
	}
	for _, r := range rel[1:] {
		r()
	}
}

func TestGateWaiterCap(t *testing.T) {
	g := newGate()
	rel := fill(t, g, maxActive)
	var wg sync.WaitGroup
	for i := 0; i < maxWaiting; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _, err := g.enter(context.Background())
			if err == nil {
				r()
			}
		}()
	}
	eventually(t, "64 waiters", func() bool { return g.waitingCount() == maxWaiting })
	start := time.Now()
	if _, _, err := g.enter(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("81st caller: %v, want ErrBusy", err)
	}
	if time.Since(start) > time.Second {
		t.Error("ErrBusy was not immediate")
	}
	for _, r := range rel {
		r()
	}
	wg.Wait()
}

func TestGateWaitEnds(t *testing.T) {
	shortWaits(t, 50*time.Millisecond, closeGrace, closeCancelWait)
	g := newGate()
	rel := fill(t, g, maxActive)
	defer func() {
		for _, r := range rel {
			r()
		}
	}()
	start := time.Now()
	if _, _, err := g.enter(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("after the admission wait: %v, want ErrBusy", err)
	}
	if d := time.Since(start); d < 40*time.Millisecond || d > 2*time.Second {
		t.Errorf("waited %v, want about 50ms", d)
	}

	shortWaits(t, 10*time.Second, closeGrace, closeCancelWait)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := g.enter(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("caller deadline: %v, want DeadlineExceeded", err)
	}
}

func TestGateCancellationFreesSlot(t *testing.T) {
	g := newGate()
	ctx, cancel := context.WithCancel(context.Background())
	rel, opCtx, err := g.enter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-opCtx.Done()
	rel()
	rel() // calling twice is harmless
	if g.activeCount() != 0 {
		t.Errorf("active = %d after release, want 0", g.activeCount())
	}
}

func TestGateOperationLimit(t *testing.T) {
	old := operationLimit
	operationLimit = 50 * time.Millisecond
	t.Cleanup(func() { operationLimit = old })
	g := newGate()
	rel, opCtx, err := g.enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	select {
	case <-opCtx.Done():
		if !errors.Is(opCtx.Err(), context.DeadlineExceeded) {
			t.Errorf("opCtx error = %v", opCtx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Error("the operation context has no deadline")
	}
}

func TestGateClose(t *testing.T) {
	shortWaits(t, 10*time.Second, 200*time.Millisecond, 100*time.Millisecond)
	g := newGate()
	var running sync.WaitGroup
	for i := 0; i < maxActive; i++ {
		rel, opCtx, err := g.enter(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		running.Add(1)
		go func() {
			defer running.Done()
			<-opCtx.Done() // an operation that only ends when cancelled
			rel()
		}()
	}
	waiter := make(chan error, 1)
	go func() {
		_, _, err := g.enter(context.Background())
		waiter <- err
	}()
	eventually(t, "a waiting caller", func() bool { return g.waitingCount() == 1 })

	start := time.Now()
	g.close(context.Background())
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("close took %v", d)
	}
	running.Wait()
	if err := <-waiter; !errors.Is(err, ErrClosed) {
		t.Errorf("waiting caller: %v, want ErrClosed", err)
	}
	if _, _, err := g.enter(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("enter after close: %v, want ErrClosed", err)
	}
}

func TestGateNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		g := newGate()
		rel, _, err := g.enter(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		rel()
		g.close(context.Background())
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Errorf("goroutines: %d before, %d after", before, after)
	}
}

func TestCommitOutcome(t *testing.T) {
	if commitOutcome(context.Background(), nil) != nil {
		t.Error("nil commit error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := commitOutcome(ctx, errors.New("boom")); !errors.Is(err, ErrIndeterminate) {
		t.Errorf("after cancellation: %v, want ErrIndeterminate", err)
	}
	if err := commitOutcome(context.Background(), errors.New("boom")); errors.Is(err, ErrIndeterminate) {
		t.Errorf("a plain failure is indeterminate: %v", err)
	}
}
