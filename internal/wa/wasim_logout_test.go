package wa

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
)

// These tests send the server's device-removed stream error (401) to the real pinned client in
// the interleavings the connection-owner contract names. The pinned handler deletes the device
// store synchronously and dispatches LoggedOut from its own goroutine; the guarded store defers
// the delete and the owner completes it after the generation drains.

func (r *ownerRig) seedChat() {
	r.t.Helper()
	err := r.st.UpdateChats(context.Background(), []store.ChatUpdate{{JID: "490000000009@s.whatsapp.net", LastActivity: time.Now()}})
	if err != nil {
		r.t.Fatalf("seed chat: %v", err)
	}
}

func (r *ownerRig) chatCount() int {
	r.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(r.dir, "whatsapp.db"))+"?mode=ro")
	if err != nil {
		r.t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM chats").Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

// pairAndConnect links the synthetic device and returns the connection that reached Connected.
func (r *ownerRig) pairAndConnect(ctx context.Context) *wasim.Conn {
	r.t.Helper()
	conn := r.pairUntilConfirmed(ctx, true)
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		r.t.Fatal(err)
	}
	conn2, err := r.srv.Accept(ctx)
	if err != nil {
		r.t.Fatalf("no reconnect: %v", err)
	}
	if err := conn2.SendSuccess(ctx, r.phone.LID); err != nil {
		r.t.Fatal(err)
	}
	r.waitConnected(ctx)
	return conn2
}

func (r *ownerRig) waitLoggedOut(ctx context.Context) {
	r.t.Helper()
	for r.owner.Snapshot().LoggedIn {
		select {
		case <-ctx.Done():
			r.t.Fatalf("still logged in; status %+v", r.owner.Snapshot())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// stop drains the owner and returns its stop error.
func (r *ownerRig) stop(ctx context.Context) error {
	r.t.Helper()
	r.owner.BeginStop()
	select {
	case <-r.owner.Done():
	case <-ctx.Done():
		r.t.Fatal("the owner did not finish stopping")
	}
	return r.owner.StopError()
}

func (r *ownerRig) runGuardState() string {
	r.t.Helper()
	state, _, err := r.st.RunGuard(context.Background())
	if err != nil {
		r.t.Fatalf("run guard: %v", err)
	}
	return state
}

func TestSimulatorDeviceRemovedAfterConnected(t *testing.T) {
	r := newOwnerRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.seedChat()
	conn := r.pairAndConnect(ctx)
	dials := r.srv.Dials()
	if err := conn.SendDeviceRemoved(ctx); err != nil {
		t.Fatal(err)
	}
	r.waitLoggedOut(ctx)
	if st := r.owner.Snapshot(); st.Connected || st.LoggedIn || st.PairingRequired {
		t.Errorf("status after device removal = %+v; want logged out without an unsolicited pairing request", st)
	}
	_ = r.stop(ctx)
	if rows := deviceRows(t, r.dir); len(rows) != 0 {
		t.Errorf("identity rows after confirmed removal = %d, want 0", len(rows))
	}
	if r.chatCount() != 1 {
		t.Errorf("chat sentinel lost: %d chats", r.chatCount())
	}
	if state := r.runGuardState(); state != "clean" {
		t.Errorf("marker = %q; the fresh link resolved the pairing, the stop passed its checks and the revocation cleanup finished, so it is clean", state)
	}
	if got := r.srv.Dials(); got != dials {
		t.Errorf("dials = %d, want %d; removal must not trigger a Connect", got, dials)
	}
	if _, ok := r.owner.PairingSnapshot(); ok && r.owner.Snapshot().PairingRequired {
		t.Error("removal opened a pairing window")
	}
}

func TestSimulatorDeviceRemovedDuringPostPairConnect(t *testing.T) {
	r := newOwnerRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.seedChat()
	conn := r.pairUntilConfirmed(ctx, true)
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	conn2, err := r.srv.Accept(ctx) // the post-pair Connect is in flight and not yet confirmed
	if err != nil {
		t.Fatal(err)
	}
	if err := conn2.SendDeviceRemoved(ctx); err != nil {
		t.Fatal(err)
	}
	r.waitLoggedOut(ctx)
	time.Sleep(300 * time.Millisecond)
	if st := r.owner.Snapshot(); st.Connected {
		t.Errorf("status = %+v; a removed device must not report connected", st)
	}
	if got := r.srv.Dials(); got != 2 {
		t.Errorf("dials = %d, want 2 (pair + the one reconnect)", got)
	}
	_ = r.stop(ctx)
	if rows := deviceRows(t, r.dir); len(rows) != 0 {
		t.Errorf("identity rows = %d, want 0", len(rows))
	}
	if r.chatCount() != 1 {
		t.Error("chat sentinel lost")
	}
	if r.runGuardState() != "unsafe" {
		t.Error("marker must stay unsafe")
	}
}

// The 515 is pending on a held Save when the server removes the device: the request is dropped,
// the late Save is drained by retirement, and the revocation cleanup removes the identity it wrote.
func TestSimulatorDeviceRemovedWhilePostPairRequestPending(t *testing.T) {
	r := newOwnerRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.seedChat()
	if _, err := r.owner.RequestPairing(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := r.srv.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SendPairDevice(ctx, "ref-1"); err != nil {
		t.Fatal(err)
	}
	var code string
	for code == "" {
		if snap, ok := r.owner.PairingSnapshot(); ok {
			code = snap.Code
		}
		time.Sleep(5 * time.Millisecond)
	}
	link, err := r.phone.Scan(code)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(r.dir, "whatsapp.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hold, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	if _, err := hold.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_, _ = hold.ExecContext(ctx, "ROLLBACK")
		}
	}
	defer release()

	if err := conn.SendPairSuccess(ctx, r.phone, link); err != nil {
		t.Fatal(err)
	}
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	if err := conn.SendDeviceRemoved(ctx); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); !r.log.contains("Got device removed stream error") && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if !r.log.contains("Got device removed stream error") {
		t.Fatal("the client did not handle the removal while the Save was held")
	}
	release()
	time.Sleep(500 * time.Millisecond)
	if got := r.srv.Dials(); got != 1 {
		t.Errorf("dials = %d, want 1; neither the 515 nor the removal may cause a Connect", got)
	}
	if st := r.owner.Snapshot(); st.Connected {
		t.Errorf("status = %+v", st)
	}
	_ = r.stop(ctx)
	if rows := deviceRows(t, r.dir); len(rows) != 0 {
		t.Errorf("identity rows = %d; the identity written by the late Save must be removed", len(rows))
	}
	if r.chatCount() != 1 {
		t.Error("chat sentinel lost")
	}
	if r.runGuardState() != "unsafe" {
		t.Error("marker must stay unsafe")
	}
}

func TestSimulatorDeviceRemovedCleanupFailureKeepsIdentityAndBlocksWork(t *testing.T) {
	r := newOwnerRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.seedChat()
	conn := r.pairAndConnect(ctx)
	dials := r.srv.Dials()
	db, err := sql.Open("sqlite", filepath.Join(r.dir, "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_revocation BEFORE DELETE ON whatsmeow_device BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := conn.SendDeviceRemoved(ctx); err != nil {
		t.Fatal(err)
	}
	r.waitLoggedOut(ctx)
	time.Sleep(300 * time.Millisecond)
	if err := r.stop(ctx); err == nil {
		t.Error("a failed revocation cleanup stopped successfully")
	}
	if rows := deviceRows(t, r.dir); len(rows) != 1 {
		t.Errorf("identity rows = %d, want 1 when cleanup could not persist", len(rows))
	}
	if r.chatCount() != 1 {
		t.Error("chat sentinel lost")
	}
	if r.runGuardState() != "unsafe" {
		t.Error("marker must stay unsafe")
	}
	if got := r.srv.Dials(); got != dials {
		t.Errorf("dials = %d, want %d; a failed cleanup must block new work", got, dials)
	}
}

// A stop racing the removal has three correct outcomes. The removal is handled first: the cleanup deletes the identity
// and the marker is clean. The stop wins but leaves work undecided: the marker stays unsafe. The stop wins before the
// client reads the removal at all: nothing was latched, the identity is still there, the owner still reports a
// logged-in session and the marker is clean. What must never happen is a clean marker beside an identity that the
// owner knows the server removed.
func TestSimulatorDeviceRemovedRacingStop(t *testing.T) {
	r := newOwnerRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.seedChat()
	conn := r.pairAndConnect(ctx)
	dials := r.srv.Dials()
	if err := conn.SendDeviceRemoved(ctx); err != nil {
		t.Fatal(err)
	}
	r.owner.BeginStop()
	select {
	case <-r.owner.Done():
	case <-ctx.Done():
		t.Fatal("the owner did not finish stopping")
	}
	rows := len(deviceRows(t, r.dir))
	switch state := r.runGuardState(); {
	case state == "unsafe" && rows <= 1:
	case state == "clean" && rows == 0:
	case state == "clean" && rows == 1 && r.owner.Snapshot().LoggedIn:
	default:
		t.Errorf("marker = %q with %d identity rows, status %+v; want unsafe, clean with no identity, or clean with the removal never seen", state, rows, r.owner.Snapshot())
	}
	if r.chatCount() != 1 {
		t.Error("chat sentinel lost")
	}
	if got := r.srv.Dials(); got != dials {
		t.Errorf("dials = %d, want %d", got, dials)
	}
}
