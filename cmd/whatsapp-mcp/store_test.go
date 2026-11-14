package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
	_ "modernc.org/sqlite"
)

func dbPath(dir string) string { return filepath.Join(dir, "whatsapp.db") }

func TestStdioCreatesTheDatabase(t *testing.T) {
	dir := privateDir(t)
	r := runIn(t, nil, "stdio", "--data-dir", dir)
	r.expect(t, exitOK)
	if _, err := os.Stat(dbPath(dir)); err != nil {
		t.Fatalf("no database: %v", err)
	}
	if !strings.Contains(r.stderr, "event=store.ready") {
		t.Errorf("stderr lacks store.ready:\n%s", r.stderr)
	}
	second := runIn(t, nil, "stdio", "--data-dir", dir)
	second.expect(t, exitOK)
	if strings.Contains(second.stderr, "event=store.migrate") {
		t.Errorf("a second start ran a migration:\n%s", second.stderr)
	}
}

func TestLogTimeIsUTC(t *testing.T) {
	r := runIn(t, nil, "stdio", "--data-dir", privateDir(t))
	r.expect(t, exitOK)
	var checked int
	for _, line := range strings.Split(r.stderr, "\n") {
		if m := regexp.MustCompile(`^time=(\S+) `).FindStringSubmatch(line); m != nil {
			checked++
			if !strings.HasSuffix(m[1], "Z") {
				t.Errorf("log time %q is not UTC", m[1])
			}
		}
	}
	if checked == 0 {
		t.Error("no log line with a time")
	}
}

func TestPairOpensAndClosesTheStore(t *testing.T) {
	dir := privateDir(t)
	// The owner factory is injected; the store path and the exit codes are real.
	so := newScriptedOwner()
	so.startHook = func() {
		so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true, WAVersion: "2.3000.101"})
	}
	pr := startPair(t, so, nil, nil, "--data-dir", dir)
	if code := pr.waitExit(t, 10*time.Second); code != exitOK {
		t.Fatalf("exit code = %d\nstdout:\n%s\nstderr:\n%s", code, pr.output(), pr.diagnostics())
	}
	if _, err := os.Stat(dbPath(dir)); err != nil {
		t.Errorf("pair did not open the store: %v", err)
	}
	if _, err := os.Stat(dbPath(dir) + "-wal"); err == nil {
		t.Error("pair left the store open: -wal still exists")
	}
}

func TestStatusAndVersionDoNotCreateTheDatabase(t *testing.T) {
	dir := privateDir(t)
	runIn(t, nil, "status", "--data-dir", dir)
	runIn(t, nil, "version")
	if _, err := os.Stat(dbPath(dir)); err == nil {
		t.Error("status or version created whatsapp.db")
	}
}

func TestStdioRefusesANonDatabase(t *testing.T) {
	dir := privateDir(t)
	junk := make([]byte, 4096)
	for i := range junk {
		junk[i] = byte(i*13 + 5)
	}
	if err := os.WriteFile(dbPath(dir), junk, 0o600); err != nil {
		t.Fatal(err)
	}
	r := runIn(t, nil, "stdio", "--data-dir", dir)
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, "is not a usable SQLite database") {
		t.Errorf("stderr = %s", r.stderr)
	}
	if b, _ := os.ReadFile(dbPath(dir)); !bytes.Equal(b, junk) {
		t.Error("the file changed")
	}
}

func TestStdioRefusesATooNewSchemaAndReleasesTheLock(t *testing.T) {
	dir := privateDir(t)
	runIn(t, nil, "stdio", "--data-dir", dir).expect(t, exitOK)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath(dir))+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE mcp_version SET version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	for i := 0; i < 2; i++ {
		r := runIn(t, nil, "stdio", "--data-dir", dir)
		r.expect(t, exitError)
		for _, want := range []string{"message schema version 99", "newer than this binary supports (3)"} {
			if !strings.Contains(r.stderr, want) {
				t.Errorf("run %d: stderr lacks %q:\n%s", i, want, r.stderr)
			}
		}
		// The run must end with the lock released, so the next run can take it again.
		if !strings.Contains(r.stderr, "msg=stopped") {
			t.Errorf("run %d: did not stop cleanly:\n%s", i, r.stderr)
		}
	}
}

// runWith runs body as a stdio command would, so a test can drive the store through the real startup and
// shutdown path.
func runWith(t *testing.T, dir string, body func(context.Context, *lifecycle, *slog.Logger, *store.Store)) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	s := &streams{stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &stderr}
	cmd := &command{name: "stdio", flags: true}
	code := runShell("stdio", cmd, []string{"--data-dir", dir}, s, func(ctx context.Context, lc *lifecycle, logger *slog.Logger, st *store.Store, s *streams, _ *wa.LIDCursor) (*ownerHandle, bool) {
		body(ctx, lc, logger, st)
		<-lc.ctx.Done()
		return nil, false
	})
	return code, stderr.String()
}

func TestShutdownUnderWriteLoad(t *testing.T) {
	dir := privateDir(t)
	var acked atomic.Int64
	var shutdownStart atomic.Int64
	code, stderr := runWith(t, dir, func(ctx context.Context, lc *lifecycle, _ *slog.Logger, st *store.Store) {
		go func() {
			for i := 0; ; i++ {
				err := st.StoreMessage(context.Background(), store.Message{
					ChatJID: "491511234567@s.whatsapp.net", ID: fmt.Sprintf("load-%d", i), SenderJID: "491511234567@s.whatsapp.net",
					Time: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second), Text: "x",
				})
				if err != nil {
					return
				}
				acked.Add(1)
			}
		}()
		for acked.Load() < 50 {
			time.Sleep(time.Millisecond)
		}
		shutdownStart.Store(time.Now().UnixNano())
		lc.requestStop("test")
		<-ctx.Done()
	})
	elapsed := time.Since(time.Unix(0, shutdownStart.Load()))
	if code != exitOK {
		t.Fatalf("exit code = %d\n%s", code, stderr)
	}
	if elapsed >= shutdownTimeout {
		t.Errorf("shutdown took %v, want under %v", elapsed, shutdownTimeout)
	}
	if !strings.Contains(stderr, "event=store.close") {
		t.Errorf("store did not close:\n%s", stderr)
	}

	// Every write the store acknowledged is there after a reopen.
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath(dir))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int64
	if err := db.QueryRow("SELECT count(*) FROM messages").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < acked.Load() {
		t.Errorf("%d messages stored, %d were acknowledged", n, acked.Load())
	}
}

// --- Real owner-shell behaviour over isolated synthetic paths. ---

// shellRun drives one shell command in this process with streaming output capture and an optional
// signal channel, like startPair does for pair.
type shellRun struct {
	done   chan int
	stdout syncBuffer
	stderr syncBuffer
}

func startShell(t *testing.T, kind string, signals chan os.Signal, environ []string, args ...string) *shellRun {
	t.Helper()
	return startShellStdin(t, kind, signals, strings.NewReader(""), environ, args...)
}

func startShellStdin(t *testing.T, kind string, signals chan os.Signal, stdin io.Reader, environ []string, args ...string) *shellRun {
	t.Helper()
	pr := &shellRun{done: make(chan int, 1)}
	if signals != nil {
		withTestLifecycleSignals(t, signals)
	}
	go func() {
		code := run(append([]string{kind}, args...), stdin, &pr.stdout, &pr.stderr, environ)
		pr.done <- code
	}()
	return pr
}

func (pr *shellRun) diagnostics() string {
	pr.stderr.mu.Lock()
	defer pr.stderr.mu.Unlock()
	return pr.stderr.buf.String()
}

func (pr *shellRun) output() string {
	pr.stdout.mu.Lock()
	defer pr.stdout.mu.Unlock()
	return pr.stdout.buf.String()
}

func (pr *shellRun) waitExit(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case code := <-pr.done:
		return code
	case <-time.After(timeout):
		t.Fatalf("%s did not exit within %v\nstdout:\n%s\nstderr:\n%s", "shell", timeout, pr.output(), pr.diagnostics())
		return 0
	}
}

// seedSavedIdentity arms and saves a synthetic device through the guarded path, exactly like a real
// pairing does, and returns the JID string.
func seedSavedIdentity(t *testing.T, dir string) string {
	t.Helper()
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	dev, barrier, err := st.LoadDevice(context.Background())
	if err != nil {
		t.Fatalf("seed LoadDevice: %v", err)
	}
	jid := types.NewJID("490000000001", types.DefaultUserServer)
	dev.ID = &jid
	dev.PushName = "Test Account"
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("details"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := dev.Save(context.Background()); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	if err := barrier.Retire(context.Background()); err != nil {
		t.Fatalf("seed retire: %v", err)
	}
	st.Close(context.Background())
	return jid.String()
}

// refusedRealSessionFactory is a real wa.Session factory whose websocket dials a refused synthetic
// transport, so every connect attempt fails fast and is counted without a network.
func refusedRealSessionFactory(counter *atomic.Int64) wa.SessionFactory {
	return func(cfg wa.SessionConfig) (wa.SessionHandle, error) {
		cfg.PreLoginHTTP = &http.Client{Transport: roundTripCount{counter}}
		cfg.WebsocketHTTP = &http.Client{Transport: roundTripCount{counter}}
		return wa.OpenSession(cfg)
	}
}

// roundTripCount counts attempts and refuses them.
type roundTripCount struct{ counter *atomic.Int64 }

func (r roundTripCount) RoundTrip(*http.Request) (*http.Response, error) {
	if r.counter != nil {
		r.counter.Add(1)
	}
	return nil, errors.New("synthetic refusal")
}

// roundTripStall blocks until the request context ends.
type roundTripStall struct{ counter *atomic.Int64 }

func (r roundTripStall) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.counter != nil {
		r.counter.Add(1)
	}
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// withRealOwnerSeam installs the owner seam with a denying discovery transport and the given real
// session factory, so the shell uses the real owner without any network.
func withRealOwnerSeam(t *testing.T, sessions wa.SessionFactory) {
	t.Helper()
	prev := ownerSeam
	t.Cleanup(func() { ownerSeam = prev })
	ownerSeam = &ownerSeamConfig{
		discoveryRoundTripper: roundTripCount{nil},
		sessionFactory:        sessions,
	}
}

// TestShellSavedSessionStartupExhaustion drives the real owner through the bounded startup budget
// for both shells: one attempt plus three five-second retries, then the actionable diagnostic, with
// the identity retained and no pairing.
func TestShellSavedSessionStartupExhaustion(t *testing.T) {
	for _, kind := range []string{"serve", "stdio"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateDir(t)
			seedSavedIdentity(t, dir)
			var dials atomic.Int64
			withRealOwnerSeam(t, refusedRealSessionFactory(&dials))
			signals := make(chan os.Signal, 2)
			stdin := blockingReader{closed: make(chan struct{})}
			pr := startShellStdin(t, kind, signals, stdin, nil, "--data-dir", dir)
			deadline := time.Now().Add(60 * time.Second)
			for !strings.Contains(pr.diagnostics(), "reconnect_denied") && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if !strings.Contains(pr.diagnostics(), "reconnect_denied") {
				t.Fatalf("no reconnect-denied diagnostic within 60s:\n%s", pr.diagnostics())
			}
			// Startup retries are denied: the first attempt is the only dial, and the former
			// five-second retry wait elapses without a second one.
			time.Sleep(6 * time.Second)
			if got := dials.Load(); got != 1 {
				t.Errorf("connect dials = %d, want 1", got)
			}
			// The shell stays alive, disconnected, without pairing.
			signals <- os.Interrupt
			// A production Connect was attempted and refused, and the stop left nothing running: the quiet-stop
			// check passes, so a graceful stop exits successfully and the session can be reused.
			if code := pr.waitExit(t, 20*time.Second); code != exitOK {
				t.Fatalf("exit code = %d, want %d after a quiet stop\nstderr:\n%s", code, exitOK, pr.diagnostics())
			}
			if strings.Contains(pr.diagnostics(), "run_guard_recovery_required") || !strings.Contains(pr.diagnostics(), "quiet_stop_passed") {
				t.Errorf("a quiet stop must pass the check and demand no recovery:\n%s", pr.diagnostics())
			}
			// Reopen and verify that the saved identity row remains present.
			db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath(dir))+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var rows int
			var jid string
			if err := db.QueryRow("SELECT count(*), coalesce(max(jid), '') FROM whatsmeow_device").Scan(&rows, &jid); err != nil {
				t.Fatalf("device rows: %v", err)
			}
			if rows != 1 || !strings.HasPrefix(jid, "490000000001") {
				t.Errorf("device rows = %d jid = %q; the identity must be preserved", rows, jid)
			}
		})
	}
}

// TestShellSavedSessionAttemptTimeoutAndCancellation covers the 30-second attempt watchdog and the
// cancellable five-second retry wait with the real owner, for both shells.
func TestShellSavedSessionAttemptTimeoutAndCancellation(t *testing.T) {
	for _, kind := range []string{"serve", "stdio"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateDir(t)
			seedSavedIdentity(t, dir)
			var dials atomic.Int64
			withRealOwnerSeam(t, func(cfg wa.SessionConfig) (wa.SessionHandle, error) {
				cfg.PreLoginHTTP = &http.Client{Transport: roundTripStall{}}
				cfg.WebsocketHTTP = &http.Client{Transport: roundTripStall{&dials}}
				return wa.OpenSession(cfg)
			})
			signals := make(chan os.Signal, 2)
			stdin := blockingReader{closed: make(chan struct{})}
			pr := startShellStdin(t, kind, signals, stdin, nil, "--data-dir", dir)
			deadline := time.Now().Add(90 * time.Second)
			for !strings.Contains(pr.diagnostics(), "connect_timeout") && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
			if !strings.Contains(pr.diagnostics(), "connect_timeout") {
				t.Fatalf("no watchdog timeout diagnostic within 90s:\n%s", pr.diagnostics())
			}
			if got := dials.Load(); got != 1 {
				t.Errorf("connect dials = %d, want 1", got)
			}
			// Stop while the five-second retry wait is pending; no second attempt may start.
			signals <- os.Interrupt
			if code := pr.waitExit(t, 20*time.Second); code != exitOK {
				t.Fatalf("exit code = %d, want %d after a quiet stop\nstderr:\n%s", code, exitOK, pr.diagnostics())
			}
			if strings.Contains(pr.diagnostics(), "run_guard_recovery_required") || !strings.Contains(pr.diagnostics(), "quiet_stop_passed") {
				t.Errorf("a quiet stop must pass the check and demand no recovery:\n%s", pr.diagnostics())
			}
			if got := dials.Load(); got != 1 {
				t.Errorf("connect dials after cancellation = %d, want 1", got)
			}
		})
	}
}

// fakeWaSession is a scripted wa.SessionHandle for shell-level owner tests.
type fakeWaSession struct {
	cfg       wa.SessionConfig
	identity  bool
	fault     chan struct{}
	failFirst int

	mu         sync.Mutex
	connectN   int
	connectErr error
}

func (f *fakeWaSession) Epoch() uint64         { return f.cfg.Epoch }
func (f *fakeWaSession) HasIdentity() bool     { return f.identity }
func (f *fakeWaSession) RecoveryPending() bool { return false }
func (f *fakeWaSession) StorageFaulted() <-chan struct{} {
	if f.fault == nil {
		return nil
	}
	return f.fault
}

func (f *fakeWaSession) Connect(context.Context) error {
	f.mu.Lock()
	f.connectN++
	fail := f.connectN <= f.failFirst
	err := f.connectErr
	f.mu.Unlock()
	if fail {
		return err
	}
	return nil
}

func (f *fakeWaSession) Disconnect() {}

func (f *fakeWaSession) Retire(context.Context) error { return nil }

func (f *fakeWaSession) send(ev wa.SessionEvent) {
	f.cfg.Dispatch(f.cfg.Epoch, ev)
}

func (f *fakeWaSession) connects() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connectN
}

// withFakeOwnerSeam installs the owner seam with a scripted session factory, so the shell runs the
// real owner against a fake generation.
func withFakeOwnerSeam(t *testing.T, mk func(cfg wa.SessionConfig) *fakeWaSession) {
	t.Helper()
	prev := ownerSeam
	t.Cleanup(func() { ownerSeam = prev })
	ownerSeam = &ownerSeamConfig{
		discoveryRoundTripper: roundTripCount{nil},
		sessionFactory: func(cfg wa.SessionConfig) (wa.SessionHandle, error) {
			fs := mk(cfg)
			fs.cfg = cfg
			return fs, nil
		},
	}
}

// blockingReader is a stdin that stays open until the test closes it.
type blockingReader struct{ closed chan struct{} }

func (r blockingReader) Read([]byte) (int, error) {
	<-r.closed
	return 0, io.EOF
}

// TestShellSavedSessionFailureIsNotRetried drives the real owner through a failed first attempt for
// both shells: the retry is denied with a diagnostic, never started, and the shell stays alive.
func TestShellSavedSessionFailureIsNotRetried(t *testing.T) {
	for _, kind := range []string{"serve", "stdio"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateDir(t)
			var fs *fakeWaSession
			withFakeOwnerSeam(t, func(cfg wa.SessionConfig) *fakeWaSession {
				fs = &fakeWaSession{identity: true, failFirst: 2, connectErr: errors.New("synthetic refusal")}
				return fs
			})
			signals := make(chan os.Signal, 2)
			stdin := blockingReader{closed: make(chan struct{})}
			pr := startShellStdin(t, kind, signals, stdin, nil, "--data-dir", dir)
			deadline := time.Now().Add(30 * time.Second)
			for !strings.Contains(pr.diagnostics(), "reconnect_denied") && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if !strings.Contains(pr.diagnostics(), "reconnect_denied") {
				t.Fatalf("no reconnect-denied diagnostic:\n%s", pr.diagnostics())
			}
			// Outlast the former five-second retry wait.
			time.Sleep(6 * time.Second)
			if got := fs.connects(); got != 1 {
				t.Errorf("connects = %d, want 1; startup retries are denied", got)
			}
			signals <- os.Interrupt
			// The scripted session has no production Connect latch, so only the retry denial is
			// under test here; the real adapter's unsafe exit is covered by the exhaustion test.
			if code := pr.waitExit(t, 15*time.Second); code != exitOK {
				t.Fatalf("exit code = %d\nstderr:\n%s", code, pr.diagnostics())
			}
		})
	}
}

// TestShellSavedSessionOutdatedHandoff checks that a 405 event stops the shell startup budget for
// both shells without further attempts, with the identity preserved and no pairing.
func TestShellSavedSessionOutdatedHandoff(t *testing.T) {
	for _, kind := range []string{"serve", "stdio"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateDir(t)
			// The factory runs on the owner's goroutine while this one polls, so the session is shared atomically.
			var fsp atomic.Pointer[fakeWaSession]
			withFakeOwnerSeam(t, func(cfg wa.SessionConfig) *fakeWaSession {
				fs := &fakeWaSession{identity: true, failFirst: 1, connectErr: errors.New("synthetic refusal")}
				fsp.Store(fs)
				return fs
			})
			signals := make(chan os.Signal, 2)
			stdin := blockingReader{closed: make(chan struct{})}
			pr := startShellStdin(t, kind, signals, stdin, nil, "--data-dir", dir)
			connects := func() int {
				if fs := fsp.Load(); fs != nil {
					return fs.connects()
				}
				return 0
			}
			deadline := time.Now().Add(10 * time.Second)
			for connects() < 1 && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			fs := fsp.Load()
			if fs == nil {
				t.Fatal("the shell never created its generation")
			}
			fs.send(wa.SessionClientOutdated)
			// Give the budget one full retry window; the handoff must have stopped it.
			time.Sleep(6 * time.Second)
			if got := connects(); got != 1 {
				t.Errorf("connects after 405 handoff = %d, want 1", got)
			}
			signals <- os.Interrupt
			if code := pr.waitExit(t, 15*time.Second); code != exitOK {
				t.Fatalf("exit code = %d\nstderr:\n%s", code, pr.diagnostics())
			}
		})
	}
}
