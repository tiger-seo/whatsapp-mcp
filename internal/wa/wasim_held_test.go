package wa

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
)

// heldPairing is a pairing window whose pair-success has reached the real client while the guarded
// device Save is blocked by a database write lock the test controls.
type heldPairing struct {
	conn    *wasim.Conn
	link    wasim.Link
	release func()
}

// holdPairingSave opens a window, serves a QR code, takes the database write lock and sends the
// pair-success, so the client's detached pairing work waits on the Save.
func (r *ownerRig) holdPairingSave(ctx context.Context) *heldPairing {
	r.t.Helper()
	if _, err := r.owner.RequestPairing(ctx); err != nil {
		r.t.Fatal(err)
	}
	conn, err := r.srv.Accept(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := conn.SendPairDevice(ctx, "ref-1"); err != nil {
		r.t.Fatal(err)
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
		r.t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(r.dir, "whatsapp.db")))
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { db.Close() })
	hold, err := db.Conn(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := hold.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		r.t.Fatal(err)
	}
	h := &heldPairing{conn: conn, link: link}
	released := false
	h.release = func() {
		if !released {
			released = true
			_, _ = hold.ExecContext(context.Background(), "ROLLBACK")
			hold.Close()
		}
	}
	r.t.Cleanup(h.release)
	if err := conn.SendPairSuccess(ctx, r.phone, link); err != nil {
		r.t.Fatal(err)
	}
	return h
}

func (r *ownerRig) waitLog(substr string) {
	r.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !r.log.contains(substr) && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if !r.log.contains(substr) {
		r.t.Fatalf("the client never logged %q", substr)
	}
}

// Stopping while the pairing Save is held by a database lock must not wait for that lock: the
// generation's lifetime cancels the Save, the owner finishes only after its admitted calls have
// drained (shown by the store closing cleanly afterwards), no Connect follows the pending 515, and
// no identity is written for a link the phone may already hold.
func TestSimulatorStopWhileSaveHeldDrainsWithoutIdentity(t *testing.T) {
	r := newOwnerRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	r.seedChat()
	h := r.holdPairingSave(ctx)
	if err := h.conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	r.waitLog("Got 515 code")
	r.owner.BeginStop()
	select {
	case <-r.owner.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("stop waited for the database lock held by the test")
	}
	if err := r.st.Close(ctx); err != nil {
		t.Errorf("store close after the owner finished = %v; admitted work must have drained", err)
	}
	h.release()
	time.Sleep(300 * time.Millisecond)
	if got := r.srv.Dials(); got != 1 {
		t.Errorf("dials = %d, want 1; the pending 515 must not cause a Connect after stop", got)
	}
	if err := r.owner.StopError(); err == nil {
		t.Error("an unresolved pairing stopped successfully")
	}
	if rows := deviceRows(t, r.dir); len(rows) != 0 {
		t.Errorf("identity rows = %d; the cancelled Save must not persist an identity", len(rows))
	}
	if r.chatCount() != 1 {
		t.Error("chat sentinel lost")
	}
}

// Cancelling the window while the Save is held leaves the generation to drain; the pending 515 is
// never turned into a Connect, even after the lock is released.
func TestSimulatorCancelWindowWhileSaveHeldMakesNoConnect(t *testing.T) {
	r := newOwnerRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	r.seedChat()
	h := r.holdPairingSave(ctx)
	if err := h.conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	r.waitLog("Got 515 code")
	r.owner.CancelPairing()
	if snap, ok := r.owner.PairingSnapshot(); !ok || snap.Result != PairingCancelled {
		t.Errorf("window = %+v, %v; want cancelled", snap, ok)
	}
	h.release()
	time.Sleep(500 * time.Millisecond)
	if got := r.srv.Dials(); got != 1 {
		t.Errorf("dials = %d, want 1; a cancelled window must not reconnect", got)
	}
	if st := r.owner.Snapshot(); st.Connected {
		t.Errorf("status = %+v", st)
	}
	_ = r.stop(ctx)
	if state := r.runGuardState(); state != "unsafe" {
		t.Errorf("marker = %q; a cancelled pairing is unresolved and must stay unsafe", state)
	}
	if r.chatCount() != 1 {
		t.Error("chat sentinel lost")
	}
}

// A pinned handler of the pairing socket that is writing when the stop signal arrives finishes its write: the
// stop signal reaches only the program's own work, not the context the socket's handlers run with. The Save is
// still waiting on the database lock when the stop begins, and completes once the lock is released.
func TestSimulatorStopSignalLetsTheHeldPairingSaveFinish(t *testing.T) {
	trace := &stepLog{}
	r := newOwnerRigWith(t, func(cfg *OwnerConfig) { cfg.trace = trace.add })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	r.seedChat()
	h := r.holdPairingSave(ctx)
	if err := h.conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	r.waitLog("Got 515 code")
	r.owner.BeginStop()
	select {
	case <-r.owner.Done():
		t.Fatal("stop finished while the pairing Save was still waiting on the lock")
	case <-time.After(300 * time.Millisecond):
	}
	h.release()
	select {
	case <-r.owner.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("stop did not finish after the lock was released")
	}
	if rows := deviceRows(t, r.dir); len(rows) != 1 {
		t.Errorf("identity rows = %d; the Save that was running when the stop began must complete", len(rows))
	}
	if r.log.contains("owner_drain_failed") {
		t.Error("the barrier took the forced path although the write could finish")
	}
	if err := r.owner.StopError(); err == nil {
		t.Error("an unresolved pairing stopped successfully")
	}
	if err := checkRetirementOrder(trace.get()); err != nil {
		t.Errorf("order %v: %v", trace.get(), err)
	}
}

// A drain that does not finish inside its bound takes the forced path: the handlers' context is released, the
// held write fails, and the stop is unsafe.
func TestSimulatorHeldSaveBeyondTheDrainBoundForcesTheStop(t *testing.T) {
	r := newOwnerRigWith(t, func(cfg *OwnerConfig) { cfg.drainBound = 300 * time.Millisecond })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	r.seedChat()
	h := r.holdPairingSave(ctx)
	if err := h.conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	r.waitLog("Got 515 code")
	r.owner.BeginStop()
	select {
	case <-r.owner.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("the forced path did not end the stop")
	}
	h.release()
	if !r.log.contains("owner_drain_failed") {
		t.Errorf("the forced path was not reported: %v", r.log.lines)
	}
	if rows := deviceRows(t, r.dir); len(rows) != 0 {
		t.Errorf("identity rows = %d; the cancelled Save must not persist an identity", len(rows))
	}
	if err := r.owner.StopError(); err == nil {
		t.Error("a forced stop reported success")
	}
}
