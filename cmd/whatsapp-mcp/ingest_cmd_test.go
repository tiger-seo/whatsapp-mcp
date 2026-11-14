package main

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

// withScriptedShellOwner installs the fake owner factory for serve and stdio.
func withScriptedShellOwner(t *testing.T, so *scriptedOwner) {
	t.Helper()
	prev := newOwner
	t.Cleanup(func() { newOwner = prev })
	newOwner = func(deps ownerDeps) (*ownerHandle, error) {
		if !deps.shell {
			t.Error("a shell built a pair owner")
		}
		return &ownerHandle{owner: so}, nil
	}
}

// failingIngestOwner is a scripted owner whose ingest fails right after Start.
func failingIngestOwner() *scriptedOwner {
	so := newScriptedOwner()
	so.terminal = make(chan struct{})
	var once sync.Once
	so.startHook = func() {
		so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true, WAVersion: "2.3000.101"})
		once.Do(func() {
			so.mu.Lock()
			so.health.IngestFailed = true
			so.mu.Unlock()
			close(so.terminal)
		})
	}
	return so
}

type ingestShellRun struct {
	done   chan int
	stdout syncBuffer
	stderr syncBuffer
}

func startIngestShell(t *testing.T, kind string, stdin io.Reader, dir string) *ingestShellRun {
	t.Helper()
	r := &ingestShellRun{done: make(chan int, 1)}
	go func() { r.done <- run([]string{kind, "--data-dir", dir}, stdin, &r.stdout, &r.stderr, nil) }()
	return r
}

func (r *ingestShellRun) wait(t *testing.T) int {
	t.Helper()
	select {
	case code := <-r.done:
		return code
	case <-time.After(15 * time.Second):
		t.Fatalf("the command did not exit\nstderr:\n%s", r.stderr.String())
		return 0
	}
}

func TestShellIngestFailureStopsTheCommand(t *testing.T) {
	for _, kind := range []string{"serve", "stdio"} {
		t.Run(kind, func(t *testing.T) {
			so := failingIngestOwner()
			withScriptedShellOwner(t, so)
			// stdin stays open, so only the terminal signal can end the command.
			stdin, keep := io.Pipe()
			defer keep.Close()
			r := startIngestShell(t, kind, stdin, privateDir(t))
			if code := r.wait(t); code != exitError {
				t.Fatalf("exit code = %d, want 1\nstderr:\n%s", code, r.stderr.String())
			}
			if so.beginN.Load() == 0 {
				t.Error("the owner was not stopped through the normal stop")
			}
			errText := r.stderr.String()
			if strings.Count(errText, storageFailedMessage+"\n") != 1 {
				t.Errorf("stderr does not carry the storage message exactly once:\n%s", errText)
			}
			if out := r.stdout.String(); out != "" {
				t.Errorf("stdout = %q, want empty", out)
			}
		})
	}
}

func TestShellLifecycleStopWithoutTerminalSignalIsUnchanged(t *testing.T) {
	so := newScriptedOwner()
	so.terminal = make(chan struct{}) // never closed
	withScriptedShellOwner(t, so)
	r := startIngestShell(t, "stdio", strings.NewReader(""), privateDir(t))
	if code := r.wait(t); code != exitOK {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", code, r.stderr.String())
	}
	if strings.Contains(r.stderr.String(), "message storage failed") {
		t.Errorf("a normal stop reported a storage failure:\n%s", r.stderr.String())
	}
	if out := r.stdout.String(); out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
}

// pair reports the link outcome first, then the storage failure, and exits 1.
func TestShellIngestFailureAfterPairLink(t *testing.T) {
	so := failingIngestOwner()
	so.health.QuietPassed = true
	pr := startPair(t, so, nil, nil, "--data-dir", privateDir(t))
	if code := pr.waitExit(t, 15*time.Second); code != exitError {
		t.Fatalf("exit code = %d, want 1\nstderr:\n%s", code, pr.diagnostics())
	}
	errText := pr.diagnostics()
	link := strings.Index(errText, "whatsapp-mcp: pair: connected.")
	storage := strings.Index(errText, storageFailedMessage)
	if link < 0 || storage < 0 || storage < link {
		t.Fatalf("want the link outcome, then the storage message:\n%s", errText)
	}
}
