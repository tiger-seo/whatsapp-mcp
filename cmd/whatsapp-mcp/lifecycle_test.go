package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
)

// syncBuffer is a bytes.Buffer that is safe for concurrent writers and readers.
type syncBuffer struct {
	mu          sync.Mutex
	buf         bytes.Buffer
	timedOut    chan struct{}
	timeoutOnce sync.Once
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if b.timedOut != nil && strings.Contains(string(p), "shutdown timed out") {
		b.timeoutOnce.Do(func() { close(b.timedOut) })
	}
	return n, err
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type testLifecycle struct {
	*lifecycle
	signals chan os.Signal
	logs    *syncBuffer
	exits   chan int
}

func newTestLifecycle(t *testing.T, timeout time.Duration) *testLifecycle {
	t.Helper()
	tl := &testLifecycle{signals: make(chan os.Signal, 2), logs: &syncBuffer{timedOut: make(chan struct{})}, exits: make(chan int, 1)}
	logger := slog.New(slog.NewTextHandler(tl.logs, nil))
	tl.lifecycle = newLifecycleWith(logger, tl.signals, func(code int) { tl.exits <- code }, timeout)
	t.Cleanup(tl.close)
	return tl
}

func (tl *testLifecycle) waitStopping(t *testing.T) {
	t.Helper()
	select {
	case <-tl.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the run context was not cancelled")
	}
}

func (tl *testLifecycle) expectLog(t *testing.T, want string) {
	t.Helper()
	if !strings.Contains(tl.logs.String(), want) {
		t.Errorf("log does not contain %q:\n%s", want, tl.logs)
	}
}

func TestStopStepsInReverseOrder(t *testing.T) {
	tl := newTestLifecycle(t, time.Second)
	var order []string
	for _, name := range []string{"first", "second", "third"} {
		tl.addStop(name, func(context.Context) error { order = append(order, name); return nil })
	}
	tl.signals <- os.Interrupt
	tl.waitStopping(t)
	if code := tl.shutdown(); code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
	if got := strings.Join(order, ","); got != "third,second,first" {
		t.Errorf("order = %s", got)
	}
	tl.expectLog(t, `msg="shutting down" reason=interrupt`)
	tl.expectLog(t, "msg=stopped")
}

func TestTerminateReason(t *testing.T) {
	tl := newTestLifecycle(t, time.Second)
	tl.signals <- syscall.SIGTERM
	tl.waitStopping(t)
	tl.shutdown()
	tl.expectLog(t, `msg="shutting down" reason=terminated`)
}

func TestStopStepFails(t *testing.T) {
	tl := newTestLifecycle(t, time.Second)
	ran := false
	tl.addStop("early", func(context.Context) error { ran = true; return nil })
	tl.addStop("broken", func(context.Context) error { return errors.New("disk on fire") })
	tl.requestStop("test")
	tl.waitStopping(t)
	if code := tl.shutdown(); code != exitError {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !ran {
		t.Error("the step after the failing one did not run")
	}
	tl.expectLog(t, `msg="stop failed" step=broken error="disk on fire"`)
}

func TestShutdownTimeout(t *testing.T) {
	tl := newTestLifecycle(t, 50*time.Millisecond)
	entered := make(chan struct{})
	release := make(chan struct{})
	lockReleased := make(chan struct{})
	tl.addStop("lock", func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Errorf("lock received cancelled context: %v", ctx.Err())
		}
		close(lockReleased)
		return nil
	})
	tl.addStop("slow", func(ctx context.Context) error { close(entered); <-release; return ctx.Err() })
	tl.requestStop("test")
	tl.waitStopping(t)
	done := make(chan int, 1)
	go func() { done <- tl.shutdown() }()
	<-entered
	<-tl.logs.timedOut
	select {
	case code := <-done:
		t.Fatalf("shutdown returned %d before drain", code)
	default:
	}
	select {
	case <-lockReleased:
		t.Fatal("lock released before drain")
	default:
	}
	close(release)
	if code := <-done; code != exitError {
		t.Errorf("exit code = %d, want 1", code)
	}
	select {
	case <-lockReleased:
	default:
		t.Error("lock not released after drain")
	}
	tl.expectLog(t, `msg="shutdown timed out" waiting_for=slow`)
}

func TestStoreNotQuiescentWaitsBeforeLock(t *testing.T) {
	tl := newTestLifecycle(t, time.Second)
	drained := make(chan struct{})
	lockReleased := make(chan struct{})
	firstClose := make(chan struct{})
	var calls int
	tl.addStop("lock", func(context.Context) error { close(lockReleased); return nil })
	tl.addStoreStop(func(ctx context.Context) error {
		calls++
		if calls == 1 {
			close(firstClose)
			return store.ErrNotQuiescent
		}
		if ctx.Err() != nil {
			t.Errorf("retry received cancelled context: %v", ctx.Err())
		}
		return nil
	}, drained)
	tl.requestStop("test")
	tl.waitStopping(t)
	done := make(chan int, 1)
	go func() { done <- tl.shutdown() }()
	<-firstClose
	select {
	case <-lockReleased:
		t.Fatal("lock released while store active")
	default:
	}
	close(drained)
	if code := <-done; code != exitError {
		t.Errorf("exit code = %d, want 1", code)
	}
	if calls != 2 {
		t.Errorf("close calls = %d, want 2", calls)
	}
	select {
	case <-lockReleased:
	default:
		t.Error("lock not released after store drained")
	}
}

func TestStoreCloseErrorAfterDrainReleasesLock(t *testing.T) {
	tl := newTestLifecycle(t, time.Second)
	drained := make(chan struct{})
	close(drained)
	released := false
	tl.addStop("lock", func(context.Context) error { released = true; return nil })
	tl.addStoreStop(func(context.Context) error { return errors.New("pool close failed") }, drained)
	tl.requestStop("test")
	tl.waitStopping(t)
	if code := tl.shutdown(); code != exitError || !released {
		t.Errorf("code = %d, released = %v; want failure and lock release", code, released)
	}
}

func TestShutdownWaitsForHeldDeviceTransaction(t *testing.T) {
	dir := privateDir(t)
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev, barrier, err := st.LoadDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := types.NewJID("100000000000", types.DefaultUserServer)
	dev.ID = &id
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("synthetic"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := dev.Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	transaction := make(chan error, 1)
	go func() {
		transaction <- dev.EventBuffer.DoDecryptionTxn(context.Background(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	tl := newTestLifecycle(t, 30*time.Millisecond)
	tl.addStop("lock", func(context.Context) error { return l.Release() })
	tl.addStoreStop(st.Close, st.Quiesced())
	lcOwnerEntered := make(chan struct{})
	tl.addStop("owner", func(ctx context.Context) error { close(lcOwnerEntered); return barrier.Retire(ctx) })
	tl.requestStop("test")
	tl.waitStopping(t)
	done := make(chan int, 1)
	go func() { done <- tl.shutdown() }()
	<-lcOwnerEntered
	<-tl.logs.timedOut
	select {
	case code := <-done:
		t.Fatalf("shutdown completed while device transaction held: %d", code)
	default:
	}
	if other, err := lock.Acquire(dir); err == nil {
		other.Release()
		t.Fatal("lock released while device transaction held")
	}
	close(release)
	if err := <-transaction; err != nil {
		t.Fatalf("held transaction: %v", err)
	}
	if code := <-done; code != exitError {
		t.Errorf("shutdown code = %d, want timeout failure", code)
	}
	other, err := lock.Acquire(dir)
	if err != nil {
		t.Fatalf("lock after drain: %v", err)
	}
	if err := other.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestSecondSignalForcesExit(t *testing.T) {
	tl := newTestLifecycle(t, 30*time.Millisecond)
	blocking := make(chan struct{})
	release := make(chan struct{})
	tl.addStop("stuck", func(ctx context.Context) error {
		close(blocking)
		<-release
		return nil
	})
	tl.signals <- os.Interrupt
	tl.waitStopping(t)
	done := make(chan int, 1)
	go func() { done <- tl.shutdown() }()
	<-blocking
	<-tl.logs.timedOut
	tl.signals <- os.Interrupt
	select {
	case code := <-tl.exits:
		if code != exitError {
			t.Errorf("forced exit code = %d, want 1", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a second signal did not force an exit")
	}
	tl.expectLog(t, `msg="forced exit"`)
	close(release)
	<-done
}

func TestStdinClosedRequest(t *testing.T) {
	tl := newTestLifecycle(t, time.Second)
	tl.requestStop("stdin closed")
	tl.waitStopping(t)
	if code := tl.shutdown(); code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
	tl.expectLog(t, `msg="shutting down" reason="stdin closed"`)
}

func TestStopDuringStartup(t *testing.T) {
	tl := newTestLifecycle(t, time.Second)
	released := false
	tl.addStop("lock", func(context.Context) error { released = true; return nil })
	tl.signals <- os.Interrupt
	tl.waitStopping(t)
	// Startup notices the cancelled context before registering anything else.
	if code := tl.shutdown(); code != exitOK || !released {
		t.Errorf("exit code = %d, released = %v; want 0 and the lock released", code, released)
	}
}
