package main

import (
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

// countingReader records every read so tests can prove that pair never consumes operator input
// outside the explicit offline-recovery branch.
type countingReader struct {
	data  string
	reads atomic.Int64
	off   int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads.Add(1)
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

// startPairWithInput is startPair with a caller-supplied stdin.
func startPairWithInput(t *testing.T, so *scriptedOwner, signals chan os.Signal, stdin io.Reader, args ...string) *pairRun {
	t.Helper()
	pr := &pairRun{done: make(chan int, 1)}
	withScriptedOwner(t, so)
	if signals != nil {
		withTestLifecycleSignals(t, signals)
	}
	go func() {
		pr.done <- run(append([]string{"pair"}, args...), stdin, &pr.stdout, &pr.stderr, nil)
	}()
	return pr
}

// An unresolved final timeout ends the command. Enter on stdin is not an approval to retry, to
// start offline recovery or to open another window.
func TestPairEnterAfterUnresolvedFinalTimeoutAuthorizesNothing(t *testing.T) {
	so := newScriptedOwner()
	so.request = func(int64) (wa.PairingState, error) {
		so.setWindow(wa.PairingState{Result: wa.PairingTimedOut}, true)
		return so.window, nil
	}
	in := &countingReader{data: "\n\n"}
	pr := startPairWithInput(t, so, nil, in, "--data-dir", privateDir(t))
	if code := pr.waitExit(t, 10*time.Second); code != exitError {
		t.Fatalf("exit code = %d, want 1\nstderr:\n%s", code, pr.diagnostics())
	}
	if n := so.requestN.Load(); n != 1 {
		t.Errorf("RequestPairing called %d times; Enter must not authorize another attempt", n)
	}
	if in.reads.Load() != 0 {
		t.Errorf("pair read stdin %d times after a final timeout", in.reads.Load())
	}
	diag := pr.diagnostics()
	if !strings.Contains(diag, "Offline recovery is required") {
		t.Errorf("no recovery diagnostic:\n%s", diag)
	}
	if strings.Contains(diag+pr.output(), "Offline recovery completed") {
		t.Error("recovery ran without the explicit unsafe-marker branch")
	}
}

// Closed input (EOF) and cancellation while a window is active exit unsuccessfully and never
// start another attempt.
func TestPairEOFAndCancellationExitWithoutAnotherAttempt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin io.Reader
	}{
		{"eof", strings.NewReader("")},
		{"closed_pipe", func() io.Reader { r, w, _ := os.Pipe(); w.Close(); t.Cleanup(func() { r.Close() }); return r }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			so := newScriptedOwner()
			so.request = func(int64) (wa.PairingState, error) {
				so.setWindow(wa.PairingState{Result: wa.PairingActive}, true)
				return so.window, nil
			}
			signals := make(chan os.Signal, 2)
			pr := startPairWithInput(t, so, signals, tc.stdin, "--data-dir", privateDir(t))
			deadline := time.Now().Add(5 * time.Second)
			for so.requestN.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			// Input being at EOF must not end or restart the window by itself.
			select {
			case code := <-pr.done:
				t.Fatalf("pair exited on EOF alone with %d", code)
			case <-time.After(50 * time.Millisecond):
			}
			signals <- os.Interrupt
			if code := pr.waitExit(t, 10*time.Second); code != exitError {
				t.Fatalf("exit code = %d, want 1\nstderr:\n%s", code, pr.diagnostics())
			}
			if n := so.requestN.Load(); n != 1 {
				t.Errorf("RequestPairing called %d times", n)
			}
			if so.beginN.Load() == 0 {
				t.Error("the owner was not stopped")
			}
		})
	}
}

// Unsupported and invalid QR outcomes fail the command and never print the code, whatever the
// owner's snapshot still carries.
func TestPairInvalidOrUnsupportedQRWritesNoCode(t *testing.T) {
	const secret = "2@SYNTHETIC-SECRET,ABCD1234==,leftover"
	for _, result := range []wa.PairingResult{wa.PairingFailed, wa.PairingCancelled} {
		t.Run(string(result), func(t *testing.T) {
			so := newScriptedOwner()
			so.request = func(int64) (wa.PairingState, error) {
				so.setWindow(wa.PairingState{Code: secret, Result: result}, true)
				return so.window, nil
			}
			pr := startPairWithInput(t, so, nil, strings.NewReader(""), "--data-dir", privateDir(t))
			if code := pr.waitExit(t, 10*time.Second); code != exitError {
				t.Fatalf("exit code = %d\nstderr:\n%s", code, pr.diagnostics())
			}
			all := pr.output() + pr.diagnostics()
			if strings.Contains(all, secret) || strings.ContainsAny(pr.output(), "█▀▄") {
				t.Errorf("a terminal window leaked or rendered its code:\n%s", all)
			}
		})
	}
}

// A request that the owner refuses (for example because an unresolved attempt gates it) is not
// retried by the command.
func TestPairRefusedRequestIsNotRetried(t *testing.T) {
	so := newScriptedOwner()
	so.request = func(int64) (wa.PairingState, error) {
		return wa.PairingState{}, wa.ErrStopping
	}
	in := &countingReader{data: "\n"}
	pr := startPairWithInput(t, so, nil, in, "--data-dir", privateDir(t))
	if code := pr.waitExit(t, 10*time.Second); code != exitError {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, pr.diagnostics())
	}
	if so.requestN.Load() != 1 || in.reads.Load() != 0 {
		t.Errorf("requests = %d, stdin reads = %d; want 1 and 0", so.requestN.Load(), in.reads.Load())
	}
}
