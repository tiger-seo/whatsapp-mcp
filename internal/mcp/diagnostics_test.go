package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type diagnosticWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	err     error
}

func (w *diagnosticWriter) Write(p []byte) (int, error) {
	if w.entered != nil {
		w.once.Do(func() { close(w.entered); <-w.release })
	}
	if w.err != nil {
		return 0, w.err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}
func (w *diagnosticWriter) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.buf.String() }
func awaitDiagnostic(t *testing.T, f func() bool) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for !f() {
		if time.Now().After(until) {
			t.Fatal("diagnostic condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDiagnosticsLevelRouting(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		t.Run(level.String(), func(t *testing.T) {
			w := &diagnosticWriter{}
			d := NewDiagnostics(w, level)
			log := slog.New(d.Handler())
			log.Debug("runtime sample")
			log.Info("ingest stopped")
			log.Warn("ignored untrusted message", "event", "history_incomplete", "secret", "synthetic-secret")
			log.Warn("ignored untrusted message", "event", "receive_overload")
			awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "receive_overload") })
			if level <= slog.LevelInfo {
				awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "ingest stopped") })
			}
			if level <= slog.LevelDebug {
				awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "runtime sample") })
			}
			got := w.String()
			if !strings.Contains(got, "history_incomplete") || strings.Contains(got, "synthetic-secret") {
				t.Fatalf("unsafe or missing diagnostics: %q", got)
			}
			if strings.Count(got, "messages_may_be_missing=true") != 2 {
				t.Fatalf("loss warning missing: %q", got)
			}
			if strings.Contains(got, "runtime sample") != (level <= slog.LevelDebug) {
				t.Errorf("debug filter: %q", got)
			}
			if strings.Contains(got, "ingest stopped") != (level <= slog.LevelInfo) {
				t.Errorf("info filter: %q", got)
			}
			awaitDiagnostic(t, func() bool {
				d.mu.Lock()
				defer d.mu.Unlock()
				return d.cells[DiagnosticHistoryIncomplete].pending == 0 && d.cells[DiagnosticReceiveOverload].pending == 0
			})
			if !d.Finish() {
				t.Fatal("acknowledged records failed")
			}
		})
	}
}

func TestDiagnosticsQueueOverflowAndFinish(t *testing.T) {
	w := &diagnosticWriter{entered: make(chan struct{}), release: make(chan struct{})}
	d := NewDiagnostics(w, slog.LevelError)
	if !d.Report(DiagnosticHistoryIncomplete) {
		t.Fatal("first report refused")
	}
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("pump did not enter writer")
	}
	for i := 0; i < 16; i++ {
		if !d.Report(DiagnosticDenied) {
			t.Fatalf("mandatory slot %d refused", i)
		}
	}
	if d.Report(DiagnosticTerminal) || !d.Failed() {
		t.Fatal("overflow did not latch failure")
	}
	select {
	case <-d.Wake():
	default:
		t.Fatal("failure did not wake command")
	}
	if d.Finish() {
		t.Fatal("finish cleared failure or pending records")
	}
	close(w.release)
}

func TestDiagnosticsSinkFailure(t *testing.T) {
	d := NewDiagnostics(&diagnosticWriter{err: errors.New("synthetic secret")}, slog.LevelInfo)
	if !d.Report(DiagnosticFailed) {
		t.Fatal("report refused before sink failed")
	}
	awaitDiagnostic(t, d.Failed)
	if d.Finish() {
		t.Fatal("sink failure cleared")
	}
}

func TestDiagnosticsAcknowledgmentTimeout(t *testing.T) {
	w := &diagnosticWriter{entered: make(chan struct{}), release: make(chan struct{})}
	d := NewDiagnostics(w, slog.LevelError)
	if !d.Report(DiagnosticTerminal) {
		t.Fatal("report refused")
	}
	<-w.entered
	select {
	case <-d.Wake():
	case <-time.After(7 * time.Second):
		t.Fatal("missing acknowledgment expiry")
	}
	if !d.Failed() || d.Finish() {
		t.Fatal("expired report not latched")
	}
	close(w.release)
}

func TestDiagnosticsBoundEventAttribute(t *testing.T) {
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	slog.New(d.Handler()).With("event", "history_incomplete").Warn("synthetic loss")
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "history_incomplete") })
	awaitDiagnostic(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.cells[DiagnosticHistoryIncomplete].pending == 0
	})
	if !d.Finish() {
		t.Fatal("acknowledged event failed")
	}
}

func TestDiagnosticsPriorityAndOrdinaryBound(t *testing.T) {
	w := &diagnosticWriter{entered: make(chan struct{}), release: make(chan struct{})}
	d := NewDiagnostics(w, slog.LevelInfo)
	log := slog.New(d.Handler())
	log.Info("ingest stopped")
	<-w.entered
	for i := 0; i < 20; i++ {
		log.Info(strings.Repeat("x", 10000))
	}
	if !d.Report(DiagnosticDenied) {
		t.Fatal("mandatory queue shared ordinary capacity")
	}
	close(w.release)
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "denied") })
	got := w.String()
	if !strings.Contains(got, "level=INFO msg=\"ingest stopped\"\ntime=") {
		t.Fatalf("mandatory did not follow current record: %q", got)
	}
	d.mu.Lock()
	drops := d.dropped
	d.mu.Unlock()
	if drops != 4 {
		t.Fatalf("ordinary drops = %d, want 4", drops)
	}
	awaitDiagnostic(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.cells[DiagnosticDenied].pending == 0 })
	if !d.Finish() {
		t.Fatal("acknowledged event failed")
	}
}

func TestDiagnosticsAuditMetadata(t *testing.T) {
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	if !d.ReportAudit(DiagnosticDenied, DiagnosticGetChat, DiagnosticInvalidInput) {
		t.Fatal("audit refused")
	}
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "event=denied") })
	got := w.String()
	if !strings.Contains(got, "tool=get_chat") || !strings.Contains(got, "category=invalid_input") || !strings.Contains(got, "outcome=denied") {
		t.Fatalf("missing fixed audit metadata: %q", got)
	}
	awaitDiagnostic(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.cells[DiagnosticDenied].pending == 0 })
	if !d.Finish() {
		t.Fatal("acknowledged event failed")
	}
}

func TestDiagnosticsGuardReporterMappings(t *testing.T) {
	cases := []struct {
		denial   GuardDenial
		expected string
	}{
		{GuardInvalidParams, "event=denied outcome=denied tool=unknown category=invalid_input"},
		{GuardBusy, "event=denied outcome=denied tool=unknown category=busy"},
		{GuardUnsupported, "event=denied outcome=denied tool=unknown category=unavailable"},
		{GuardSDKToolFailure, "event=failed outcome=failed tool=unknown category=internal"},
		{GuardDenial(255), "event=failed outcome=failed tool=unknown category=unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.expected, func(t *testing.T) {
			w := &diagnosticWriter{}
			d := NewDiagnostics(w, slog.LevelError)
			defer d.Finish()
			reporter, ok := any(d).(GuardReporter)
			if !ok {
				t.Fatal("Diagnostics does not implement GuardReporter")
			}
			if !reporter.GuardDenied(tc.denial) {
				t.Fatal("guard event rejected")
			}
			awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), tc.expected) })
			awaitDiagnostic(t, func() bool {
				d.mu.Lock()
				defer d.mu.Unlock()
				return d.cells[DiagnosticDenied].pending == 0 && d.cells[DiagnosticFailed].pending == 0
			})
		})
	}
}

func TestDiagnosticsGuardReporterOverflow(t *testing.T) {
	w := &diagnosticWriter{entered: make(chan struct{}), release: make(chan struct{})}
	d := NewDiagnostics(w, slog.LevelError)
	defer close(w.release)
	reporter, ok := any(d).(GuardReporter)
	if !ok {
		t.Fatal("Diagnostics does not implement GuardReporter")
	}
	if !reporter.GuardDenied(GuardSDKToolFailure) {
		t.Fatal("first event rejected")
	}
	<-w.entered
	for i := 0; i < 16; i++ {
		if !reporter.GuardDenied(GuardBusy) {
			t.Fatalf("slot %d rejected", i)
		}
	}
	if reporter.GuardDenied(GuardInvalidParams) || !d.Failed() || d.Finish() {
		t.Fatal("overflow did not latch unsuccessful finish")
	}
	select {
	case <-d.Wake():
	default:
		t.Fatal("overflow did not wake owner")
	}
}

func TestDiagnosticsAuditInvalidMetadata(t *testing.T) {
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	defer d.Finish()
	if !d.ReportAudit(DiagnosticFailed, DiagnosticTool(255), DiagnosticCategory(255)) {
		t.Fatal("report rejected")
	}
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "tool=unknown category=unknown") })
}

func TestDiagnosticsAuditKnownTools(t *testing.T) {
	for _, tc := range []struct {
		tool DiagnosticTool
		name string
	}{
		{DiagnosticListChats, "list_chats"},
		{DiagnosticGetChat, "get_chat"},
		{DiagnosticSearchContacts, "search_contacts"},
		{DiagnosticDirectChat, "get_direct_chat_by_contact"},
		{DiagnosticContactChats, "get_contact_chats"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &diagnosticWriter{}
			d := NewDiagnostics(w, slog.LevelError)
			defer d.Finish()
			if !d.ReportAudit(DiagnosticFailed, tc.tool, DiagnosticInternal) {
				t.Fatal("report rejected")
			}
			awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "tool="+tc.name+" category=internal") })
		})
	}
}

func TestDiagnosticsGuardReporterSinkFailure(t *testing.T) {
	d := NewDiagnostics(&diagnosticWriter{err: errors.New("synthetic-secret")}, slog.LevelError)
	reporter := any(d).(GuardReporter)
	if !reporter.GuardDenied(GuardSDKToolFailure) {
		t.Fatal("first report rejected")
	}
	awaitDiagnostic(t, d.Failed)
	if reporter.GuardDenied(GuardBusy) || d.Finish() {
		t.Fatal("failed sink accepted another report or cleared failure")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cells[DiagnosticFailed].pending != 1 || d.cells[DiagnosticDeliveryFailed].count != 1 {
		t.Fatal("failure identity or unacknowledged record lost")
	}
}

func TestDiagnosticsGuardReporterToolIdentity(t *testing.T) {
	type toolReporter interface {
		GuardDeniedForTool(GuardDenial, DiagnosticTool) bool
	}
	for _, tc := range []struct {
		tool DiagnosticTool
		name string
	}{{DiagnosticGetChat, "get_chat"}, {DiagnosticTool(255), "unknown"}} {
		t.Run(tc.name, func(t *testing.T) {
			w := &diagnosticWriter{}
			d := NewDiagnostics(w, slog.LevelError)
			defer d.Finish()
			r, ok := any(d).(toolReporter)
			if !ok {
				t.Fatal("typed guard tool reporter missing")
			}
			if !r.GuardDeniedForTool(GuardSDKToolFailure, tc.tool) {
				t.Fatal("report rejected")
			}
			awaitDiagnostic(t, func() bool {
				return strings.Contains(w.String(), "event=failed outcome=failed tool="+tc.name+" category=internal")
			})
		})
	}
}

type partialDiagnosticWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *partialDiagnosticWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(p) > 3 {
		p = p[:3]
	}
	return w.buf.Write(p)
}
func TestDiagnosticsPartialWriteAcknowledgment(t *testing.T) {
	w := &partialDiagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	if !d.Report(DiagnosticTerminal) {
		t.Fatal("report refused")
	}
	awaitDiagnostic(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.cells[DiagnosticTerminal].pending == 0 })
	if !d.Finish() {
		t.Fatal("complete partial writes not acknowledged")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !strings.HasSuffix(w.buf.String(), "\n") {
		t.Fatal("incomplete output acknowledged")
	}
}

func TestDiagnosticsOrdinarySinkFailureThenMandatory(t *testing.T) {
	d := NewDiagnostics(&diagnosticWriter{err: errors.New("closed")}, slog.LevelInfo)
	slog.New(d.Handler()).Info("ordinary")
	awaitDiagnostic(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.sinkFailed })
	if d.Failed() {
		t.Fatal("ordinary-only failure latched")
	}
	if d.Report(DiagnosticHistoryIncomplete) || !d.Failed() {
		t.Fatal("mandatory submission after sink failure did not latch")
	}
	if d.Finish() {
		t.Fatal("finish cleared failed reporting")
	}
}

func TestDiagnosticsOversizedMandatoryLatches(t *testing.T) {
	d := NewDiagnostics(io.Discard, slog.LevelInfo)
	if d.report(DiagnosticTerminal, strings.Repeat("x", 1024)) || !d.Failed() {
		t.Fatal("oversized mandatory record was accepted")
	}
	if d.Finish() {
		t.Fatal("oversized failure cleared")
	}
}

func TestDiagnosticsOrdinaryStructuredAttributes(t *testing.T) {
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelInfo)
	log := slog.New(d.Handler()).With("secret", "synthetic-secret").WithGroup("worker").With("event", "ingest_stopped")
	log.Info("ingest stopped", "token", "synthetic-token")
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "ingest stopped") })
	got := w.String()
	for _, s := range []string{"group.event=ingest_stopped"} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %s in %q", s, got)
		}
	}
	for _, secret := range []string{"synthetic-secret", "synthetic-token"} {
		if strings.Contains(got, secret) {
			t.Errorf("secret leaked in %q", got)
		}
	}
	if !d.Finish() {
		t.Fatal("ordinary log failed")
	}
}

func TestDiagnosticsOrdinaryPrivateAttributes(t *testing.T) {
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelDebug)
	log := slog.New(d.Handler()).With("query", "private-query", "name", "private-name", "chat", "private-chat")
	log.Debug("message received", "body", "private-body", "message_id", "private-id", "phone", "private-phone", "client_key", "private-key", "event", "message_received")
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "message received") })
	got := w.String()
	if strings.Contains(got, "private-") || !strings.Contains(got, "event=message_received") {
		t.Fatalf("private attribute retained or safe state missing: %q", got)
	}
	d.Finish()
}

func TestDiagnosticsOrdinaryUntrustedText(t *testing.T) {
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelDebug)
	log := slog.New(d.Handler()).WithGroup("synthetic-secret-group").With("component", "synthetic-secret-component")
	log.Info("synthetic-secret-message", "event", "synthetic-secret-event", "state", "synthetic-secret-state", "outcome", "synthetic-secret-outcome", "epoch", 987654321, "synthetic-secret-key", "value", slog.Group("synthetic-secret-nested", "state", "synthetic-secret-nested-value"))
	log.Info("quiet stop passed", "event", "quiet_stop_passed", "epoch", 42, "duration_ms", 7)
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "quiet_stop_passed") })
	got := w.String()
	if strings.Contains(got, "synthetic-secret") || strings.Contains(got, "987654321") {
		t.Fatalf("untrusted text leaked: %q", got)
	}
	if !strings.Contains(got, "quiet stop passed") || !strings.Contains(got, "level=INFO") {
		t.Fatalf("operational identity missing: %q", got)
	}
	d.Finish()
}

func TestDiagnosticsHumanFailure(t *testing.T) {
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	if !d.ReportHuman(HumanLockHeld, 1234) {
		t.Fatal("human refusal not admitted")
	}
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "human_failure") })
	got := w.String()
	if !strings.Contains(got, "reason=lock_held") || !strings.Contains(got, "pid=1234") {
		t.Fatalf("missing lock refusal details: %q", got)
	}
	awaitDiagnostic(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.cells[DiagnosticHumanFailure].pending == 0 })
	if !d.Finish() {
		t.Fatal("human failure not acknowledged")
	}
}

func TestDiagnosticsOrdinarySeverityIdentity(t *testing.T) {
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelDebug)
	log := slog.New(d.Handler())
	log.Debug("runtime sample")
	log.Info("ingest stopped")
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "ingest stopped") })
	got := w.String()
	if !strings.Contains(got, "level=DEBUG") || !strings.Contains(got, "level=INFO") {
		t.Fatalf("severity identities lost: %q", got)
	}
	d.Finish()
}

func TestDiagnosticsTerminalFailureIdentity(t *testing.T) {
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	if !d.ReportTerminal(TerminalDevice) || !d.ReportTerminal(TerminalIngest) {
		t.Fatal("terminal failure refused")
	}
	awaitDiagnostic(t, func() bool { return strings.Count(w.String(), "event=terminal_fault") == 2 })
	got := w.String()
	if !strings.Contains(got, "component=device") || !strings.Contains(got, "component=ingest") {
		t.Fatalf("terminal identities lost: %q", got)
	}
	awaitDiagnostic(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.cells[DiagnosticTerminal].pending == 0 })
	if !d.Finish() {
		t.Fatal("terminal failure not acknowledged")
	}
}

func TestDiagnosticsLateAcknowledgmentLatches(t *testing.T) {
	now := time.Now()
	d := &Diagnostics{wake: make(chan struct{}, 1), current: &diagnosticRecord{event: DiagnosticHistoryIncomplete, deadline: now.Add(-time.Millisecond)}}
	d.cells[DiagnosticHistoryIncomplete].pending = 1
	d.acknowledgeLocked(nil, now)
	if !d.Failed() {
		t.Fatal("late acknowledgment cleared reporting failure")
	}
	if d.cells[DiagnosticHistoryIncomplete].count != 0 || d.cells[DiagnosticDeliveryFailed].count != 1 {
		t.Fatal("delivery failure identity not retained")
	}
}

func TestDiagnosticsBufferedAcknowledgmentAtFinish(t *testing.T) {
	w := &diagnosticWriter{entered: make(chan struct{}), release: make(chan struct{})}
	d := NewDiagnostics(w, slog.LevelError)
	if !d.Report(DiagnosticHistoryIncomplete) {
		t.Fatal("report refused")
	}
	<-w.entered
	d.mu.Lock()
	close(w.release)
	// Hold the coordinator out until the write and its acknowledgment complete.
	awaitDiagnostic(t, func() bool { return len(d.completed) == 1 })
	d.mu.Unlock()
	if !d.Finish() {
		t.Fatal("completed mandatory write was treated as pending")
	}
}

func TestDiagnosticsFinishRejectsLateOrFailedAcknowledgment(t *testing.T) {
	for _, tc := range []struct {
		name string
		ack  diagnosticAck
	}{
		{"late", diagnosticAck{at: time.Now().Add(time.Hour)}},
		{"failed", diagnosticAck{at: time.Now(), err: io.ErrClosedPipe}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Diagnostics{wake: make(chan struct{}, 1), changed: make(chan struct{}, 1), completed: make(chan diagnosticAck, 1), current: &diagnosticRecord{event: DiagnosticHistoryIncomplete, deadline: time.Now().Add(time.Minute)}}
			d.cells[DiagnosticHistoryIncomplete].pending = 1
			d.completed <- tc.ack
			if d.Finish() || !d.Failed() {
				t.Fatal("late or failed write was accepted")
			}
		})
	}
}

func TestDiagnosticsBufferedAcknowledgmentAtExpiry(t *testing.T) {
	for i := 0; i < 100; i++ {
		now := time.Now()
		d := &Diagnostics{wake: make(chan struct{}, 1), changed: make(chan struct{}, 1), completed: make(chan diagnosticAck, 1), current: &diagnosticRecord{event: DiagnosticHistoryIncomplete, deadline: now.Add(-time.Second)}}
		d.cells[DiagnosticHistoryIncomplete].pending = 1
		jobs := make(chan []byte, 1)
		d.completed <- diagnosticAck{at: now.Add(-2 * time.Second)}
		go d.run(jobs)
		awaitDiagnostic(t, func() bool {
			d.mu.Lock()
			defer d.mu.Unlock()
			return d.current == nil || d.failed
		})
		if !d.Finish() {
			t.Fatal("timely buffered acknowledgment was treated as expired")
		}
	}
}

func TestDiagnosticsMandatoryEventIdentities(t *testing.T) {
	events := []string{"ingest_failed", "messages_dropped", "quiet_stop_failed", "owner_drain_failed", "run_guard_recovery_required", "run_guard_clear_failed", "revocation_cleanup_failed", "repair_incomplete", "repair_begin_failed", "repair_clear_rejected", "repair_replay_failed", "lid_merge_failed", "version_refresh_failed", "reconnect_denied", "connect_timeout", "registration_decode_failed", "registration_encode_failed"}
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	log := slog.New(d.Handler())
	for _, event := range events {
		log.Warn("synthetic-secret", "event", event, "reason", "synthetic-secret", "error", errors.New("synthetic-secret"))
		awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "event="+event) })
	}
	got := w.String()
	if strings.Contains(got, "synthetic-secret") {
		t.Fatalf("secret leaked: %s", got)
	}
	awaitDiagnostic(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, cell := range d.cells {
			if cell.pending != 0 {
				return false
			}
		}
		return true
	})
	if !d.Finish() {
		t.Fatal("mandatory records failed")
	}
}

var _ io.Writer = (*diagnosticWriter)(nil)

type countedDiagnosticWriter struct {
	calls     atomic.Int64
	completed atomic.Int64
	w         io.Writer
}

func (w *countedDiagnosticWriter) Write(p []byte) (int, error) {
	w.calls.Add(1)
	n, err := w.w.Write(p)
	w.completed.Add(1)
	return n, err
}

func TestDiagnosticsNoRecursiveLogging(t *testing.T) {
	w := &countedDiagnosticWriter{w: &diagnosticWriter{err: io.ErrClosedPipe}}
	d := NewDiagnostics(w, slog.LevelError)
	if !d.Report(DiagnosticHistoryIncomplete) {
		t.Fatal("initial report rejected")
	}
	awaitDiagnostic(t, d.Failed)
	for i := 0; i < 100; i++ {
		if d.Report(DiagnosticReceiveOverload) {
			t.Fatal("failed sink accepted report")
		}
	}
	if d.Finish() || w.calls.Load() != 1 {
		t.Fatalf("recursive or repeated sink writes: %d", w.calls.Load())
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cells[DiagnosticDeliveryFailed].count != 1 || d.cells[DiagnosticHistoryIncomplete].pending != 1 || d.cells[DiagnosticReceiveOverload].pending != 100 {
		t.Fatal("failed events or latch accounting lost")
	}
}

func TestDiagnosticsFixedCellCeiling(t *testing.T) {
	d := NewDiagnostics(io.Discard, slog.LevelError)
	if n := len(d.cells); n > 128 || n != len(diagnosticNames) {
		t.Fatalf("diagnostic accounting has %d cells", n)
	}
	for _, event := range []DiagnosticEvent{0, 255} {
		if !d.Report(event) {
			t.Fatal("unknown event rejected")
		}
	}
	awaitDiagnostic(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.cells[DiagnosticUnknown].pending == 0 })
	d.mu.Lock()
	count := d.cells[DiagnosticUnknown].count
	d.mu.Unlock()
	if count != 2 || !d.Finish() {
		t.Fatal("unknown events did not use fixed accounting")
	}
}

type blockedPartialDiagnosticWriter struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func (w *blockedPartialDiagnosticWriter) Write(p []byte) (int, error) {
	if w.calls.Add(1) == 1 {
		return 1, nil
	}
	close(w.entered)
	<-w.release
	return len(p), nil
}

func TestDiagnosticsPendingPartialWriteAtFinish(t *testing.T) {
	w := &blockedPartialDiagnosticWriter{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(w.release)
	d := NewDiagnostics(w, slog.LevelError)
	if !d.Report(DiagnosticHistoryIncomplete) {
		t.Fatal("report rejected")
	}
	<-w.entered
	if !d.Report(DiagnosticReceiveOverload) || d.Finish() {
		t.Fatal("pending records were treated as delivered")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cells[DiagnosticHistoryIncomplete].pending != 1 || d.cells[DiagnosticReceiveOverload].pending != 1 || d.cells[DiagnosticDeliveryFailed].count != 1 || len(d.mandatory) != 1 {
		t.Fatal("partial or queued mandatory record lost")
	}
}

func TestDiagnosticsStderrSubprocess(t *testing.T) {
	if mode := os.Getenv("MCP_DIAGNOSTIC_PIPE_CHILD"); mode != "" {
		SetupDiagnosticSignals()
		w := &countedDiagnosticWriter{w: os.Stderr}
		d := NewDiagnostics(w, slog.LevelInfo)
		if mode == "closed" {
			if !d.Report(DiagnosticHistoryIncomplete) {
				t.Fatal("initial mandatory report rejected")
			}
			awaitDiagnostic(t, d.Failed)
			if d.Finish() || w.calls.Load() != 1 {
				t.Fatal("closed stderr failure not retained")
			}
			return
		}
		log := slog.New(d.Handler())
		blocked := false
		for i := int64(0); i < 20000; i++ {
			log.Info("ingest stopped")
			deadline := time.Now().Add(100 * time.Millisecond)
			for w.completed.Load() <= i && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if w.completed.Load() <= i {
				if w.calls.Load() != i+1 {
					t.Fatal("writer did not enter blocked stderr")
				}
				blocked = true
				break
			}
		}
		if !blocked {
			t.Fatal("stderr pipe did not fill")
		}
		calls := w.calls.Load()
		if !d.Report(DiagnosticHistoryIncomplete) || !d.Report(DiagnosticReceiveOverload) {
			t.Fatal("pending mandatory events rejected")
		}
		time.Sleep(100 * time.Millisecond)
		if w.calls.Load() != calls {
			t.Fatal("replacement pump entered blocked stderr")
		}
		if d.Finish() {
			t.Fatal("pending mandatory events not failed on finish")
		}
		d.mu.Lock()
		pending := d.cells[DiagnosticHistoryIncomplete].pending == 1 && d.cells[DiagnosticReceiveOverload].pending == 1
		d.mu.Unlock()
		if !pending || w.calls.Load() != calls {
			t.Fatal("pending loss events discarded or replacement pump started")
		}
		fmt.Fprint(os.Stdout, "cleanup-returned")
		return
	}
	for _, mode := range []string{"blocked", "closed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDiagnosticsStderrSubprocess$")
			cmd.Env = append(os.Environ(), "MCP_DIAGNOSTIC_PIPE_CHILD="+mode)
			var out bytes.Buffer
			cmd.Stdout = &out
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			if mode == "closed" {
				reader.Close()
			}
			cmd.Stderr = writer
			err = cmd.Run()
			if ctx.Err() != nil || err != nil {
				t.Fatalf("child failed or blocked: %v (context %v), stdout %q", err, ctx.Err(), out.String())
			}
			if mode == "blocked" && !strings.Contains(out.String(), "cleanup-returned") {
				t.Fatalf("missing cleanup completion: %q", out.String())
			}
		})
	}
}
