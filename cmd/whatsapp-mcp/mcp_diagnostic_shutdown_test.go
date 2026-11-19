package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

type brokenDiagnosticSink struct{}

func (brokenDiagnosticSink) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestStdioDiagnosticRefusal(t *testing.T) {
	args := []string{"stdio", "--data-dir", privateDir(t), "--log-level", "synthetic-secret\x1b"}
	var stdout bytes.Buffer
	var stderr syncBuffer
	if got := run(args, strings.NewReader(""), &stdout, &stderr, nil); got != exitUsage {
		t.Fatalf("healthy stderr exit = %d, want %d", got, exitUsage)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "synthetic-secret") || len(stderr.String()) > 1024 {
		t.Fatalf("unbounded or unredacted refusal: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	for _, want := range []string{"log-level", "debug", "info", "warn", "error"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("refusal missing %q: %q", want, stderr.String())
		}
	}
	if got := run(args, strings.NewReader(""), &stdout, brokenDiagnosticSink{}, nil); got != exitError {
		t.Fatalf("failed mandatory refusal exit = %d, want %d", got, exitError)
	}
}

func TestStdioDiagnosticOwnerFailure(t *testing.T) {
	old := newOwner
	defer func() { newOwner = old }()
	newOwner = func(ownerDeps) (*ownerHandle, error) { return nil, errors.New("synthetic-owner-secret") }
	var stdout bytes.Buffer
	var stderr syncBuffer
	if got := run([]string{"stdio", "--data-dir", privateDir(t)}, strings.NewReader(""), &stdout, &stderr, nil); got != exitError {
		t.Fatalf("exit = %d", got)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "synthetic-owner-secret") || !strings.Contains(stderr.String(), "startup_failure") {
		t.Fatalf("unexpected diagnostics: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

type diagnosticHoldSink struct {
	entered, release chan struct{}
	once             sync.Once
	text             syncBuffer
}

func (w *diagnosticHoldSink) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "event=history_incomplete ") {
		w.once.Do(func() { close(w.entered) })
		<-w.release
	}
	return w.text.Write(p)
}

func TestStdioDiagnosticAcknowledgmentAfterCleanup(t *testing.T) {
	oldOwner, oldLifecycle := newOwner, lifecycleFactory
	defer func() { newOwner, lifecycleFactory = oldOwner, oldLifecycle }()
	finalizing := make(chan struct{})
	lifecycleFactory = func(logger *slog.Logger) *lifecycle {
		lc := newLifecycleWith(logger, nil, func(int) {}, shutdownTimeout)
		lc.diagnosticFinalizing = func(pending bool) {
			if !pending {
				t.Error("expected a pending acknowledgment after cleanup")
			}
			close(finalizing)
		}
		return lc
	}
	opened := make(chan *store.Store, 1)
	newOwner = func(deps ownerDeps) (*ownerHandle, error) {
		h := newHeldOwner()
		h.startHook = func() {
			opened <- deps.store
			deps.log.Info("config")
			deps.log.Warn("synthetic", "event", "history_incomplete")
		}
		return &ownerHandle{owner: &diagnosticChildOwner{heldOwner: h, stop: h.complete}}, nil
	}
	sink := &diagnosticHoldSink{entered: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(sink.release) })
	defer release()
	dir := privateDir(t)
	var stdout bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"stdio", "--data-dir", dir, "--log-level", "error"}, strings.NewReader(""), &stdout, sink, nil)
	}()
	for _, ch := range []<-chan struct{}{sink.entered, finalizing} {
		select {
		case <-ch:
		case <-time.After(8 * time.Second):
			t.Fatal("command never reached the held diagnostic or finalization")
		}
	}
	select {
	case <-done:
		t.Fatal("command returned before the mandatory write was acknowledged")
	default:
	}
	select {
	case <-(<-opened).Quiesced():
	default:
		t.Fatal("store still working at diagnostic finalization")
	}
	requireReleasedLock(t, dir)
	release()
	select {
	case code := <-done:
		if code != exitOK || stdout.Len() != 0 || strings.Contains(sink.text.String(), "level=INFO") || !strings.Contains(sink.text.String(), "event=history_incomplete") {
			t.Fatalf("exit %d stdout=%q stderr=%q", code, stdout.String(), sink.text.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("command did not return after delivery")
	}
}

type diagnosticChildOwner struct {
	*heldOwner
	stop func()
}

func (o *diagnosticChildOwner) BeginStop() { o.heldOwner.BeginStop(); o.stop() }

func TestStdioDiagnosticDebugLevel(t *testing.T) {
	old := newOwner
	defer func() { newOwner = old }()
	logged := make(chan struct{})
	newOwner = func(deps ownerDeps) (*ownerHandle, error) {
		h := newHeldOwner()
		h.startHook = func() {
			select {
			case <-logged:
			case <-time.After(5 * time.Second):
				t.Error("configured debug logger did not deliver startup diagnostics")
			}
			deps.log.With("event", "history_incomplete").Warn("mandatory")
		}
		return &ownerHandle{owner: &diagnosticChildOwner{heldOwner: h, stop: h.complete}}, nil
	}
	var stdout bytes.Buffer
	var once sync.Once
	var sink syncBuffer
	writer := diagnosticTestWriter(func(p []byte) (int, error) {
		if strings.Contains(string(p), `level=DEBUG msg="config"`) {
			once.Do(func() { close(logged) })
		}
		return sink.Write(p)
	})
	if got := run([]string{"stdio", "--data-dir", privateDir(t), "--log-level", "debug"}, strings.NewReader(""), &stdout, writer, nil); got != exitOK {
		t.Fatalf("exit %d: %s", got, sink.String())
	}
}

type diagnosticTestWriter func([]byte) (int, error)

func (f diagnosticTestWriter) Write(p []byte) (int, error) { return f(p) }

func TestStdioDiagnosticPrelatched(t *testing.T) {
	d := mcp.NewDiagnostics(brokenDiagnosticSink{}, slog.LevelInfo)
	d.Report(mcp.DiagnosticHistoryIncomplete)
	select {
	case <-d.Wake():
	case <-time.After(5 * time.Second):
		t.Fatal("reporter did not latch")
	}
	cmd := commands()[0]
	var output bytes.Buffer
	s := &streams{stdin: strings.NewReader(""), stdout: &output, stderr: brokenDiagnosticSink{}, diagnostics: d}
	code := runShell("stdio", cmd, []string{"--data-dir", privateDir(t)}, s, func(_ context.Context, _ *lifecycle, _ *slog.Logger, _ *store.Store, _ *streams, _ *wa.LIDCursor) (*ownerHandle, bool) {
		t.Error("owner body started after diagnostic failure")
		return nil, true
	})
	if code != exitError || output.Len() != 0 {
		t.Fatalf("exit %d stdout=%q", code, &output)
	}
}

func TestStdioDiagnosticFailureWinsShutdown(t *testing.T) {
	for _, running := range []bool{true, false} {
		name := "owner_drain"
		if running {
			name = "in_operation"
		}
		t.Run(name, func(t *testing.T) {
			old := newOwner
			defer func() { newOwner = old }()
			owner := newHeldOwner()
			finish := sync.OnceFunc(owner.complete)
			defer finish()
			ready := make(chan ownerDeps, 1)
			retiring := make(chan struct{})
			newOwner = func(deps ownerDeps) (*ownerHandle, error) {
				owner.startHook = func() { ready <- deps }
				return &ownerHandle{owner: &diagnosticChildOwner{heldOwner: owner, stop: func() { close(retiring) }}}, nil
			}
			var stdout bytes.Buffer
			stdin, closeInput := io.Pipe()
			defer stdin.Close()
			defer closeInput.Close()
			dir := privateDir(t)
			exited := make(chan int, 1)
			go func() {
				exited <- run([]string{"stdio", "--data-dir", dir, "--log-level", "error"}, stdin, &stdout, brokenDiagnosticSink{}, nil)
			}()
			var deps ownerDeps
			select {
			case deps = <-ready:
			case <-time.After(8 * time.Second):
				t.Fatal("owner never started")
			}
			fail := func() { deps.log.Warn("synthetic", "event", "history_incomplete") }
			if running {
				fail()
			} else {
				closeInput.Close()
			}
			select {
			case <-retiring:
			case <-time.After(8 * time.Second):
				t.Fatal("command did not cancel and stop its owner")
			}
			if !running {
				fail()
			}
			select {
			case <-deps.store.Quiesced():
				t.Error("store drained before owner was released")
			default:
			}
			if l, err := lock.Acquire(dir); err == nil {
				l.Release()
				t.Error("lock released before owner")
			}
			finish()
			select {
			case code := <-exited:
				if code != exitError || stdout.Len() != 0 {
					t.Errorf("exit %d stdout=%q", code, &stdout)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("command did not exit after owner release")
			}
			requireReleasedLock(t, dir)
		})
	}
}

func TestStdioDiagnosticOrdinaryFailureAndDrop(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		name := "failed_sink"
		if blocked {
			name = "full_queue"
		}
		t.Run(name, func(t *testing.T) {
			old := newOwner
			defer func() { newOwner = old }()
			entered := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			var once sync.Once
			writer := diagnosticTestWriter(func(p []byte) (int, error) {
				once.Do(func() { close(entered) })
				if blocked {
					<-release
					return len(p), nil
				}
				return 0, io.ErrClosedPipe
			})
			newOwner = func(deps ownerDeps) (*ownerHandle, error) {
				h := newHeldOwner()
				h.startHook = func() {
					deps.log.Info("config")
					<-entered
					for i := 0; i < 32; i++ {
						deps.log.Info("config")
					}
				}
				return &ownerHandle{owner: &diagnosticChildOwner{heldOwner: h, stop: h.complete}}, nil
			}
			var stdout bytes.Buffer
			dir := privateDir(t)
			if code := run([]string{"stdio", "--data-dir", dir}, strings.NewReader(""), &stdout, writer, nil); code != exitOK || stdout.Len() != 0 {
				t.Fatalf("ordinary-only exit=%d stdout=%q", code, stdout.String())
			}
			requireReleasedLock(t, dir)
		})
	}
}

func TestStdioDiagnosticOverflowRetainsMarker(t *testing.T) {
	old := newOwner
	defer func() { newOwner = old }()
	sink := &diagnosticHoldSink{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(sink.release)
	newOwner = func(deps ownerDeps) (*ownerHandle, error) {
		h := newHeldOwner()
		h.startHook = func() {
			if err := deps.store.ArmRunGuard(context.Background()); err != nil {
				t.Errorf("arm marker: %v", err)
			}
			deps.log.Warn("loss", "event", "history_incomplete")
			<-sink.entered
			for i := 0; i < 17; i++ {
				deps.log.Warn("loss", "event", "receive_overload")
			}
		}
		return &ownerHandle{owner: &diagnosticChildOwner{heldOwner: h, stop: h.complete}}, nil
	}
	dir := privateDir(t)
	var stdout bytes.Buffer
	if code := run([]string{"stdio", "--data-dir", dir, "--log-level", "error"}, strings.NewReader(""), &stdout, sink, nil); code != exitError || stdout.Len() != 0 {
		t.Fatalf("overflow exit=%d stdout=%q", code, stdout.String())
	}
	requireReleasedLock(t, dir)
	if got := markerState(t, dir); got != "unsafe" {
		t.Fatalf("diagnostics changed owner marker: %s", got)
	}
}

func TestStdioDiagnosticExpiryStopsRunningCommand(t *testing.T) {
	old := newOwner
	defer func() { newOwner = old }()
	sink := &diagnosticHoldSink{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(sink.release)
	retiring := make(chan struct{})
	owner := newHeldOwner()
	defer owner.complete()
	newOwner = func(deps ownerDeps) (*ownerHandle, error) {
		owner.startHook = func() { deps.log.Warn("loss", "event", "history_incomplete") }
		return &ownerHandle{owner: &diagnosticChildOwner{heldOwner: owner, stop: func() { close(retiring) }}}, nil
	}
	in, input := io.Pipe()
	defer in.Close()
	defer input.Close()
	dir := privateDir(t)
	var stdout bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"stdio", "--data-dir", dir, "--log-level", "error"}, in, &stdout, sink, nil)
	}()
	select {
	case <-sink.entered:
	case <-time.After(8 * time.Second):
		t.Fatal("mandatory write did not start")
	}
	select {
	case <-retiring:
	case <-time.After(8 * time.Second):
		t.Fatal("missing acknowledgment did not stop running owner")
	}
	if l, err := lock.Acquire(dir); err == nil {
		l.Release()
		t.Fatal("lock released while owner still running")
	}
	owner.complete()
	select {
	case code := <-done:
		if code != exitError || stdout.Len() != 0 {
			t.Fatalf("expiry exit=%d stdout=%q", code, stdout.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("blocked diagnostic delayed cleanup")
	}
	requireReleasedLock(t, dir)
}
