package wa

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

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

func newOwnerRig(t *testing.T) *ownerRig {
	t.Helper()
	return newOwnerRigWith(t, nil)
}

// newOwnerRigWith lets a test adjust the owner configuration before the owner starts.
func newOwnerRigWith(t *testing.T, adjust func(*OwnerConfig)) *ownerRig {
	t.Helper()
	r := &ownerRig{t: t, dir: waDataDir(t)}
	leftover := libraryGoroutinesNow()
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
	ownerCfg := OwnerConfig{
		Log:      logger,
		Store:    r.st,
		Versions: bundledProvider(context.Background()),
		Sessions: func(cfg SessionConfig) (SessionHandle, error) {
			cfg.PreLoginHTTP = r.srv.Client()
			cfg.WebsocketHTTP = r.srv.Client()
			s, err := OpenSession(cfg)
			if err != nil {
				return nil, err
			}
			s.observer = observerIgnoring(leftover)
			return s, nil
		},
	}
	if adjust != nil {
		adjust(&ownerCfg)
	}
	r.owner = NewOwner(ownerCfg)
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

// sessionLog records the real sessions the owner builds, in order, so a test can reach a retired generation.
type sessionLog struct {
	mu       sync.Mutex
	sessions []*Session
}

func (l *sessionLog) add(s *Session) {
	l.mu.Lock()
	l.sessions = append(l.sessions, s)
	l.mu.Unlock()
}

func (l *sessionLog) get(i int) *Session {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sessions[i]
}

func (l *sessionLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.sessions)
}

// newRecordedRig is newOwnerRigWith with every real session recorded.
func newRecordedRig(t *testing.T, adjust func(*OwnerConfig)) (*ownerRig, *sessionLog) {
	t.Helper()
	log := &sessionLog{}
	r := newOwnerRigWith(t, func(cfg *OwnerConfig) {
		inner := cfg.Sessions
		cfg.Sessions = func(sc SessionConfig) (SessionHandle, error) {
			h, err := inner(sc)
			if s, ok := h.(*Session); ok && err == nil {
				log.add(s)
			}
			return h, err
		}
		if adjust != nil {
			adjust(cfg)
		}
	})
	return r, log
}

func TestOwnerPostPairUsesAFreshClientAgainstSimulator(t *testing.T) {
	r, sessions := newRecordedRig(t, nil)
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
	payload := conn2.ClientPayload
	if payload.GetDevicePairingData() != nil {
		t.Error("the fresh client registered again instead of using the saved identity")
	}
	selected := r.owner.cfg.Versions.Current()
	if v := payload.GetUserAgent().GetAppVersion(); v.GetPrimary() != selected[0] || v.GetSecondary() != selected[1] || v.GetTertiary() != selected[2] {
		t.Errorf("the fresh client logged in with version %v, want the selected %v", v, selected)
	}
	if err := conn2.SendSuccess(ctx, r.phone.LID); err != nil {
		t.Fatal(err)
	}
	r.waitConnected(ctx)
	if sessions.count() != 2 || sessions.get(0) == sessions.get(1) || sessions.get(0).client == sessions.get(1).client {
		t.Fatalf("the pairing socket and the fresh connection share a client (%d sessions)", sessions.count())
	}
	if !sessions.get(0).isRetired() {
		t.Error("the pairing generation was not retired before the fresh connection")
	}
	if st := r.owner.Snapshot(); !st.LoggedIn || st.PairingRequired {
		t.Errorf("status = %+v", st)
	}
	time.Sleep(200 * time.Millisecond)
	if got := r.srv.Handshakes(); got != 2 {
		t.Errorf("handshakes = %d, want exactly 2 (pair + one fresh connection)", got)
	}
	if rows := deviceRows(t, r.dir); len(rows) != 1 {
		t.Errorf("device rows = %d, want 1", len(rows))
	}
	if f := r.srv.Failures(); len(f) != 0 {
		t.Errorf("simulator failures: %v", f)
	}
}

// Calls that the retired pairing generation still makes through the real client, its HTTP clients and its
// disconnect never reach the fresh connection.
func TestOwnerPostPairLateCallsFromThePairingGenerationNeverReachTheFreshConnection(t *testing.T) {
	r, sessions := newRecordedRig(t, nil)
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
	if err := conn2.SendSuccess(ctx, r.phone.LID); err != nil {
		t.Fatal(err)
	}
	r.waitConnected(ctx)
	// Let the fresh client's own start-up traffic settle, then take the baseline.
	for {
		quiet, qcancel := context.WithTimeout(ctx, 400*time.Millisecond)
		_, err := conn2.Recv(quiet)
		qcancel()
		if err != nil {
			break
		}
	}
	old := sessions.get(0)
	dials, handshakes := r.srv.Dials(), r.srv.Handshakes()
	refusedDials := old.fence.RefusedDials()

	// A presence, a receipt, a message: all fail inside the library because its socket is gone; none can write to
	// the fresh connection.
	_ = old.client.SendPresence(ctx, types.PresenceAvailable)
	_ = old.client.MarkRead(ctx, []types.MessageID{"LATE-1"}, time.Now(), r.phone.LID, r.phone.LID)
	_, _ = old.client.SendMessage(ctx, r.phone.LID, &waE2E.Message{Conversation: proto.String("late")})
	old.client.Disconnect() // a late disconnect from the pairing error tail or the QR emitter
	// Media HTTP of the retired generation is refused at the dial and counted.
	if resp, err := old.mediaClient.Get("https://127.0.0.1:1/late"); err == nil {
		resp.Body.Close()
		t.Error("the retired generation's media client reached the network")
	}
	if got := old.fence.RefusedDials(); got <= refusedDials {
		t.Errorf("refused dials = %d, was %d; the late HTTP call was not refused and counted", got, refusedDials)
	}

	quiet, qcancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer qcancel()
	if n, err := conn2.Recv(quiet); err == nil {
		t.Errorf("the fresh connection received a frame from the retired generation: %v", n.Tag)
	}
	select {
	case <-conn2.Done():
		t.Error("a late disconnect from the pairing generation closed the fresh connection")
	default:
	}
	if st := r.owner.Snapshot(); !st.Connected {
		t.Errorf("status = %+v; the fresh connection must stay up", st)
	}
	if got := r.srv.Dials(); got != dials {
		t.Errorf("dials = %d, was %d: a late call opened a connection", got, dials)
	}
	if got := r.srv.Handshakes(); got != handshakes {
		t.Errorf("handshakes = %d, was %d", got, handshakes)
	}
}

// The server closes the pairing socket right after the 515, before the owner has retired that generation: the
// command never sees a disconnected phase and still links.
func TestOwnerPostPairServerCloseAfterThe515StillLinks(t *testing.T) {
	r, _ := newRecordedRig(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var phases []Phase
	var pmu sync.Mutex
	unsubscribe := r.owner.Subscribe(func(ev Event) {
		pmu.Lock()
		phases = append(phases, ev.Phase)
		pmu.Unlock()
	})
	defer unsubscribe()
	conn := r.pairUntilConfirmed(ctx, true)
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	conn2, err := r.srv.Accept(ctx)
	if err != nil {
		t.Fatalf("no fresh connection after the close: %v", err)
	}
	if err := conn2.SendSuccess(ctx, r.phone.LID); err != nil {
		t.Fatal(err)
	}
	r.waitConnected(ctx)
	pmu.Lock()
	defer pmu.Unlock()
	seenConnecting := false
	for _, p := range phases {
		if p == PhaseConnecting {
			seenConnecting = true
		}
		if seenConnecting && p == PhaseDisconnected {
			t.Errorf("the owner reported a disconnected phase during a normal handover: %v", phases)
		}
	}
}

// A stop that lands between the fresh generation's creation and its Connect: nothing dials, every generation is
// retired, and the unresolved pairing keeps the marker unsafe.
func TestOwnerPostPairStopBeforeTheFreshConnectDialsNothing(t *testing.T) {
	var owner atomic.Pointer[connectionOwner]
	r, sessions := newRecordedRig(t, func(cfg *OwnerConfig) {
		cfg.afterPostPairPublish = func() { owner.Load().BeginStop() }
	})
	owner.Store(r.owner)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := r.pairUntilConfirmed(ctx, true)
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.owner.Done():
	case <-ctx.Done():
		t.Fatal("the owner did not finish stopping")
	}
	if got := r.srv.Dials(); got != 1 {
		t.Errorf("dials = %d, want 1: the stop began before the fresh connect", got)
	}
	if sessions.count() != 2 || !sessions.get(0).isRetired() || !sessions.get(1).isRetired() {
		t.Errorf("not every generation was retired (%d sessions)", sessions.count())
	}
	if r.owner.StopError() == nil {
		t.Error("an interrupted link reported a successful stop")
	}
	if f := r.srv.Failures(); len(f) != 0 {
		t.Errorf("simulator failures: %v", f)
	}
}

// After a fresh-client link, a stop that passes its checks leaves the marker clear.
func TestOwnerPostPairLinkThenStopClearsTheMarker(t *testing.T) {
	r, _ := newRecordedRig(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := r.pairUntilConfirmed(ctx, true)
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	conn2, err := r.srv.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn2.SendSuccess(ctx, r.phone.LID); err != nil {
		t.Fatal(err)
	}
	r.waitConnected(ctx)
	r.owner.BeginStop()
	<-r.owner.Done()
	if err := r.owner.StopError(); err != nil {
		t.Fatalf("StopError = %v\n%v", err, r.log.lines)
	}
	state, _, err := r.st.RunGuard(ctx)
	if err != nil || state != "clean" {
		t.Fatalf("marker = %q, %v; want clean", state, err)
	}
}

// A 515 that outruns the pairing work (here the guarded Save is held by a database write lock)
// must not start a Connect; the reconnect happens once, after PairSuccess.
func TestOwnerPostPairWaitsForHeldSave(t *testing.T) {
	r := newOwnerRig(t)
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
	// Wait until the client has handled the 515 while the Save is still blocked, instead of
	// assuming a delay was long enough.
	for deadline := time.Now().Add(10 * time.Second); !r.log.contains("Got 515 code") && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if !r.log.contains("Got 515 code") {
		t.Fatal("the client did not handle the 515")
	}
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
