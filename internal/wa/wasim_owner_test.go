package wa

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
)

// ownerRig runs the real owner, real adapter and guarded store against the simulated server.
type ownerRig struct {
	t     *testing.T
	dir   string
	st    *store.Store
	srv   *wasim.Server
	phone *wasim.Phone
	owner *connectionOwner
	log   *memLog
}

func newOwnerRig(t *testing.T, postPair bool) *ownerRig {
	t.Helper()
	r := &ownerRig{t: t, dir: waDataDir(t)}
	var err error
	if r.st, err = store.Open(context.Background(), r.dir, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.st.Close(context.Background()) })
	r.srv = wasim.Start(t)
	if r.phone, err = wasim.NewPhone(types.NewJID(testOwnPhone, types.DefaultUserServer), types.NewJID(testOwnPhone, types.HiddenUserServer)); err != nil {
		t.Fatal(err)
	}
	logger, mem := newMemLog()
	r.log = mem
	r.owner = NewOwner(OwnerConfig{
		Log:      logger,
		Store:    r.st,
		Versions: bundledProvider(context.Background()),
		Sessions: func(cfg SessionConfig) (SessionHandle, error) {
			cfg.PreLoginHTTP = r.srv.Client()
			cfg.WebsocketHTTP = r.srv.Client()
			return OpenSession(cfg)
		},
		PostPairReconnect: postPair,
	})
	if err := r.owner.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = r.owner.stopForTest(context.Background()) })
	return r
}

// pairUntilConfirmed opens a window, serves one QR code and confirms the link; it returns the
// first connection with the client's confirmation received.
func (r *ownerRig) pairUntilConfirmed(ctx context.Context, confirmFirst bool) *wasim.Conn {
	r.t.Helper()
	if _, err := r.owner.RequestPairing(ctx); err != nil {
		r.t.Fatalf("request pairing: %v", err)
	}
	conn, err := r.srv.Accept(ctx)
	if err != nil {
		r.t.Fatalf("no connection: %v (failures %v)", err, r.srv.Failures())
	}
	if err := conn.SendPairDevice(ctx, "ref-1"); err != nil {
		r.t.Fatal(err)
	}
	var code string
	for code == "" {
		if snap, ok := r.owner.PairingSnapshot(); ok {
			code = snap.Code
		}
		select {
		case <-ctx.Done():
			r.t.Fatal("no QR code in the pairing snapshot")
		case <-time.After(5 * time.Millisecond):
		}
	}
	link, err := r.phone.Scan(code)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := conn.SendPairSuccess(ctx, r.phone, link); err != nil {
		r.t.Fatal(err)
	}
	if confirmFirst {
		if _, err := conn.Expect(ctx, wasim.IsPairConfirmation); err != nil {
			r.t.Fatalf("no pair confirmation: %v", err)
		}
	}
	return conn
}

func (r *ownerRig) waitConnected(ctx context.Context) {
	r.t.Helper()
	for !r.owner.Snapshot().Connected {
		select {
		case <-ctx.Done():
			r.t.Fatalf("not connected; status %+v\n%v", r.owner.Snapshot(), r.log.lines)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestOwnerPostPairReconnectReachesConnectedAgainstSimulator(t *testing.T) {
	r := newOwnerRig(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := r.pairUntilConfirmed(ctx, true)
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	conn2, err := r.srv.Accept(ctx)
	if err != nil {
		t.Fatalf("no reconnect: %v", err)
	}
	if conn2.ClientPayload.GetDevicePairingData() != nil {
		t.Error("the reconnect registered again instead of using the saved identity")
	}
	if err := conn2.SendSuccess(ctx, r.phone.LID); err != nil {
		t.Fatal(err)
	}
	r.waitConnected(ctx)
	if st := r.owner.Snapshot(); !st.LoggedIn || st.PairingRequired {
		t.Errorf("status = %+v", st)
	}
	time.Sleep(200 * time.Millisecond)
	if got := r.srv.Handshakes(); got != 2 {
		t.Errorf("handshakes = %d, want exactly 2 (pair + one reconnect)", got)
	}
	if rows := deviceRows(t, r.dir); len(rows) != 1 {
		t.Errorf("device rows = %d, want 1", len(rows))
	}
	if f := r.srv.Failures(); len(f) != 0 {
		t.Errorf("simulator failures: %v", f)
	}
}

func TestOwnerDeniesPostPairReconnectByDefaultAgainstSimulator(t *testing.T) {
	r := newOwnerRig(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := r.pairUntilConfirmed(ctx, true)
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !r.log.contains("reconnect_denied") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !r.log.contains("reconnect_denied") {
		t.Fatalf("no limitation diagnostic:\n%v", r.log.lines)
	}
	time.Sleep(300 * time.Millisecond)
	if got := r.srv.Dials(); got != 1 {
		t.Errorf("dials = %d, want 1; the 515 must not cause a second Connect", got)
	}
	if st := r.owner.Snapshot(); st.Connected || !st.LoggedIn {
		t.Errorf("status = %+v; the identity is kept and the link is not connected", st)
	}
}

// A 515 that outruns the pairing work (here the guarded Save is held by a database write lock)
// must not start a Connect; the reconnect happens once, after PairSuccess.
func TestOwnerPostPairReconnectWaitsForHeldSave(t *testing.T) {
	r := newOwnerRig(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
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
	time.Sleep(300 * time.Millisecond) // the Save is blocked; the 515 has been delivered
	if got := r.srv.Dials(); got != 1 {
		t.Fatalf("dials while the Save was held = %d, want 1", got)
	}
	if len(deviceRows(t, r.dir)) != 0 {
		t.Fatal("device row exists while the Save is held")
	}
	release()

	conn2, err := r.srv.Accept(ctx)
	if err != nil {
		t.Fatalf("no reconnect after the Save completed: %v", err)
	}
	if err := conn2.SendSuccess(ctx, r.phone.LID); err != nil {
		t.Fatal(err)
	}
	r.waitConnected(ctx)
	time.Sleep(200 * time.Millisecond)
	if got := r.srv.Handshakes(); got != 2 {
		t.Errorf("handshakes = %d, want 2", got)
	}
	if rows := deviceRows(t, r.dir); len(rows) != 1 {
		t.Errorf("device rows = %d, want 1", len(rows))
	}
}
