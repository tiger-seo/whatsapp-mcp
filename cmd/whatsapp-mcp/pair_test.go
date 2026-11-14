package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

func TestPairOwnerConstructionFailureExitsUnsuccessfully(t *testing.T) {
	prev := newOwner
	t.Cleanup(func() { newOwner = prev })
	newOwner = func(ownerDeps) (*ownerHandle, error) {
		return nil, errors.New("synthetic owner construction failure")
	}
	dir := privateDir(t)
	r := runIn(t, nil, "pair", "--data-dir", dir)
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, "synthetic owner construction failure") {
		t.Fatalf("missing diagnostic: %s", r.stderr)
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	defer l.Release()
}

func TestPairConnectionEventsAreNotProofOfSuccess(t *testing.T) {
	for _, status := range []wa.Status{
		{Connected: true},
		{LoggedIn: true},
		{},
	} {
		t.Run(fmt.Sprintf("connected=%t,loggedIn=%t", status.Connected, status.LoggedIn), func(t *testing.T) {
			so := newScriptedOwner()
			so.status = status
			events := make(chan wa.Event, 1)
			events <- wa.Event{Phase: wa.PhaseConnected}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if waitSavedConnection(so, events, &streams{stderr: io.Discard}, ctx, false) {
				t.Fatal("incomplete current status reported success")
			}
		})
	}
}

func TestPairStaleQueuedEventsUseCurrentState(t *testing.T) {
	so := newScriptedOwner()
	so.status = wa.Status{LoggedIn: true, Connected: true}
	events := make(chan wa.Event, 2)
	events <- wa.Event{Phase: wa.PhaseLoggedOut}
	if !waitSavedConnection(so, events, &streams{stderr: io.Discard}, context.Background(), false) {
		t.Fatal("stale logout overrode current connected identity")
	}

	so = newScriptedOwner()
	so.status = wa.Status{LoggedIn: true}
	events = make(chan wa.Event, 2)
	events <- wa.Event{Phase: wa.PhaseDisconnected}
	events <- wa.Event{Phase: wa.PhaseConnecting}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if waitSavedConnection(so, events, &streams{stderr: io.Discard}, ctx, false) {
		t.Fatal("disconnected session reported success")
	}
}

func TestPairTerminalEventSurvivesNotificationFlood(t *testing.T) {
	so := newScriptedOwner()
	so.startHook = func() {
		for i := 0; i < 32; i++ {
			so.setPhase(wa.PhaseConnecting, wa.Status{LoggedIn: true})
		}
		so.setPhase(wa.PhaseDisconnected, wa.Status{LoggedIn: true})
	}
	pr := startPair(t, so, nil, nil, "--data-dir", privateDir(t))
	if code := pr.waitExit(t, 10*time.Second); code != exitError {
		t.Fatalf("exit code = %d, want failure after terminal event", code)
	}
}

func TestPairWindowConnectedEventDoesNotSucceed(t *testing.T) {
	so := newScriptedOwner()
	so.window = wa.PairingState{Result: wa.PairingActive}
	so.hasWindow = true
	events := make(chan wa.Event, 1)
	events <- wa.Event{Phase: wa.PhaseConnected}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if pairingWindowFlow(so, events, &streams{stdout: io.Discard, stderr: io.Discard}, ctx) {
		t.Fatal("connected event without saved identity reported success")
	}
}

// scriptedOwner is a fake wa.Owner whose state transitions the test drives explicitly. It never
// creates a client, a transport or a network connection.
type scriptedOwner struct {
	mu        sync.Mutex
	phase     wa.Phase
	status    wa.Status
	health    wa.Health
	window    wa.PairingState
	hasWindow bool
	// terminal is what Terminal returns; nil never fires.
	terminal chan struct{}

	wake      chan struct{}
	subs      map[int]func(wa.Event)
	subNext   int
	epoch     uint64
	started   atomic.Bool
	beginN    atomic.Int64
	requestN  atomic.Int64
	request   func(n int64) (wa.PairingState, error)
	startHook func()
	stopErr   error
}

func newScriptedOwner() *scriptedOwner {
	return &scriptedOwner{
		wake:   make(chan struct{}),
		subs:   map[int]func(wa.Event){},
		status: wa.Status{WAVersion: "2.3000.101"},
	}
}

func (s *scriptedOwner) Start(context.Context) error {
	s.started.Store(true)
	if s.startHook != nil {
		s.startHook()
	}
	return nil
}

func (s *scriptedOwner) BeginStop() {
	if s.beginN.Add(1) == 1 {
		s.setPhase(wa.PhaseStopping, wa.Status{})
	}
}

var closedDone = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

func (s *scriptedOwner) Done() <-chan struct{} { return closedDone }
func (s *scriptedOwner) StopError() error      { return s.stopErr }

func (s *scriptedOwner) Snapshot() wa.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *scriptedOwner) RequestPairing(context.Context) (wa.PairingState, error) {
	n := s.requestN.Add(1)
	if s.request != nil {
		return s.request(n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.window, nil
}

func (s *scriptedOwner) PairingSnapshot() (wa.PairingState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.window, s.hasWindow
}

func (s *scriptedOwner) CancelPairing() {}

func (s *scriptedOwner) Subscribe(fn func(wa.Event)) func() {
	s.mu.Lock()
	id := s.subNext
	s.subNext++
	s.subs[id] = fn
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

func (s *scriptedOwner) Terminal() <-chan struct{} { return s.terminal }

func (s *scriptedOwner) Health() wa.Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health
}

// setPhase drives a phase change and notifies subscribers, like the real owner does.
func (s *scriptedOwner) setPhase(phase wa.Phase, status wa.Status) {
	s.mu.Lock()
	s.phase = phase
	if status != (wa.Status{}) {
		s.status = status
	}
	epoch := s.epoch
	subs := make([]func(wa.Event), 0, len(s.subs))
	for _, fn := range s.subs {
		subs = append(subs, fn)
	}
	s.mu.Unlock()
	for _, fn := range subs {
		fn(wa.Event{Epoch: epoch, Phase: phase})
	}
}

// setWindow publishes a pairing window state and notifies subscribers.
func (s *scriptedOwner) setWindow(state wa.PairingState, has bool) {
	s.mu.Lock()
	s.window = state
	s.hasWindow = has
	subs := make([]func(wa.Event), 0, len(s.subs))
	for _, fn := range s.subs {
		subs = append(subs, fn)
	}
	s.mu.Unlock()
	for _, fn := range subs {
		fn(wa.Event{Epoch: s.epoch, Phase: wa.PhasePairing})
	}
}

// pairRun drives one pair command with streaming output capture.
type pairRun struct {
	done   chan int
	stdout syncBuffer
	stderr syncBuffer
}

// startPair runs the pair command in a goroutine with the given scripted owner factory and an
// optional signal channel the test can use to simulate an interrupt.
func startPair(t *testing.T, so *scriptedOwner, signals chan os.Signal, environ []string, args ...string) *pairRun {
	t.Helper()
	pr := &pairRun{done: make(chan int, 1)}
	withScriptedOwner(t, so)
	if signals != nil {
		withTestLifecycleSignals(t, signals)
	}
	go func() {
		code := run(append([]string{"pair"}, args...), strings.NewReader(""), &pr.stdout, &pr.stderr, environ)
		pr.done <- code
	}()
	return pr
}

func (pr *pairRun) output() string {
	pr.stdout.mu.Lock()
	defer pr.stdout.mu.Unlock()
	return pr.stdout.buf.String()
}

func (pr *pairRun) diagnostics() string {
	pr.stderr.mu.Lock()
	defer pr.stderr.mu.Unlock()
	return pr.stderr.buf.String()
}

func (pr *pairRun) waitExit(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case code := <-pr.done:
		return code
	case <-time.After(timeout):
		t.Fatalf("pair did not exit within %v\nstdout:\n%s\nstderr:\n%s", timeout, pr.output(), pr.diagnostics())
		return 0
	}
}

// withScriptedOwner installs the fake owner factory for the duration of the test. Tests always
// supply the fake factory; the real owner is never constructed in this file.
func withScriptedOwner(t *testing.T, so *scriptedOwner) {
	t.Helper()
	prev := newOwner
	t.Cleanup(func() { newOwner = prev })
	newOwner = func(deps ownerDeps) (*ownerHandle, error) {
		if deps.shell {
			t.Error("pair built a shell owner")
		}
		return &ownerHandle{owner: so}, nil
	}
}

// TestPairSucceedsOnlyWithDurableIdentityAndConnected checks the saved-session confirmation path:
// pair waits for the connection to confirm, then shuts down cleanly.
func TestPairSucceedsOnlyWithDurableIdentityAndConnected(t *testing.T) {
	so := newScriptedOwner()
	so.startHook = func() {
		so.setPhase(wa.PhaseConnecting, wa.Status{LoggedIn: true, WAVersion: "2.3000.101"})
	}
	dir := privateDir(t)
	pr := startPair(t, so, nil, nil, "--data-dir", dir)
	deadline := time.Now().Add(5 * time.Second)
	for !so.started.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true, WAVersion: "2.3000.101"})
	if code := pr.waitExit(t, 10*time.Second); code != exitOK {
		t.Fatalf("exit code = %d\nstdout:\n%s\nstderr:\n%s", code, pr.output(), pr.diagnostics())
	}
	if out := pr.output(); out != "" {
		t.Errorf("a saved session printed to stdout: %q", out)
	}
	if so.beginN.Load() == 0 {
		t.Error("the owner was not stopped")
	}
	if _, err := os.Stat(dbPath(dir)); err != nil {
		t.Errorf("pair did not open the store: %v", err)
	}
	if _, err := os.Stat(dbPath(dir) + "-wal"); err == nil {
		t.Error("pair left the store open: -wal still exists")
	}
}

func TestPairSavedSessionFailureRetainsUnsuccessfulExit(t *testing.T) {
	so := newScriptedOwner()
	so.startHook = func() {
		so.setPhase(wa.PhaseConnecting, wa.Status{LoggedIn: true, WAVersion: "2.3000.101"})
	}
	dir := privateDir(t)
	pr := startPair(t, so, nil, nil, "--data-dir", dir)
	deadline := time.Now().Add(5 * time.Second)
	for !so.started.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	so.setPhase(wa.PhaseDisconnected, wa.Status{LoggedIn: true, WAVersion: "2.3000.101"})
	if code := pr.waitExit(t, 10*time.Second); code != exitError {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, pr.output(), pr.diagnostics())
	}
	if out := pr.output(); out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(pr.diagnostics(), "whatsapp-mcp: pair:") {
		t.Errorf("no actionable diagnostic:\n%s", pr.diagnostics())
	}
}

// TestPairHolderPIDRefusal checks the lock-holder diagnostic before any owner work.
func TestPairHolderPIDRefusal(t *testing.T) {
	factoryCalls := 0
	prev := newOwner
	t.Cleanup(func() { newOwner = prev })
	newOwner = func(deps ownerDeps) (*ownerHandle, error) {
		factoryCalls++
		return nil, errors.New("unexpected owner construction")
	}
	dir := privateDir(t)
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	r := runIn(t, nil, "pair", "--data-dir", dir)
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, "another whatsapp-mcp (PID") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if factoryCalls != 0 {
		t.Error("the owner factory ran although the lock was held")
	}
}

// TestPairRendersCurrentCodeWithoutPayloadLogs drives the pairing window: the current code is
// rendered once, the payload never reaches stderr or logs, and success needs the connection.
func TestPairRendersCurrentCodeWithoutPayloadLogs(t *testing.T) {
	const code = "2@SYNTHETIC-PAYLOAD,ABCD1234==,first"
	so := newScriptedOwner()
	var rendered atomic.Bool
	so.request = func(n int64) (wa.PairingState, error) {
		so.setWindow(wa.PairingState{Code: code, Result: wa.PairingActive}, true)
		return so.window, nil
	}
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if rendered.Load() {
				so.setWindow(wa.PairingState{Result: wa.PairingSucceeded}, true)
				so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true, WAVersion: "2.3000.101"})
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	dir := privateDir(t)
	pr := startPair(t, so, nil, nil, "--data-dir", dir)
	deadline := time.Now().Add(10 * time.Second)
	for {
		out := pr.output()
		if strings.ContainsAny(out, "█▀▄") {
			rendered.Store(true)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no rendering within 10s\nstdout:\n%s", out)
		}
		time.Sleep(time.Millisecond)
	}
	if code := pr.waitExit(t, 10*time.Second); code != exitOK {
		t.Fatalf("exit code = %d\nstdout:\n%s\nstderr:\n%s", code, pr.output(), pr.diagnostics())
	}
	out := pr.output()
	if strings.Contains(out, code) {
		t.Error("stdout contains the raw pairing payload")
	}
	if strings.Contains(pr.diagnostics(), code) {
		t.Error("stderr or logs contain the raw pairing payload")
	}
	if !strings.Contains(out, "  ") {
		t.Error("the rendering lacks the quiet zone")
	}
}

// TestPairEncoderFailureExitsWithDiagnostic checks a payload within the byte bound that the QR
// encoder still rejects: a category-only diagnostic, no rendering.
func TestPairEncoderFailureExitsWithDiagnostic(t *testing.T) {
	so := newScriptedOwner()
	so.request = func(n int64) (wa.PairingState, error) {
		so.setWindow(wa.PairingState{Code: strings.Repeat("a", 3500), Result: wa.PairingActive}, true)
		return so.window, nil
	}
	dir := privateDir(t)
	pr := startPair(t, so, nil, nil, "--data-dir", dir)
	if code := pr.waitExit(t, 10*time.Second); code != exitError {
		t.Fatalf("exit code = %d\nstdout:\n%s\nstderr:\n%s", code, pr.output(), pr.diagnostics())
	}
	if strings.Contains(pr.output(), strings.Repeat("a", 100)) {
		t.Error("the oversized payload was rendered")
	}
	if !strings.Contains(pr.diagnostics(), "whatsapp-mcp: pair:") {
		t.Errorf("no diagnostic:\n%s", pr.diagnostics())
	}
}

// TestPairWindowTerminalOutcomes checks distinguishable terminal results.
func TestPairWindowTerminalOutcomes(t *testing.T) {
	for _, tc := range []struct {
		result wa.PairingResult
		want   string
	}{
		{wa.PairingTimedOut, "timed out"},
		{wa.PairingCancelled, "cancelled"},
		{wa.PairingFailed, "failed"},
	} {
		t.Run(string(tc.result), func(t *testing.T) {
			so := newScriptedOwner()
			so.request = func(n int64) (wa.PairingState, error) {
				so.setWindow(wa.PairingState{Result: tc.result}, true)
				return so.window, nil
			}
			dir := privateDir(t)
			pr := startPair(t, so, nil, nil, "--data-dir", dir)
			if code := pr.waitExit(t, 10*time.Second); code != exitError {
				t.Fatalf("exit code = %d\nstderr:\n%s", code, pr.diagnostics())
			}
			if !strings.Contains(pr.diagnostics(), tc.want) {
				t.Errorf("diagnostic = %q, want %q", pr.diagnostics(), tc.want)
			}
		})
	}
}

// TestPairAlreadyPairedDiagnostic checks the distinguishable already-paired outcome.
func TestPairAlreadyPairedDiagnostic(t *testing.T) {
	so := newScriptedOwner()
	so.request = func(n int64) (wa.PairingState, error) {
		return wa.PairingState{}, wa.ErrAlreadyPaired
	}
	dir := privateDir(t)
	pr := startPair(t, so, nil, nil, "--data-dir", dir)
	if code := pr.waitExit(t, 10*time.Second); code != exitError {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, pr.diagnostics())
	}
	if !strings.Contains(pr.diagnostics(), "already paired") {
		t.Errorf("diagnostic = %q", pr.diagnostics())
	}
}

// TestPairPendingRepairWarning checks the honest pending-repair warning after success.
func TestPairPendingRepairWarning(t *testing.T) {
	so := newScriptedOwner()
	so.health = wa.Health{RecoveryPending: true}
	so.startHook = func() {
		so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true, WAVersion: "2.3000.101"})
	}
	dir := privateDir(t)
	pr := startPair(t, so, nil, nil, "--data-dir", dir)
	if code := pr.waitExit(t, 10*time.Second); code != exitOK {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, pr.diagnostics())
	}
	if !strings.Contains(pr.diagnostics(), "repair") {
		t.Errorf("no pending-repair warning:\n%s", pr.diagnostics())
	}
}

// TestPairInterruptExitsUnsuccessfullyAfterCleanup delivers a real interrupt through the lifecycle's
// channel and checks the cleanup ordering.
func TestPairInterruptExitsUnsuccessfullyAfterCleanup(t *testing.T) {
	so := newScriptedOwner()
	so.request = func(n int64) (wa.PairingState, error) {
		so.setWindow(wa.PairingState{Result: wa.PairingActive}, true)
		return so.window, nil
	}
	signals := make(chan os.Signal, 2)
	dir := privateDir(t)
	pr := startPair(t, so, signals, nil, "--data-dir", dir)
	deadline := time.Now().Add(5 * time.Second)
	for !so.started.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	signals <- os.Interrupt
	if code := pr.waitExit(t, 10*time.Second); code != exitError {
		t.Fatalf("exit code = %d, want 1 after interrupt\nstderr:\n%s", code, pr.diagnostics())
	}
	if so.beginN.Load() == 0 {
		t.Error("the owner was not stopped after the interrupt")
	}
	if _, err := os.Stat(dbPath(dir) + "-wal"); err == nil {
		t.Error("the store was left open after the interrupt")
	}
}

// withTestLifecycleSignals installs a lifecycle factory that reads the test's signal channel.
func withTestLifecycleSignals(t *testing.T, signals chan os.Signal) {
	t.Helper()
	prev := lifecycleFactory
	t.Cleanup(func() { lifecycleFactory = prev })
	lifecycleFactory = func(logger *slog.Logger) *lifecycle {
		return newLifecycleWith(logger, signals, func(int) {}, 5*time.Second)
	}
}

// A denied post-pair reconnect ends in an unsuccessful, actionable result that says linking was not
// completed safely rather than claiming a usable saved session.
func TestPairDeniedFollowUpConnectionReportsUnsafeLinking(t *testing.T) {
	so := newScriptedOwner()
	so.status = wa.Status{LoggedIn: true}
	events := make(chan wa.Event, 1)
	events <- wa.Event{Phase: wa.PhaseDisconnected}
	var stderr strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if waitSavedConnection(so, events, &streams{stderr: &stderr}, ctx, true) {
		t.Fatal("a denied follow-up connection reported success")
	}
	for _, want := range []string{"could not be completed safely", "Offline recovery is required"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q: %s", want, stderr.String())
		}
	}
}
