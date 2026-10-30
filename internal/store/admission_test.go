package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// holdWriteLock opens a second connection to the file and keeps BEGIN IMMEDIATE open, so every store write waits
// for the lock. The returned function ends the transaction.
func holdWriteLock(t *testing.T, st *Store) (release func()) {
	t.Helper()
	db, err := sql.Open("sqlite", fileDSN(st.path, "_pragma=busy_timeout(100)"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			conn.ExecContext(context.Background(), "ROLLBACK")
			conn.Close()
			db.Close()
		})
	}
	t.Cleanup(release)
	return release
}

func TestAdmissionLimitsOnTheRealStore(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	release := holdWriteLock(t, st)

	var wg sync.WaitGroup
	results := make(chan error, maxActive+maxWaiting)
	for i := 0; i < maxActive+maxWaiting; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- st.StoreMessage(context.Background(), msg(alice, fmt.Sprintf("adm-%d", i), alice, at(i)))
		}()
	}
	eventually(t, "16 active and 64 waiting", func() bool {
		return st.gate.activeCount() == maxActive && st.gate.waitingCount() == maxWaiting
	})
	start := time.Now()
	err := st.StoreMessage(context.Background(), msg(alice, "one-too-many", alice, at(0)))
	if !errors.Is(err, ErrBusy) {
		t.Errorf("the 81st call: %v, want ErrBusy", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("ErrBusy took %v, want it at once", time.Since(start))
	}
	release()
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("a queued call failed: %v", err)
		}
	}
	if n := countRows(t, st, "messages"); n != maxActive+maxWaiting {
		t.Errorf("messages = %d, want %d", n, maxActive+maxWaiting)
	}
}

func TestAdmissionWaitEndsOnTheRealStore(t *testing.T) {
	shortWaits(t, 100*time.Millisecond, closeGrace, closeCancelWait)
	st, _ := openTest(t, newDataDir(t))
	release := holdWriteLock(t, st)
	defer release()
	var wg sync.WaitGroup
	for i := 0; i < maxActive; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st.StoreMessage(context.Background(), msg(alice, fmt.Sprintf("w-%d", i), alice, at(i)))
		}()
	}
	eventually(t, "16 active", func() bool { return st.gate.activeCount() == maxActive })
	start := time.Now()
	err := st.StoreMessage(context.Background(), msg(alice, "late", alice, at(0)))
	if !errors.Is(err, ErrBusy) {
		t.Errorf("after the admission wait: %v, want ErrBusy", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("waited %v, want about 100ms", d)
	}
	release()
	wg.Wait()
}

func TestCallerDeadlineEndsALockWait(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	release := holdWriteLock(t, st)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := st.StoreMessage(ctx, msg(alice, "slow", alice, at(0)))
	if err == nil {
		t.Fatal("the write succeeded while the lock was held")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the call took %v, want about 200ms rather than the 5 s busy timeout", d)
	}
	eventually(t, "the slot to be freed", func() bool { return st.gate.activeCount() == 0 })
}

// TestCancellationFreesTheSlot cancels a caller that is waiting for the write lock. The slot must come back
// within 1 s.
func TestCancellationFreesTheSlot(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	release := holdWriteLock(t, st)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- st.StoreMessage(ctx, msg(alice, "cancelled", alice, at(0))) }()
	eventually(t, "the call to hold a slot", func() bool { return st.gate.activeCount() == 1 })
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a cancelled write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("the cancelled call did not return within 1 s")
	}
	for st.gate.activeCount() != 0 {
		if time.Since(start) > time.Second {
			t.Fatalf("the slot was not freed within 1 s (%d active)", st.gate.activeCount())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCommitCancelledIsIndeterminateAndRetrySafe(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx, cancel := context.WithCancel(context.Background())
	st.hooks.beforeCommit = func(context.Context) { cancel() }
	m := msg(alice, "retry", alice, at(0))
	err := st.StoreMessage(ctx, m)
	st.hooks.beforeCommit = nil
	if !errors.Is(err, ErrIndeterminate) {
		t.Fatalf("a commit cancelled at the commit point: %v, want ErrIndeterminate", err)
	}
	if err := st.StoreMessage(context.Background(), m); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n := countRows(t, st, "messages"); n != 1 {
		t.Errorf("rows = %d after the retry, want 1", n)
	}
}

func TestEveryMethodAfterClose(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, err := range map[string]error{
		"StoreMessages": st.StoreMessages(ctx, []Message{msg(alice, "A", alice, at(0))}),
		"UpdateChats":   st.UpdateChats(ctx, []ChatUpdate{{JID: alice}}),
		"MergeLID":      st.MergeLID(ctx, lidAlice, alice),
		"read":          st.read(ctx, "x", func(context.Context, *sql.Conn) error { return nil }),
	} {
		if !errors.Is(err, ErrClosed) {
			t.Errorf("%s after Close: %v, want ErrClosed", name, err)
		}
	}
	if err := st.Close(ctx); err != nil {
		t.Errorf("second Close: %v, want nil", err)
	}
}

// TestCloseCancelsALongOperation holds one operation until its context ends. Close must give it its grace
// period, cancel it, and finish within about 3 s, and later calls get ErrClosed.
func TestCloseCancelsALongOperation(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	held := make(chan struct{})
	var once sync.Once
	st.hooks.beforeCommit = func(ctx context.Context) {
		once.Do(func() { close(held) })
		<-ctx.Done()
	}
	done := make(chan error, 1)
	go func() { done <- st.StoreMessage(context.Background(), msg(alice, "long", alice, at(0))) }()
	<-held

	start := time.Now()
	if err := st.Close(context.Background()); err != nil {
		t.Errorf("Close: %v", err)
	}
	if d := time.Since(start); d > 3500*time.Millisecond {
		t.Errorf("Close took %v, want at most about 3 s", d)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("the held operation succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held operation never returned")
	}
	if err := st.StoreMessage(context.Background(), msg(alice, "after", alice, at(0))); !errors.Is(err, ErrClosed) {
		t.Errorf("a call after the stop: %v, want ErrClosed", err)
	}
}
