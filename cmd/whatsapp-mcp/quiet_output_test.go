package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

var quietReasons = []string{"work_running", "network_refused", "storage_refused", "call_refused", "event_dropped",
	"stop_bound_exceeded", "observation_incomplete", "observation_error"}

// A failed check prints exactly one human line that names the category and says offline recovery is required.
func TestShellStopNamesTheFailedCategoryOnce(t *testing.T) {
	for _, kind := range []string{"stdio", "serve"} {
		for _, reason := range quietReasons {
			t.Run(kind+"/"+reason, func(t *testing.T) {
				so := newScriptedOwner()
				so.health = wa.Health{QuietReason: reason}
				var out, errb bytes.Buffer
				reportShellStop(kind, so, store.ErrUnsafeRun, &streams{stdout: &out, stderr: &errb})
				lines := strings.Split(strings.TrimSpace(errb.String()), "\n")
				if len(lines) != 1 {
					t.Fatalf("lines = %q, want exactly one", errb.String())
				}
				for _, want := range []string{kind, reason, "offline recovery required"} {
					if !strings.Contains(lines[0], want) {
						t.Errorf("line %q lacks %q", lines[0], want)
					}
				}
				if out.Len() != 0 {
					t.Errorf("stdout carries %q; in stdio mode it is protocol-only", out.String())
				}
			})
		}
	}
}

func TestStdioDiagnosticQuietStopFailure(t *testing.T) {
	old := newOwner
	t.Cleanup(func() { newOwner = old })
	const badReason = "synthetic-secret\x1b\nreason=work_running"
	for _, reason := range append(append([]string(nil), quietReasons...), badReason) {
		name, wantReason := reason, reason
		if reason == badReason {
			name, wantReason = "redacted", "unknown"
		}
		t.Run(name, func(t *testing.T) {
			owner := newHeldOwner()
			owner.health = wa.Health{QuietReason: reason}
			owner.stopErr = store.ErrUnsafeRun
			newOwner = func(deps ownerDeps) (*ownerHandle, error) {
				return &ownerHandle{owner: &diagnosticChildOwner{heldOwner: owner, stop: func() {
					deps.log.Error("synthetic-secret", "event", "quiet_stop_failed", "epoch", uint64(2),
						"reason", reason, "library_goroutines", 3, "network_refused", uint64(4),
						"storage_refused", uint64(5), "calls_refused", uint64(6), "events_dropped", uint64(7),
						"dropped_messages", ^uint64(0), "duration_ms", int64(8), "body", "synthetic-secret",
						"network_refused", uint64(99))
					deps.log.Error("recovery required", "event", "run_guard_recovery_required")
					deps.log.Warn("history loss", "reason", "queue_full", "event", "history_incomplete")
					deps.log.Warn("receive loss", "event", "receive_overload", "reason", "item_limit")
					deps.log.Warn("messages lost", "event", "messages_dropped", "epoch", uint64(2), "dropped_messages", uint64(7))
					owner.complete()
				}}}, nil
			}
			var stdout bytes.Buffer
			var stderr syncBuffer
			if code := run([]string{"stdio", "--data-dir", privateDir(t), "--log-level", "error"}, strings.NewReader(""), &stdout, &stderr, nil); code != exitError {
				t.Fatalf("stdio exit = %d, want %d", code, exitError)
			}
			log := stderr.String()
			if stdout.Len() != 0 || strings.Contains(log, "synthetic-secret") || strings.Contains(log, "\x1b") {
				t.Errorf("unexpected stdout or unredacted diagnostic: stdout=%q stderr=%q", stdout.String(), log)
			}
			var quiet, human string
			for line := range strings.SplitSeq(log, "\n") {
				if len(line)+1 > 1024 {
					t.Errorf("diagnostic exceeded the record limit: %d bytes", len(line)+1)
				}
				if strings.Contains(line, "event=quiet_stop_failed ") {
					quiet = line
				}
				if strings.Contains(line, "event=human_failure ") {
					human = line
				}
			}
			for _, field := range []string{"reason=" + wantReason, "epoch=2", "library_goroutines=3", "network_refused=4", "storage_refused=5", "calls_refused=6", "events_dropped=7", "dropped_messages=18446744073709551615", "duration_ms=8"} {
				if !strings.Contains(" "+quiet+" ", " "+field+" ") {
					t.Errorf("quiet-stop record lacks %q: %q", field, quiet)
				}
			}
			if strings.Count(quiet, " network_refused=") != 1 {
				t.Errorf("quiet-stop record repeated a count: %q", quiet)
			}
			if !strings.Contains(human, "offline recovery required ("+wantReason+")") || strings.Contains(human, "library_goroutines=") {
				t.Errorf("human record does not name just the quiet-stop category: %q", human)
			}
			for _, want := range []string{"event=run_guard_recovery_required ", "event=history_incomplete outcome=failed reason=queue_full ",
				"event=receive_overload outcome=failed reason=item_limit ", "event=messages_dropped outcome=failed epoch=2 dropped_messages=7 "} {
				if !strings.Contains(log, want) {
					t.Errorf("stderr lacks %q: %q", want, log)
				}
			}
		})
	}
}

func TestShellStopSaysTheSessionCanBeReusedAfterAPass(t *testing.T) {
	so := newScriptedOwner()
	so.health = wa.Health{QuietPassed: true}
	var out, errb bytes.Buffer
	reportShellStop("stdio", so, nil, &streams{stdout: &out, stderr: &errb})
	if !strings.Contains(errb.String(), "can be reused") || out.Len() != 0 {
		t.Errorf("stderr = %q, stdout = %q", errb.String(), out.String())
	}
}

func TestShellStopSaysNothingWithoutACheck(t *testing.T) {
	so := newScriptedOwner()
	var errb bytes.Buffer
	reportShellStop("serve", so, nil, &streams{stderr: &errb})
	reportShellStop("serve", so, store.ErrUnsafeRun, &streams{stderr: &errb})
	if errb.Len() != 0 {
		t.Errorf("stderr = %q; with no check and no category there is nothing to add", errb.String())
	}
}

// pair, saved session, passed check: the new line, exit 0.
func TestPairSavedSessionPassedCheckSaysItCanBeReused(t *testing.T) {
	so := newScriptedOwner()
	so.health = wa.Health{QuietPassed: true}
	so.startHook = func() {
		so.setPhase(wa.PhaseConnecting, wa.Status{LoggedIn: true, WAVersion: "2.3000.101"})
	}
	pr := startPair(t, so, nil, nil, "--data-dir", privateDir(t))
	deadline := time.Now().Add(5 * time.Second)
	for !so.started.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true, WAVersion: "2.3000.101"})
	if code := pr.waitExit(t, 10*time.Second); code != exitOK {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, pr.diagnostics())
	}
	if !strings.Contains(pr.diagnostics(), "whatsapp-mcp: pair: connected. The saved session can be reused.") {
		t.Errorf("stderr lacks the reuse line:\n%s", pr.diagnostics())
	}
}

// pair, fresh link, failed check: still exit 0 with the recovery notice plus the category.
func TestPairLinkedFailedCheckNamesTheCategory(t *testing.T) {
	for _, reason := range quietReasons {
		t.Run(reason, func(t *testing.T) {
			so := newScriptedOwner()
			so.health = wa.Health{QuietReason: reason}
			so.stopErr = store.ErrUnsafeRun
			so.request = func(int64) (wa.PairingState, error) {
				so.setWindow(wa.PairingState{Result: wa.PairingSucceeded}, true)
				so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true, WAVersion: "2.3000.101"})
				return so.window, nil
			}
			pr := startPair(t, so, nil, nil, "--data-dir", privateDir(t))
			if code := pr.waitExit(t, 10*time.Second); code != exitOK {
				t.Fatalf("exit code = %d\nstderr:\n%s", code, pr.diagnostics())
			}
			for _, want := range []string{"linked. Offline recovery is required before the next run", reason} {
				if !strings.Contains(pr.diagnostics(), want) {
					t.Errorf("stderr lacks %q:\n%s", want, pr.diagnostics())
				}
			}
		})
	}
}

// pair, fresh link, passed check: the linked line.
func TestPairLinkedPassedCheckSaysLinked(t *testing.T) {
	so := newScriptedOwner()
	so.health = wa.Health{QuietPassed: true}
	so.request = func(int64) (wa.PairingState, error) {
		so.setWindow(wa.PairingState{Result: wa.PairingSucceeded}, true)
		so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true, WAVersion: "2.3000.101"})
		return so.window, nil
	}
	pr := startPair(t, so, nil, nil, "--data-dir", privateDir(t))
	if code := pr.waitExit(t, 10*time.Second); code != exitOK {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, pr.diagnostics())
	}
	if !strings.Contains(pr.diagnostics(), "whatsapp-mcp: pair: linked. The saved session can be reused.") {
		t.Errorf("stderr lacks the linked line:\n%s", pr.diagnostics())
	}
}

// The pairing generation failed its check: the incomplete-link line carries the category.
func TestPairDeniedFollowUpNamesTheCategory(t *testing.T) {
	so := newScriptedOwner()
	so.status = wa.Status{LoggedIn: true}
	so.health = wa.Health{QuietReason: "work_running"}
	events := make(chan wa.Event, 1)
	events <- wa.Event{Phase: wa.PhaseDisconnected}
	var errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if waitSavedConnection(so, events, &streams{stderr: &errb}, ctx, true) {
		t.Fatal("a failed handover reported success")
	}
	if !strings.Contains(errb.String(), "could not be completed safely") || !strings.Contains(errb.String(), "(work_running)") {
		t.Errorf("stderr = %q", errb.String())
	}
}
