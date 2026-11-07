package wa

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
)

// simRig is the production adapter wired to the simulated WhatsApp server: the real pinned client,
// the guarded store and the owner-facing event stream, with no network and no account.
type simRig struct {
	t      *testing.T
	dir    string
	st     *store.Store
	srv    *wasim.Server
	phone  *wasim.Phone
	sess   *Session
	events chan SessionEvent
	life   context.Context
	stop   context.CancelFunc
}

func newSimRig(t *testing.T) *simRig {
	t.Helper()
	r := &simRig{t: t, dir: waDataDir(t), events: make(chan SessionEvent, 64)}
	var err error
	if r.st, err = store.Open(context.Background(), r.dir, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.st.Close(context.Background()) })
	r.srv = wasim.Start(t)
	jid := types.NewJID(testOwnPhone, types.DefaultUserServer)
	if r.phone, err = wasim.NewPhone(jid, types.NewJID(testOwnPhone, types.HiddenUserServer)); err != nil {
		t.Fatal(err)
	}
	r.life, r.stop = context.WithCancel(context.Background())
	t.Cleanup(r.stop)
	cfg := newSessionTestConfig(r.st, r.life)
	cfg.PreLoginHTTP = r.srv.Client()
	cfg.WebsocketHTTP = r.srv.Client()
	cfg.Dispatch = func(_ uint64, ev SessionEvent) { r.events <- ev }
	if r.sess, err = OpenSession(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.sess.Retire(context.Background()) })
	if err := r.st.ArmRunGuard(context.Background()); err != nil {
		t.Fatalf("arm: %v", err)
	}
	return r
}

func (r *simRig) expectEvent(want SessionEvent) {
	r.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-r.events:
			if ev == want {
				return
			}
		case <-deadline:
			r.t.Fatalf("event %v not seen", want)
		}
	}
}

// TestSimulatorPairsAndReconnectsToConnected proves the simulator is a usable model of the server
// side: a real pinned client dials it, completes the Noise handshake and certificate check, pairs a
// synthetic device from a scanned QR code through the guarded store, receives the post-pair 515,
// reconnects with the saved identity and reaches Connected. The candidate reconnect here is driven
// by the test; the owner still denies it.
func TestSimulatorPairsAndReconnectsToConnected(t *testing.T) {
	r := newSimRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	qr, err := r.sess.GetQRChannel(r.life)
	if err != nil {
		t.Fatal(err)
	}
	connectErr := make(chan error, 1)
	go func() { connectErr <- r.sess.Connect(r.life) }()
	conn, err := r.srv.Accept(ctx)
	if err != nil {
		t.Fatalf("no connection reached the simulator: %v (failures %v)", err, r.srv.Failures())
	}
	if conn.ClientPayload.GetDevicePairingData() == nil {
		t.Error("a fresh client did not send a registration payload")
	}
	if err := <-connectErr; err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := conn.SendPairDevice(ctx, "ref-1", "ref-2"); err != nil {
		t.Fatal(err)
	}
	var item whatsmeow.QRChannelItem
	select {
	case item = <-qr:
	case <-ctx.Done():
		t.Fatal("no QR code")
	}
	if item.Event != whatsmeow.QRChannelEventCode {
		t.Fatalf("QR event = %q", item.Event)
	}
	link, err := r.phone.Scan(item.Code)
	if err != nil || link.Ref != "ref-1" {
		t.Fatalf("scan = %+v, %v", link, err)
	}
	if err := conn.SendPairSuccess(ctx, r.phone, link); err != nil {
		t.Fatal(err)
	}
	r.expectEvent(SessionPairSuccess)
	if _, err := conn.Expect(ctx, wasim.IsPairConfirmation); err != nil {
		t.Fatalf("no pair confirmation from the client: %v", err)
	}
	if rows := deviceRows(t, r.dir); len(rows) != 1 {
		t.Fatalf("device rows after pairing = %d, want 1", len(rows))
	}
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	r.expectEvent(SessionManualLoginReconnect)

	// The candidate completion: drop the paired socket and connect once with the saved identity.
	r.sess.client.Disconnect()
	if err := r.sess.client.ConnectContext(r.life); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	conn2, err := r.srv.Accept(ctx)
	if err != nil {
		t.Fatalf("no second connection: %v (failures %v)", err, r.srv.Failures())
	}
	if conn2.ClientPayload.GetDevicePairingData() != nil || conn2.ClientPayload.GetUsername() == 0 {
		t.Error("the reconnect did not use the saved identity")
	}
	if err := conn2.SendSuccess(ctx, r.phone.LID); err != nil {
		t.Fatal(err)
	}
	r.expectEvent(SessionConnected)
	if got := r.srv.Handshakes(); got != 2 {
		t.Errorf("handshakes = %d, want 2", got)
	}
	if f := r.srv.Failures(); len(f) != 0 {
		t.Errorf("simulator failures: %v", f)
	}
}
