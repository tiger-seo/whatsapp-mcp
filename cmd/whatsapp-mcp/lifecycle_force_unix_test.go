//go:build !windows

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

type forceOutput struct {
	ready, first         chan struct{}
	readyOnce, firstOnce sync.Once
	log                  syncBuffer
}

func (o *forceOutput) Write(p []byte) (int, error) {
	n, err := o.log.Write(p)
	if strings.Contains(string(p), "synthetic cleanup held") {
		o.readyOnce.Do(func() { close(o.ready) })
	}
	if strings.Contains(string(p), "first stop signal received") {
		o.firstOnce.Do(func() { close(o.first) })
	}
	return n, err
}

// The child intentionally exits through the real lifecycle os.Exit callback while cleanup is held.
func TestForcedProcessExitLeavesUnsafeMarker(t *testing.T) {
	if dir := os.Getenv("WA_TEST_FORCED_SHUTDOWN_DIR"); dir != "" {
		l, err := lock.Acquire(dir)
		if err != nil {
			t.Fatal(err)
		}
		st, err := store.Open(context.Background(), dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.ArmRunGuard(context.Background()); err != nil {
			t.Fatal(err)
		}
		lc := newLifecycle(slog.New(slog.NewTextHandler(os.Stderr, nil)))
		lc.addStop("lock", func(context.Context) error { return l.Release() })
		lc.addStoreStop(st.Close, st.Quiesced())
		lc.addStop("held", func(context.Context) error { fmt.Fprintln(os.Stderr, "synthetic cleanup held"); select {} })
		lc.requestStop("synthetic completion")
		<-lc.ctx.Done()
		os.Exit(lc.shutdown())
	}
	dir := privateDir(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestForcedProcessExitLeavesUnsafeMarker$", "-test.timeout=30s")
	cmd.Env = append(childEnv(), "WA_TEST_FORCED_SHUTDOWN_DIR="+dir)
	out := &forceOutput{ready: make(chan struct{}), first: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = io.Discard, out
	startForSignal(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			<-done
		}
	})
	select {
	case <-out.ready:
	case err := <-done:
		t.Fatalf("child exited before drain: %v: %s", err, out.log.String())
	case <-time.After(15 * time.Second):
		t.Fatalf("child did not start cleanup: %s", out.log.String())
	}
	if other, err := lock.Acquire(dir); err == nil {
		other.Release()
		t.Fatal("child lock not held")
	}
	if err := sendStop(cmd.Process, "interrupt"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-out.first:
	case err := <-done:
		t.Fatalf("first signal exited child: %v: %s", err, out.log.String())
	case <-time.After(5 * time.Second):
		t.Fatal("first signal not observed")
	}
	if err := sendStop(cmd.Process, "terminate"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || cmd.ProcessState.ExitCode() != exitError {
			t.Fatalf("forced exit = %v, state %v", err, cmd.ProcessState)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second signal did not terminate child")
	}
	if !strings.Contains(out.log.String(), "forced exit") {
		t.Fatalf("missing forced exit diagnostic: %s", out.log.String())
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatalf("lock after process exit: %v", err)
	}
	defer l.Release()
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" {
		t.Fatalf("marker=%q err=%v", state, err)
	}
}
