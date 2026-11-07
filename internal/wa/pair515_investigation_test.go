package wa

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// syntheticPairSuccessNode builds a pair-success node that passes the pinned HMAC and account
// signature checks for the session's synthetic keys.
func syntheticPairSuccessNode(t *testing.T, s *Session) *waBinary.Node {
	t.Helper()
	account := ecc.CreateKeyPair([]byte("synthetic account signing key"))
	details, err := proto.Marshal(&waAdv.ADVDeviceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	message := append(append(append([]byte{}, whatsmeow.AdvAccountSignaturePrefix...), details...), s.dev.IdentityKey.Pub[:]...)
	sig := ecc.CalculateSignature(account.PrivateKey(), message)
	pub := account.PublicKey().PublicKey()
	signed, err := proto.Marshal(&waAdv.ADVSignedDeviceIdentity{Details: details, AccountSignatureKey: pub[:], AccountSignature: sig[:]})
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, s.dev.AdvSecretKey)
	mac.Write(signed)
	identity, err := proto.Marshal(&waAdv.ADVSignedDeviceIdentityHMAC{Details: signed, HMAC: mac.Sum(nil)})
	if err != nil {
		t.Fatal(err)
	}
	jid := types.NewJID(testOwnPhone, types.DefaultUserServer)
	lid := types.NewJID("490000000001", types.HiddenUserServer)
	return &waBinary.Node{Attrs: waBinary.Attrs{"id": "synthetic", "t": "0"}, Content: []waBinary.Node{{Tag: "pair-success", Content: []waBinary.Node{
		{Tag: "device-identity", Content: identity},
		{Tag: "device", Attrs: waBinary.Attrs{"jid": jid, "lid": lid}},
	}}}}
}

// TestInvestigation515PinnedPairSuccessIsDetached records what the pinned pair-success handler
// does relative to the 515 stream error and Disconnect. It is evidence for the 515 proof gate, not
// a product behaviour test: the pinned handler returns before its pairing work finishes, so a 515
// can reach the owner while that work is still pending, and Disconnect does not join it.
//
// The guarded device Save is held by an external SQLite write lock released by the test.
func TestInvestigation515PinnedPairSuccessIsDetached(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())

	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan SessionEvent, 16)
	network := &countingTransport{}
	cfg := newSessionTestConfig(st, lifetime)
	cfg.PreLoginHTTP = countingClient(network)
	cfg.WebsocketHTTP = countingClient(network)
	cfg.Dispatch = func(_ uint64, ev SessionEvent) { seen <- ev }
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Retire(context.Background())
	if err := st.ArmRunGuard(context.Background()); err != nil {
		t.Fatalf("arm: %v", err)
	}

	// Hold the database write lock from a second connection so the handler's Save waits.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "whatsapp.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}
	defer release()

	node := syntheticPairSuccessNode(t, s)
	returned := make(chan struct{})
	go func() {
		s.client.DangerousInternals().HandlePairSuccess(context.Background(), node)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("the pinned pair-success handler waited for its pairing work")
	}
	t.Log("observation 1: HandlePairSuccess returned while the device Save is still blocked (pairing work is detached)")

	// The 515 stream error is a separate queue node and is handled while that work is pending.
	s.client.DangerousInternals().HandleStreamError(context.Background(), &waBinary.Node{
		Tag: "stream:error", Attrs: waBinary.Attrs{"code": "515"},
	})
	select {
	case ev := <-seen:
		if ev != SessionManualLoginReconnect {
			t.Fatalf("first event = %v, want the 515 ManualLoginReconnect", ev)
		}
		t.Log("observation 2: the owner receives ManualLoginReconnect before PairSuccess; the identity is not durable yet")
	case <-time.After(2 * time.Second):
		t.Fatal("no ManualLoginReconnect event")
	}
	if len(deviceRows(t, dir)) != 0 {
		t.Fatal("device row exists while Save is blocked")
	}

	// Disconnect returns without joining the detached pairing work.
	s.client.Disconnect()
	if len(deviceRows(t, dir)) != 0 {
		t.Fatal("device row exists while Save is blocked")
	}
	t.Log("observation 3: Disconnect returned while the detached pairing work is still pending")

	release()
	var late []SessionEvent
	deadline := time.After(10 * time.Second)
	for len(late) < 1 {
		select {
		case ev := <-seen:
			late = append(late, ev)
		case <-deadline:
			t.Fatal("the detached pairing work never finished")
		}
	}
	// With no socket, the final confirmation send fails, which exercises the handler's error tail:
	// a late device delete, Disconnect and PairError on the same client after the 515 and Disconnect.
	t.Logf("observation 4: after the earlier Disconnect, the detached work ran to completion and dispatched event %d (PairError=%d)", late[0], SessionPairError)
	if late[0] != SessionPairError {
		t.Errorf("late event = %v, want PairError from the unconnected confirmation send", late[0])
	}
	t.Logf("observation 5: device rows after the late tail = %d (the tail's Store.Delete is deferred by the guarded container, not applied to the live object)", len(deviceRows(t, dir)))
}
