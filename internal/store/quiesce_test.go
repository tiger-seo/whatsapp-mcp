package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	wastore "go.mau.fi/whatsmeow/store"
)

// openLoadedStore opens a store with a capturing logger, seeds a durable synthetic device and loads it guarded,
// so the close and retirement tests can hold raw-device calls and assert on events.
func openLoadedStore(t testing.TB) (*Store, *logRecords, *wastore.Device, *DeviceBarrier) {
	t.Helper()
	st, recs := openTest(t, newDataDir(t))
	seedDevice(t, st)
	dev, barrier := loadDevice(t, st)
	return st, recs, dev, barrier
}

// holdDeviceSave starts a guarded device Save and holds it at the raw-container boundary until release is
// closed. The hold does not end on cancellation, so a close must wait for the call's actual completion instead
// of assuming its context ended it.
func holdDeviceSave(t testing.TB, st *Store, dev *wastore.Device) (release func(), entered chan struct{}, done chan error) {
	t.Helper()
	entered = make(chan struct{})
	held := make(chan struct{})
	done = make(chan error, 1)
	var once sync.Once
	st.hooks.devicePutHold = func(context.Context) error {
		once.Do(func() { close(entered) })
		<-held
		return nil
	}
	t.Cleanup(func() { st.hooks.devicePutHold = nil })
	go func() { done <- dev.Save(context.Background()) }()
	return func() { close(held) }, entered, done
}

// holdDeviceTransaction holds one guarded DoDecryptionTxn inside its callback until release is closed. The
// callback ignores its context while waiting, so the call keeps its admission slot after a cancellation.
func holdDeviceTransaction(t testing.TB, dev *wastore.Device) (release func(), entered chan struct{}, done chan error) {
	t.Helper()
	entered = make(chan struct{})
	held := make(chan struct{})
	done = make(chan error, 1)
	go func() {
		done <- dev.EventBuffer.DoDecryptionTxn(context.Background(), func(ctx context.Context) error {
			close(entered)
			<-held
			return ctx.Err()
		})
	}()
	return func() { close(held) }, entered, done
}

// TestRetirementKeepsSharedStorageOpen retires a busy generation: new access on the generation is rejected, the
// admitted device write still drains, and the shared store never closes. A fresh generation loads afterwards.
func TestRetirementKeepsSharedStorageOpen(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()

	releaseSave, entered, saveDone := holdDeviceSave(t, st, dev)
	<-entered

	retireDone := make(chan error, 1)
	go func() { retireDone <- barrier.Retire(ctx) }()
	eventually(t, "retirement to reject new generation access", func() bool {
		barrier.sess.mu.Lock()
		defer barrier.sess.mu.Unlock()
		return barrier.sess.retired
	})
	if _, err := dev.Sessions.HasSession(ctx, sessionPN); !errors.Is(err, ErrRetired) {
		t.Errorf("device access during retirement: %v, want ErrRetired", err)
	}
	if _, _, err := st.LoadDevice(ctx); !errors.Is(err, ErrSessionExists) {
		t.Errorf("replacement before drain: %v, want ErrSessionExists", err)
	}
	expired, cancel := context.WithCancel(ctx)
	cancel()
	if err := barrier.Retire(expired); !errors.Is(err, ErrNotQuiescent) {
		t.Errorf("retirement while held: %v, want ErrNotQuiescent", err)
	}
	select {
	case <-barrier.Done():
		t.Fatal("retirement signalled completion before the device call drained")
	default:
	}

	// The shared store is open and its shared budgets still serve application work.
	if err := st.StoreMessage(ctx, msg(alice, "retire-shared", alice, at(0))); err != nil {
		t.Fatalf("an application write while the generation retires: %v", err)
	}
	if st.gate.isClosed() {
		t.Fatal("generation retirement closed the shared admission")
	}

	releaseSave()
	select {
	case err := <-saveDone:
		if err != nil {
			t.Fatalf("the drained device save failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the admitted device save never finished")
	}
	select {
	case err := <-retireDone:
		if err != nil {
			t.Fatalf("Retire after the drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Retire never completed")
	}
	select {
	case <-barrier.Done():
	default:
		t.Fatal("Done did not close after the drain")
	}

	// The store stays open: the reader works and a fresh generation loads.
	if n := countRows(t, st, "whatsmeow_device"); n != 1 {
		t.Errorf("device rows after retirement = %d, want 1", n)
	}
	dev2, barrier2 := loadDevice(t, st)
	assertGuards(t, dev2)
	if err := barrier2.Retire(ctx); err != nil {
		t.Fatalf("second generation retire: %v", err)
	}
	if err := st.StoreMessage(ctx, msg(alice, "retire-shared-2", alice, at(1))); err != nil {
		t.Fatalf("an application write after retirement: %v", err)
	}
}

// TestClosingRejectsNewCallsWhileDraining starts the shared close while a raw-device call is active. Shared
// admission is permanently closed at once, every new call is refused although the pools are still open, and the
// close returns only after the active call drains.
func TestClosingRejectsNewCallsWhileDraining(t *testing.T) {
	st, recs, dev, barrier := openLoadedStore(t)
	ctx := context.Background()

	releaseSave, entered, saveDone := holdDeviceSave(t, st, dev)
	<-entered

	closeDone := make(chan error, 1)
	go func() { closeDone <- st.Close(ctx) }()
	eventually(t, "the closing to reject new calls", func() bool { return st.gate.isClosed() })

	if err := st.StoreMessage(ctx, msg(alice, "late-write", alice, at(0))); !errors.Is(err, ErrClosed) {
		t.Errorf("an application write while closing: %v, want ErrClosed", err)
	}
	if _, _, err := st.LoadDevice(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("LoadDevice while closing: %v, want ErrClosed", err)
	}
	if _, err := dev.Sessions.HasSession(ctx, sessionPN); !errors.Is(err, ErrClosed) {
		t.Errorf("a guarded device call while closing: %v, want ErrClosed", err)
	}
	// The pools are still open while work is active: the reader answers.
	if n := countRows(t, st, "messages"); n != 0 {
		t.Errorf("messages = %d, want 0 (the read must not be refused by a pool close)", n)
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned %v while a raw-device call was still active", err)
	default:
	}

	releaseSave()
	select {
	case err := <-saveDone:
		if err != nil {
			t.Fatalf("the active device save did not drain on the open pools: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the active device save never finished")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close after the drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close never completed after the drain")
	}
	if !st.closed {
		t.Error("closed was not set after the drain and pool close")
	}
	if n := len(recs.events("store.close")); n != 1 {
		t.Errorf("store.close events = %d, want exactly one for the completed close", n)
	}
	if err := st.StoreMessage(ctx, msg(alice, "after-close", alice, at(1))); !errors.Is(err, ErrClosed) {
		t.Errorf("an application write after the close: %v, want ErrClosed", err)
	}
	if _, _, err := st.LoadDevice(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("LoadDevice after the close: %v, want ErrClosed", err)
	}
	if err := st.Close(ctx); err != nil {
		t.Errorf("a second Close: %v, want nil", err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Errorf("retiring the idle generation after the close: %v", err)
	}
}

// TestExpiredCloseKeepsPoolsOpenWithoutSuccess holds a raw-device call whose hold ignores cancellation and
// closes the store with a short context. The close must report that work is still active, keep the pools open,
// mark nothing closed and log no success; the call keeps its admission slot until it returns, and only the
// final close after the drain completes.
func TestExpiredCloseKeepsPoolsOpenWithoutSuccess(t *testing.T) {
	shortWaits(t, 100*time.Millisecond, 2*time.Second, 1*time.Second)
	st, recs, dev, barrier := openLoadedStore(t)
	ctx := context.Background()

	releaseSave, entered, saveDone := holdDeviceSave(t, st, dev)
	<-entered

	start := time.Now()
	shortCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := st.Close(shortCtx); !errors.Is(err, ErrNotQuiescent) {
		t.Fatalf("the expired close: %v, want ErrNotQuiescent", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the expired close took %v, want its short context", d)
	}
	if st.closed {
		t.Fatal("the expired close marked the store closed")
	}
	if n := len(recs.events("store.close")); n != 0 {
		t.Errorf("the expired close logged %d store.close events, want none", n)
	}
	// The pools are open: the reader answers, and the active call still holds its admission slot.
	if n := countRows(t, st, "whatsmeow_device"); n != 1 {
		t.Errorf("device rows after the expired close = %d, want 1", n)
	}
	if got := st.gate.activeCount(); got != 1 {
		t.Errorf("active operations after the expired close = %d, want the held raw-device call", got)
	}
	// Admission is closed although the pools are open.
	if err := st.StoreMessage(ctx, msg(alice, "during-closing", alice, at(0))); !errors.Is(err, ErrClosed) {
		t.Errorf("an application write during the closing: %v, want ErrClosed", err)
	}

	// The raw-device call remains active until released, regardless of cancellation delivery.
	releaseSave()
	select {
	case <-saveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the held raw-device save never returned")
	}
	if n := countRows(t, st, "whatsmeow_device"); n != 1 {
		t.Errorf("device rows after the refused save = %d, want 1 (nothing was written)", n)
	}

	// The drain completes once the call returns; the final close finishes with a live context.
	if err := st.Close(context.Background()); err != nil {
		t.Fatalf("the final close: %v", err)
	}
	if !st.closed {
		t.Error("closed was not set by the final close")
	}
	if n := len(recs.events("store.close")); n != 1 {
		t.Errorf("store.close events after the final close = %d, want exactly one", n)
	}
	if err := st.StoreMessage(ctx, msg(alice, "after-final", alice, at(1))); !errors.Is(err, ErrClosed) {
		t.Errorf("an application write after the final close: %v, want ErrClosed", err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Errorf("retiring the idle generation after the close: %v", err)
	}
}

// TestHeldDeviceWriteDrainsBeforePoolClose holds a raw-device write, closes the store and releases the write
// while the close waits: the write commits on the live pools, and only then do the pools close.
func TestHeldDeviceWriteDrainsBeforePoolClose(t *testing.T) {
	st, _, dev, barrier := openLoadedStore(t)
	ctx := context.Background()

	releaseSave, entered, saveDone := holdDeviceSave(t, st, dev)
	<-entered

	closeDone := make(chan error, 1)
	go func() { closeDone <- st.Close(ctx) }()
	eventually(t, "the closing to reject new calls", func() bool { return st.gate.isClosed() })
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned %v while the device write was held", err)
	default:
	}

	releaseSave()
	select {
	case err := <-saveDone:
		if err != nil {
			t.Fatalf("the held raw-device write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held raw-device write never finished")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close after the drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close never completed after the drain")
	}
	if !st.closed {
		t.Error("closed was not set after the drain and pool close")
	}
	if err := st.StoreMessage(ctx, msg(alice, "after-drain", alice, at(0))); !errors.Is(err, ErrClosed) {
		t.Errorf("an application write after the close: %v, want ErrClosed", err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Errorf("retiring the idle generation after the close: %v", err)
	}
}

// TestHeldDeviceTransactionDrainsBeforePoolClose holds a guarded device transaction and proves the close waits
// for its actual completion: releasing the callback runs a nested device write inside that transaction, commits
// on the live pools, and only then do the pools close.
func TestHeldDeviceTransactionDrainsBeforePoolClose(t *testing.T) {
	st, _, dev, barrier := openLoadedStore(t)
	ctx := context.Background()

	entered := make(chan struct{})
	held := make(chan struct{})
	txnDone := make(chan error, 1)
	go func() {
		txnDone <- dev.EventBuffer.DoDecryptionTxn(context.Background(), func(tx context.Context) error {
			close(entered)
			<-held
			// The nested write shares the transaction: it can only commit while the pools are open.
			return dev.Sessions.PutSession(tx, sessionPN, sessionBlob(9))
		})
	}()
	<-entered

	closeDone := make(chan error, 1)
	go func() { closeDone <- st.Close(ctx) }()
	eventually(t, "the closing to reject new calls", func() bool { return st.gate.isClosed() })
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned %v while the device transaction was held", err)
	default:
	}
	if n := countRows(t, st, "whatsmeow_sessions"); n != 0 {
		t.Errorf("sessions before the transaction = %d, want 0", n)
	}

	close(held)
	select {
	case err := <-txnDone:
		if err != nil {
			t.Fatalf("the held device transaction failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held device transaction never finished")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close after the drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close never completed after the drain")
	}
	if !st.closed {
		t.Error("closed was not set after the drain and pool close")
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Errorf("retiring the idle generation after the close: %v", err)
	}
}

// TestQuiesceProvesTheDrainAndKeepsPoolsOpen quiesces the shared admission while a raw-device call is active:
// releasing the call lets it commit, Quiesce then proves the drain with nil, the completion signal closes, and
// the pools stay open until a real Close.
func TestQuiesceProvesTheDrainAndKeepsPoolsOpen(t *testing.T) {
	shortWaits(t, 100*time.Millisecond, 2*time.Second, 1*time.Second)
	st, recs, dev, barrier := openLoadedStore(t)
	ctx := context.Background()

	releaseSave, entered, saveDone := holdDeviceSave(t, st, dev)
	<-entered

	quiescedSignal := st.Quiesced()
	if same := st.Quiesced(); same != quiescedSignal {
		t.Error("Quiesced returned different signals for one store")
	}

	quiesceDone := make(chan error, 1)
	go func() { quiesceDone <- st.Quiesce(ctx) }()
	eventually(t, "the quiesce to reject new calls", func() bool { return st.gate.isClosed() })

	releaseSave()
	select {
	case err := <-saveDone:
		if err != nil {
			t.Fatalf("the held raw-device write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held raw-device write never finished")
	}
	select {
	case err := <-quiesceDone:
		if err != nil {
			t.Fatalf("Quiesce after the drain: %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Quiesce never completed after the drain")
	}
	select {
	case <-quiescedSignal:
	default:
		t.Fatal("the completion signal did not close after the drain")
	}
	if st.closed {
		t.Error("Quiesce marked the store closed; only Close closes the pools")
	}
	// The pools are open after quiescence, and admission stays shut.
	if n := countRows(t, st, "whatsmeow_device"); n != 1 {
		t.Errorf("device rows after quiescence = %d, want 1 (the write committed)", n)
	}
	if err := st.StoreMessage(ctx, msg(alice, "post-quiesce", alice, at(0))); !errors.Is(err, ErrClosed) {
		t.Errorf("an application write after quiescence: %v, want ErrClosed", err)
	}

	// The close finishes after quiescence with a live context.
	if err := st.Close(ctx); err != nil {
		t.Fatalf("the close after quiescence: %v", err)
	}
	if !st.closed {
		t.Error("closed was not set by the close after quiescence")
	}
	if n := len(recs.events("store.close")); n != 1 {
		t.Errorf("store.close events = %d, want exactly one", n)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Errorf("retiring the idle generation after the close: %v", err)
	}
}

// TestQuiesceReportsStillActive holds a device transaction whose callback ignores cancellation and quiesces
// with a short context: the outcome is explicitly not-quiescent, the pools stay open, and the completion signal
// closes only when the call has actually returned.
func TestQuiesceReportsStillActive(t *testing.T) {
	shortWaits(t, 100*time.Millisecond, 200*time.Millisecond, 100*time.Millisecond)
	st, _, dev, barrier := openLoadedStore(t)
	ctx := context.Background()

	releaseTxn, entered, txnDone := holdDeviceTransaction(t, dev)
	<-entered

	quiesceCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := st.Quiesce(quiesceCtx); !errors.Is(err, ErrNotQuiescent) {
		t.Fatalf("Quiesce while a device transaction is held: %v, want ErrNotQuiescent", err)
	}
	if st.closed {
		t.Fatal("Quiesce marked the store closed while work was active")
	}
	// The pools are open while the transaction keeps its slot.
	if n := countRows(t, st, "whatsmeow_sessions"); n != 0 {
		t.Errorf("sessions while quiescing = %d, want 0", n)
	}

	// The held callback keeps its slot regardless of when cancellation reaches its context.
	releaseTxn()
	select {
	case <-txnDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the held device transaction never returned")
	}
	eventually(t, "the completion signal after the drain", func() bool {
		select {
		case <-st.Quiesced():
			return true
		default:
			return false
		}
	})
	if err := st.Close(context.Background()); err != nil {
		t.Fatalf("the close after the drain: %v", err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Errorf("retiring the idle generation after the close: %v", err)
	}
}

// TestGateQuiesceOutcome quiesces the admission gate directly: a bounded attempt while slots are held reports
// not-quiescent, the drain closes the completion signal exactly once, and only quiesced admission refuses new
// callers.
func TestGateQuiesceOutcome(t *testing.T) {
	shortWaits(t, 100*time.Millisecond, 200*time.Millisecond, 100*time.Millisecond)
	g := newGate()
	rel := fill(t, g, 2)
	quiesceCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := g.quiesce(quiesceCtx); !errors.Is(err, ErrNotQuiescent) {
		t.Fatalf("quiesce with held slots: %v, want ErrNotQuiescent", err)
	}
	if _, _, err := g.enter(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("enter while closing: %v, want ErrClosed", err)
	}

	for _, r := range rel {
		r()
	}
	eventually(t, "the completion signal", func() bool { return g.isQuiesced() })
	if err := g.quiesce(context.Background()); err != nil {
		t.Fatalf("quiesce after the drain: %v, want nil", err)
	}
	if err := g.quiesce(context.Background()); err != nil {
		t.Fatalf("a repeated quiesce after completion: %v, want nil", err)
	}
	select {
	case <-g.quiesced:
	default:
		t.Fatal("the completion signal is not closed after quiescence")
	}
}

// TestRepeatedCloseAttemptsShareOneWaiter issues repeated close attempts while a raw-device call is active:
// every bounded attempt reports not-quiescent, no attempt closes the pools or marks success, the attempts share
// one completion waiter, and the final close after the drain succeeds.
func TestRepeatedCloseAttemptsShareOneWaiter(t *testing.T) {
	shortWaits(t, 100*time.Millisecond, 2*time.Second, 1*time.Second)
	st, recs, dev, barrier := openLoadedStore(t)
	ctx := context.Background()

	releaseSave, entered, saveDone := holdDeviceSave(t, st, dev)
	<-entered

	quiescedSignal := st.Quiesced()
	const attempts = 8
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attempt, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			defer cancel()
			results <- st.Close(attempt)
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, ErrNotQuiescent) {
			t.Errorf("a repeated close while work is active: %v, want ErrNotQuiescent", err)
		}
	}
	if st.closed {
		t.Fatal("a bounded close attempt marked the store closed")
	}
	if n := len(recs.events("store.close")); n != 0 {
		t.Errorf("the bounded close attempts logged %d store.close events, want none", n)
	}
	if st.Quiesced() != quiescedSignal {
		t.Error("close attempts did not share the same completion signal")
	}

	// The raw-device call keeps its slot; releasing it ends the drain.
	releaseSave()
	select {
	case <-saveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the held raw-device save never returned")
	}
	eventually(t, "the admitted work to drain", func() bool { return st.gate.activeCount() == 0 })
	eventually(t, "the completion signal after the drain", func() bool {
		select {
		case <-quiescedSignal:
			return true
		default:
			return false
		}
	})

	// The final close completes with a live context.
	if err := st.Close(ctx); err != nil {
		t.Fatalf("the final close: %v", err)
	}
	if !st.closed {
		t.Error("closed was not set by the final close")
	}
	if n := len(recs.events("store.close")); n != 1 {
		t.Errorf("store.close events after the final close = %d, want exactly one", n)
	}
	if err := st.Close(ctx); err != nil {
		t.Errorf("a repeated close after completion: %v, want nil", err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Errorf("retiring the idle generation after the close: %v", err)
	}
}
