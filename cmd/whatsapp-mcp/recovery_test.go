package main

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

// Journeys across several invocations of the real commands against one real data directory.
// Nothing here contacts WhatsApp: owners and sessions are synthetic.

const sentinelChat = "490000000042@s.whatsapp.net"

// seedUnsafeWithSentinel arms the run marker (so the state is unsafe, as after an attempted
// production Connect) and stores a chat that must survive every step.
func seedUnsafeWithSentinel(t *testing.T, dir string) {
	t.Helper()
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateChats(context.Background(), []store.ChatUpdate{{JID: sentinelChat, LastActivity: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if err := st.StoreMessages(context.Background(), []store.Message{{ChatJID: sentinelChat, ID: "SENTINEL1", SenderJID: sentinelChat, Time: time.Now(), Text: "sentinel"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ArmRunGuard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, dir, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(dbPath(dir), "\\", "/")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func markerState(t *testing.T, dir string) string {
	t.Helper()
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	state, _, err := st.RunGuard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// After an attempted production Connect the marker is unsafe. A plain invocation, a redirected
// recovery and a refused confirmation all leave it unsafe and start nothing; only the separately
// authorized interactive recovery clears it, and only then can a fresh pair run, once.
func TestJourneyUnsafeRunNeedsOfflineRecoveryBeforeFreshPair(t *testing.T) {
	dir := privateDir(t)
	seedUnsafeWithSentinel(t, dir)

	old := recoveryInteractive
	t.Cleanup(func() { recoveryInteractive = old })

	// 1. A plain pair, with no terminal: refused, no owner, marker unchanged.
	so := newScriptedOwner()
	withScriptedOwner(t, so)
	recoveryInteractive = func(*streams) bool { return false }
	var out, diag bytes.Buffer
	if code := run([]string{"pair", "--data-dir", dir}, strings.NewReader("\n"), &out, &diag, nil); code != exitError {
		t.Fatalf("plain pair on an unsafe store exited %d", code)
	}
	if so.started.Load() || so.requestN.Load() != 0 {
		t.Fatal("account work started on an unsafe store")
	}
	if got := markerState(t, dir); got != "unsafe" {
		t.Fatalf("marker after the refused pair = %q", got)
	}

	// 2. Interactive but unconfirmed: still unsafe.
	recoveryInteractive = func(*streams) bool { return true }
	out.Reset()
	diag.Reset()
	if code := run([]string{"pair", "--data-dir", dir}, strings.NewReader("recover\nabsent\nno\n"), &out, &diag, nil); code != exitError {
		t.Fatalf("unconfirmed recovery exited %d", code)
	}
	if got := markerState(t, dir); got != "unsafe" {
		t.Fatalf("marker after unconfirmed recovery = %q", got)
	}

	// 3. Confirmed recovery: clean marker, no owner, a separate invocation is still required.
	out.Reset()
	diag.Reset()
	if code := run([]string{"pair", "--data-dir", dir}, strings.NewReader("recover\nabsent\nverified\n"), &out, &diag, nil); code != exitOK {
		t.Fatalf("confirmed recovery exited %d: %s", code, diag.String())
	}
	if so.started.Load() || so.requestN.Load() != 0 {
		t.Fatal("recovery itself started account work")
	}
	if !strings.Contains(out.String(), "separate invocation") {
		t.Errorf("recovery did not say a fresh pair needs a separate invocation:\n%s", out.String())
	}
	if got := markerState(t, dir); got != "clean" {
		t.Fatalf("marker after confirmed recovery = %q", got)
	}

	// 4. The fresh pair runs exactly once.
	fresh := newScriptedOwner()
	fresh.request = func(int64) (wa.PairingState, error) {
		fresh.setWindow(wa.PairingState{Result: wa.PairingSucceeded}, true)
		fresh.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true, WAVersion: "2.3000.101"})
		return fresh.window, nil
	}
	withScriptedOwner(t, fresh)
	pr := startPair(t, fresh, nil, nil, "--data-dir", dir)
	if code := pr.waitExit(t, 10*time.Second); code != exitOK {
		t.Fatalf("fresh pair exited %d\n%s", code, pr.diagnostics())
	}
	if n := fresh.requestN.Load(); n != 1 {
		t.Errorf("fresh pair requested %d windows, want 1", n)
	}

	// The chat survived every step.
	if n := countRows(t, dir, "chats"); n != 1 {
		t.Errorf("chats = %d, want the 1 sentinel", n)
	}
	if n := countRows(t, dir, "messages"); n != 1 {
		t.Errorf("messages = %d, want the 1 sentinel", n)
	}
}

// The recovery barrier comes before repair: with an unsafe marker and a pending repair, nothing
// reconnects or repairs, and offline recovery keeps the repair marker and the saved identity's
// rows for the later authorized run.
func TestJourneyRecoveryBarrierPrecedesRepairAndPreservesIt(t *testing.T) {
	dir := privateDir(t)
	seedInterruptedRepair(t, dir)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ArmRunGuard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	old := recoveryInteractive
	t.Cleanup(func() { recoveryInteractive = old })
	recoveryInteractive = func(*streams) bool { return false }
	so := newScriptedOwner()
	withScriptedOwner(t, so)
	var out, diag bytes.Buffer
	if code := run([]string{"pair", "--data-dir", dir}, strings.NewReader(""), &out, &diag, nil); code != exitError {
		t.Fatalf("pair on an unsafe store with a pending repair exited %d", code)
	}
	if so.started.Load() {
		t.Fatal("an owner started before offline recovery")
	}
	if got := pendingRepairs(t, dir); len(got) != 1 {
		t.Fatalf("repair markers after the refusal = %v", got)
	}

	// Recovery of a store that still holds the identity needs the separate removal confirmation.
	// It removes only that identity: chats and messages stay, and the pending repair need is
	// carried in the run marker for the later authorized run.
	recoveryInteractive = func(*streams) bool { return true }
	diag.Reset()
	in := "recover\nunlink\nauthorize-unlink\nverified\nremove\n"
	if code := run([]string{"pair", "--data-dir", dir}, strings.NewReader(in), &out, &diag, nil); code != exitOK {
		t.Fatalf("recovery exited %d: %s", code, diag.String())
	}
	if so.started.Load() || so.requestN.Load() != 0 {
		t.Fatal("recovery started account work")
	}
	st, err = store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	state, bits, err := st.RunGuard(context.Background())
	st.Close(context.Background())
	if err != nil || state != "clean" {
		t.Fatalf("marker after recovery = %q, %v", state, err)
	}
	if bits == 0 {
		t.Error("recovery dropped the pending repair need")
	}
	if n := countRows(t, dir, "whatsmeow_device"); n != 0 {
		t.Errorf("device rows after authorized removal = %d", n)
	}
}

// A final timeout leaves the attempt unresolved: another pair in the same invocation is never
// started, and the next invocation sees the store as it was left.
func TestJourneyFinalTimeoutStartsNoSecondWindow(t *testing.T) {
	dir := privateDir(t)
	so := newScriptedOwner()
	so.request = func(int64) (wa.PairingState, error) {
		so.setWindow(wa.PairingState{Result: wa.PairingTimedOut}, true)
		return so.window, nil
	}
	pr := startPairWithInput(t, so, nil, strings.NewReader("\n\n"), "--data-dir", dir)
	if code := pr.waitExit(t, 10*time.Second); code != exitError {
		t.Fatalf("exit %d\n%s", code, pr.diagnostics())
	}
	if n := so.requestN.Load(); n != 1 {
		t.Errorf("windows requested = %d, want 1", n)
	}
	if !strings.Contains(pr.diagnostics(), "Offline recovery is required") {
		t.Errorf("no recovery guidance:\n%s", pr.diagnostics())
	}
}

// connectingSession is the real adapter with a synthetic Connect that confirms at once, so the
// real owner reaches Connected and starts its repair pass. The pass then uses the real pinned
// FetchAppState on a client that has no connection, which fails without any network.
type connectingSession struct {
	*wa.Session
	dispatch func(uint64, wa.SessionEvent)
	epoch    uint64
}

func (c *connectingSession) Connect(context.Context) error {
	go c.dispatch(c.epoch, wa.SessionConnected)
	return nil
}

// seedInterruptedRepair saves an identity and then leaves a durable repair marker, as a process
// killed in the middle of an app-state write would.
func seedInterruptedRepair(t *testing.T, dir string) {
	t.Helper()
	seedSavedIdentity(t, dir)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev, barrier, err := st.LoadDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(context.Background(), "critical_block", 3, [128]byte{1}); err != nil {
		t.Fatalf("progress write: %v", err)
	}
	if err := barrier.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func pendingRepairs(t *testing.T, dir string) []string {
	t.Helper()
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	_, barrier, err := st.LoadDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Retire(context.Background())
	names, err := barrier.PendingRepairs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// A repair marker survives a pass that cannot finish and a restart, and the next invocation still
// connects and tries again without losing it. Chats are untouched throughout.
func TestJourneyRepairMarkerSurvivesFailedPassAndRestart(t *testing.T) {
	dir := privateDir(t)
	seedInterruptedRepair(t, dir)
	if got := pendingRepairs(t, dir); len(got) != 1 {
		t.Fatalf("seeded markers = %v", got)
	}

	prevOwner, prevSeam := newOwner, ownerSeam
	t.Cleanup(func() { newOwner, ownerSeam = prevOwner, prevSeam })
	newOwner = productionOwner
	ownerSeam = &ownerSeamConfig{
		discoveryRoundTripper: roundTripCount{nil},
		sessionFactory: func(cfg wa.SessionConfig) (wa.SessionHandle, error) {
			s, err := wa.OpenSession(cfg)
			if err != nil {
				return nil, err
			}
			return &connectingSession{Session: s, dispatch: cfg.Dispatch, epoch: cfg.Epoch}, nil
		},
	}

	for round := 1; round <= 2; round++ {
		var out, diag bytes.Buffer
		code := run([]string{"pair", "--data-dir", dir}, strings.NewReader(""), &out, &diag, nil)
		if code != exitOK {
			t.Fatalf("round %d: pair exited %d\n%s", round, code, diag.String())
		}
		if got := pendingRepairs(t, dir); len(got) != 1 || got[0] != "critical_block" {
			t.Fatalf("round %d: markers after a pass that could not finish = %v", round, got)
		}
		// The run marker is not asserted here: the synthetic Connect bypasses the adapter's
		// production-attempt latch, which the default-deny tests cover.
	}
}
