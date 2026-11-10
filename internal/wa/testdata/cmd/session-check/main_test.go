package main

// These tests drive the helper with a scripted owner and no network. They are never run by
// ./... (testdata is excluded) and none of them builds the production owner.

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tiger-seo/whatsapp-mcp/internal/config"
	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

const testPayload = "SYNTHETIC-QR-PAYLOAD-2@abc,def,ghi"

// scripted is an owner with no client behind it.
type scripted struct {
	mu          sync.Mutex
	started     atomic.Int64
	requests    atomic.Int64
	cancels     atomic.Int64
	pairCalls   atomic.Int64
	status      wa.Status
	subs        []func(wa.Event)
	requestErr  []error // one entry per RequestPairing call; nil or missing means a window opens
	startHook   func(*scripted)
	stopErr     error
	done        chan struct{}
	stopOnce    sync.Once
	windowOpen  bool
	windowClose bool
}

func newScripted() *scripted { return &scripted{done: make(chan struct{})} }

func (s *scripted) emit(p wa.Phase) {
	s.mu.Lock()
	subs := append([]func(wa.Event){}, s.subs...)
	s.mu.Unlock()
	for _, f := range subs {
		f(wa.Event{Phase: p})
	}
}

func (s *scripted) Start(context.Context) error {
	s.started.Add(1)
	if s.startHook != nil {
		s.startHook(s)
	}
	return nil
}
func (s *scripted) BeginStop()            { s.stopOnce.Do(func() { close(s.done) }) }
func (s *scripted) Done() <-chan struct{} { return s.done }
func (s *scripted) StopError() error      { return s.stopErr }
func (s *scripted) Snapshot() wa.Status   { s.mu.Lock(); defer s.mu.Unlock(); return s.status }
func (s *scripted) Health() wa.Health     { return wa.Health{} }
func (s *scripted) CancelPairing() {
	s.cancels.Add(1)
	s.mu.Lock()
	s.windowOpen = false
	s.mu.Unlock()
}
func (s *scripted) Subscribe(f func(wa.Event)) func() {
	s.mu.Lock()
	s.subs = append(s.subs, f)
	s.mu.Unlock()
	return func() {}
}
func (s *scripted) RequestPairing(context.Context) (wa.PairingState, error) {
	n := int(s.requests.Add(1))
	if n <= len(s.requestErr) && s.requestErr[n-1] != nil {
		return wa.PairingState{}, s.requestErr[n-1]
	}
	s.mu.Lock()
	s.windowOpen = true
	s.mu.Unlock()
	return wa.PairingState{Code: testPayload, Result: wa.PairingActive}, nil
}
func (s *scripted) PairingSnapshot() (wa.PairingState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.windowOpen {
		return wa.PairingState{}, false
	}
	if s.pairCalls.Add(1) < 2 {
		return wa.PairingState{Code: testPayload, Result: wa.PairingActive}, true
	}
	return wa.PairingState{Result: wa.PairingSucceeded}, true
}

type rig struct {
	t     *testing.T
	dir   string
	owner *scripted
	built atomic.Int64
	out   bytes.Buffer
	errb  bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	return &rig{t: t, dir: filepath.Join(t.TempDir(), "acceptance-one"), owner: newScripted()}
}

func (r *rig) deps(input string, interactive bool) deps {
	return deps{
		stdin:       strings.NewReader(input),
		stdout:      &r.out,
		stderr:      &r.errb,
		interactive: func() bool { return interactive },
		defaultDir:  func() (string, error) { return filepath.Join(r.t.TempDir(), "default-data"), nil },
		newOwner: func(env ownerEnv) (wa.Owner, error) {
			r.built.Add(1)
			return r.owner, nil
		},
	}
}

func (r *rig) run(args []string, input string) int {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return run(ctx, args, r.deps(input, true))
}

func (r *rig) assertNothingHappened() {
	r.t.Helper()
	if r.built.Load() != 0 || r.owner.started.Load() != 0 || r.owner.requests.Load() != 0 {
		r.t.Fatalf("account work happened: built=%d started=%d requests=%d", r.built.Load(), r.owner.started.Load(), r.owner.requests.Load())
	}
	if _, err := os.Stat(r.dir); !os.IsNotExist(err) {
		r.t.Fatalf("the acceptance directory was created before approval: %v", err)
	}
}

func lockFree(t *testing.T, dir string) {
	t.Helper()
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	_ = l.Release()
}

func TestRejectsMissingRelativeAndDefaultDirectory(t *testing.T) {
	r := newRig(t)
	cases := [][]string{
		{"-action", "connect"},
		{"-dir", "relative/dir", "-action", "connect"},
		{"-dir", r.dir},
		{"-dir", r.dir, "-action", "send"},
	}
	for _, args := range cases {
		if code := r.run(args, "I permit connect in acceptance-one\n"); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
	r.assertNothingHappened()

	def := filepath.Join(t.TempDir(), "default-data")
	d := r.deps("I permit connect in default-data\n", true)
	d.defaultDir = func() (string, error) { return def, nil }
	if code := run(context.Background(), []string{"-dir", def, "-action", "connect"}, d); code != exitUsage {
		t.Errorf("the default data directory was accepted: exit %d", code)
	}
	if _, err := os.Stat(def); !os.IsNotExist(err) {
		t.Error("the default directory was created")
	}
}

func TestRefusesNonInteractiveInput(t *testing.T) {
	r := newRig(t)
	d := r.deps("I permit connect in acceptance-one\n", false)
	if code := run(context.Background(), []string{"-dir", r.dir, "-action", "connect"}, d); code != exitRefused {
		t.Fatalf("exit %d", code)
	}
	r.assertNothingHappened()
}

func TestWrongOrMalformedApprovalChangesNothing(t *testing.T) {
	for name, input := range map[string]string{
		"empty":           "\n",
		"eof":             "",
		"upper_case":      "I PERMIT CONNECT IN ACCEPTANCE-ONE\n",
		"padded":          " I permit connect in acceptance-one\n",
		"other_action":    "I permit pair in acceptance-one\n",
		"other_dir":       "I permit connect in acceptance-two\n",
		"prefix_only":     "I permit connect\n",
		"suffix":          "I permit connect in acceptance-one now\n",
		"yes":             "yes\n",
		"oversized":       strings.Repeat("A", 4096) + "\nI permit connect in acceptance-one\n",
		"terminal_escape": "I permit connect in acceptance-one\x1b[2J\n",
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			if code := r.run([]string{"-dir", r.dir, "-action", "connect"}, input); code != exitRefused {
				t.Fatalf("exit %d\nstderr:\n%s", code, r.errb.String())
			}
			r.assertNothingHappened()
			if strings.Contains(r.out.String()+r.errb.String(), testPayload) {
				t.Error("a QR payload reached the output")
			}
		})
	}
}

func markUnsafe(t *testing.T, dir string) {
	t.Helper()
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE unsafe_run SET state='unsafe'`); err != nil {
		t.Fatal(err)
	}
}

func TestUnsafeStoreRefusesBeforeAnyOwner(t *testing.T) {
	r := newRig(t)
	if err := os.MkdirAll(filepath.Dir(r.dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.PrepareDataDir(r.dir); err != nil {
		t.Fatal(err)
	}
	markUnsafe(t, r.dir)
	if code := r.run([]string{"-dir", r.dir, "-action", "pair"}, "I permit pair in acceptance-one\n"); code != exitRefused {
		t.Fatalf("exit %d\nstderr:\n%s", code, r.errb.String())
	}
	if r.built.Load() != 0 || r.owner.started.Load() != 0 {
		t.Fatal("an owner was built on an unsafe store")
	}
	if !strings.Contains(strings.ToLower(r.errb.String()), "offline recovery") {
		t.Errorf("no recovery guidance: %s", r.errb.String())
	}
	lockFree(t, r.dir)
}

func TestConnectRunsOnceObservesAndReleasesEverything(t *testing.T) {
	r := newRig(t)
	r.owner.startHook = func(s *scripted) {
		s.mu.Lock()
		s.status = wa.Status{Connected: true, LoggedIn: true}
		s.mu.Unlock()
		go s.emit(wa.PhaseConnected)
	}
	code := r.run([]string{"-dir", r.dir, "-action", "connect"}, "I permit connect in acceptance-one\ndone\n")
	if code != exitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, r.out.String(), r.errb.String())
	}
	if r.owner.started.Load() != 1 || r.built.Load() != 1 {
		t.Errorf("started %d, built %d", r.owner.started.Load(), r.built.Load())
	}
	if r.owner.requests.Load() != 0 {
		t.Error("connect asked for a pairing window")
	}
	out := r.out.String() + r.errb.String()
	for _, want := range []string{"unsafe", "never unlinks"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	lockFree(t, r.dir)
}

func TestPairRendersTheCodeOnStdoutOnly(t *testing.T) {
	r := newRig(t)
	code := r.run([]string{"-dir", r.dir, "-action", "pair"}, "I permit pair in acceptance-one\ndone\n")
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, r.errb.String())
	}
	if r.owner.requests.Load() != 1 {
		t.Errorf("%d pairing windows requested", r.owner.requests.Load())
	}
	if strings.Contains(r.out.String(), testPayload) || strings.Contains(r.errb.String(), testPayload) {
		t.Error("the raw QR payload was printed")
	}
	if !strings.Contains(r.out.String(), "\u2588") && !strings.Contains(r.out.String(), "\u2580") && !strings.Contains(r.out.String(), "\u2584") {
		t.Errorf("no rendered code on stdout:\n%s", r.out.String())
	}
	lockFree(t, r.dir)
}

func TestLogoutIsObservedAndNoRepairIsOffered(t *testing.T) {
	r := newRig(t)
	r.owner.startHook = func(s *scripted) {
		s.mu.Lock()
		s.status = wa.Status{Connected: true, LoggedIn: true}
		s.mu.Unlock()
		go func() {
			s.emit(wa.PhaseConnected)
			time.Sleep(50 * time.Millisecond)
			s.emit(wa.PhaseLoggedOut)
		}()
	}
	// No "done" and stdin stays open: the logout alone ends the observation.
	pr, pw := io.Pipe()
	defer pw.Close()
	go func() { _, _ = pw.Write([]byte("I permit connect in acceptance-one\n")) }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := r.deps("", true)
	d.stdin = pr
	code := run(ctx, []string{"-dir", r.dir, "-action", "connect"}, d)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, r.errb.String())
	}
	if !strings.Contains(r.out.String()+r.errb.String(), "logout observed") {
		t.Errorf("the logout was not reported:\n%s", r.out.String()+r.errb.String())
	}
	if r.owner.requests.Load() != 0 {
		t.Error("a pairing window was requested after the logout")
	}
}

func TestDenialCheckPassesWhenTheSecondPairIsRefused(t *testing.T) {
	r := newRig(t)
	r.owner.requestErr = []error{nil, store.ErrUnsafeRun}
	input := "I permit pair in acceptance-one\ndone\nI permit the denial check\n"
	code := r.run([]string{"-dir", r.dir, "-action", "pair", "-denial"}, input)
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, r.errb.String())
	}
	if r.owner.requests.Load() != 2 {
		t.Errorf("%d requests, want 2", r.owner.requests.Load())
	}
	if !strings.Contains(r.out.String()+r.errb.String(), "denial confirmed") {
		t.Errorf("no denial result:\n%s", r.out.String()+r.errb.String())
	}
}

func TestDenialCheckFailsAndClosesAWindowThatOpened(t *testing.T) {
	r := newRig(t)
	r.owner.requestErr = nil // the second request is wrongly accepted
	input := "I permit pair in acceptance-one\ndone\nI permit the denial check\n"
	code := r.run([]string{"-dir", r.dir, "-action", "pair", "-denial"}, input)
	if code != exitFailed {
		t.Fatalf("exit %d, want %d\nstderr:\n%s", code, exitFailed, r.errb.String())
	}
	if r.owner.cancels.Load() == 0 {
		t.Error("the unexpected window was left open")
	}
	if !strings.Contains(r.errb.String(), "NOT denied") {
		t.Errorf("no failure message:\n%s", r.errb.String())
	}
}

func TestDenialCheckNeedsItsOwnApproval(t *testing.T) {
	r := newRig(t)
	input := "I permit pair in acceptance-one\ndone\nyes\n"
	code := r.run([]string{"-dir", r.dir, "-action", "pair", "-denial"}, input)
	if code != exitRefused {
		t.Fatalf("exit %d", code)
	}
	if r.owner.requests.Load() != 1 {
		t.Errorf("%d pairing requests; the denial check ran without approval", r.owner.requests.Load())
	}
	lockFree(t, r.dir)
}
