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
)

// syncBuffer is a bytes.Buffer that is safe for concurrent writers and readers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
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
	tl := &testLifecycle{signals: make(chan os.Signal, 2), logs: &syncBuffer{}, exits: make(chan int, 1)}
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
	tl.addStop("slow", func(ctx context.Context) error { time.Sleep(time.Second); return nil })
	tl.requestStop("test")
	tl.waitStopping(t)
	start := time.Now()
	if code := tl.shutdown(); code != exitError {
		t.Errorf("exit code = %d, want 1", code)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("shutdown took %v, past its deadline", elapsed)
	}
	tl.expectLog(t, `msg="shutdown timed out" waiting_for=slow`)
}

func TestSecondSignalForcesExit(t *testing.T) {
	tl := newTestLifecycle(t, 5*time.Second)
	blocking := make(chan struct{})
	tl.addStop("stuck", func(ctx context.Context) error {
		close(blocking)
		<-ctx.Done()
		return ctx.Err()
	})
	tl.signals <- os.Interrupt
	tl.waitStopping(t)
	go tl.shutdown()
	<-blocking
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
