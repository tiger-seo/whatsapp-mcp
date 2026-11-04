package wa

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waAdv"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

// testOwnPhone is a synthetic JID user, never a real account.
const testOwnPhone = "490000000001"

// countingTransport denies every request and records the attempt, so tests prove that no network was
// touched instead of trusting that nothing was dialled.
type countingTransport struct {
	mu    sync.Mutex
	calls []string
}

func (t *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.calls = append(t.calls, r.URL.Host+r.URL.Path)
	t.mu.Unlock()
	return nil, errors.New("synthetic test transport refuses every request")
}

func (t *countingTransport) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.calls)
}

// stallTransport blocks until the request context ends, simulating a connection that hangs.
type stallTransport struct{}

func (stallTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func countingClient(t http.RoundTripper) *http.Client {
	return &http.Client{Transport: t}
}

// waDataDir creates a fresh private data directory, like a real run.
func waDataDir(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := private.MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return dir
}

// openWADir opens a store on a fresh private directory and closes it when the test ends.
func openWADir(t testing.TB) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), waDataDir(t), nil)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	return st
}

// bundledProvider returns a provider that never discovers anything.
func bundledProvider(ctx context.Context) *VersionProvider {
	return newVersionProvider(ctx, newBoundedHTTPClient(time.Second, 1<<20, forbiddenTransport{}), "https://example.invalid/")
}

// capture is a session factory that passes through to the real adapter but keeps the config and the
// concrete session for assertions and synthetic event dispatch.
type capture struct {
	cfg    SessionConfig
	handle SessionHandle
}

func (c *capture) factory() SessionFactory {
	return func(cfg SessionConfig) (SessionHandle, error) {
		s, err := OpenSession(cfg)
		if err != nil {
			return nil, err
		}
		c.cfg = cfg
		c.handle = s
		return s, nil
	}
}

// qrRecorder wraps a session handle and counts GetQRChannel calls, proving that the startup path
// never opens a pairing window.
type qrRecorder struct {
	SessionHandle
	qrCalls atomic.Int64
}

type payloadAtConnect struct {
	*Session
	observed chan uint32
}

func (s *payloadAtConnect) Connect(context.Context) error {
	s.observed <- s.client.GetClientPayload().UserAgent.AppVersion.GetTertiary()
	return errors.New("synthetic connection refusal")
}

func (n *qrRecorder) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	n.qrCalls.Add(1)
	return nil, errors.New("synthetic: pairing channel must not be opened")
}

// stallSessionFactory builds a factory whose sessions dial a stalling transport, so an opened
// connection attempt hangs until the watchdog cancels it.
func stallSessionFactory(c *capture, ws bool) SessionFactory {
	return func(cfg SessionConfig) (SessionHandle, error) {
		if ws {
			cfg.WebsocketHTTP = countingClient(&stallTransport{})
		}
		cfg.PreLoginHTTP = countingClient(&stallTransport{})
		s, err := OpenSession(cfg)
		if err != nil {
			return nil, err
		}
		c.cfg, c.handle = cfg, s
		return s, nil
	}
}

// transportSessionFactory builds a factory whose sessions dial the shared refused transport, so every
// connect attempt fails fast and is counted.
func refusedSessionFactory(c *capture, t *countingTransport) SessionFactory {
	return func(cfg SessionConfig) (SessionHandle, error) {
		cfg.PreLoginHTTP = countingClient(t)
		cfg.WebsocketHTTP = countingClient(t)
		s, err := OpenSession(cfg)
		if err != nil {
			return nil, err
		}
		c.cfg, c.handle = cfg, s
		return s, nil
	}
}

// armAndSave gives the fresh device a durable synthetic identity through the guarded save path, the
// same persistence a real pairing performs.
func armAndSave(t testing.TB, s *Session) {
	t.Helper()
	jid := types.NewJID(testOwnPhone, types.DefaultUserServer)
	s.dev.ID = &jid
	s.dev.PushName = "Test Account"
	s.dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("details"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := s.dev.Save(context.Background()); err != nil {
		t.Fatalf("device save: %v", err)
	}
}

func deviceRows(t *testing.T, dir string) [][]any {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "whatsapp.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT * FROM whatsmeow_device ORDER BY jid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func newSessionTestConfig(st *store.Store, lifetime context.Context) SessionConfig {
	return SessionConfig{
		Store:    st,
		Log:      slog.New(slog.DiscardHandler),
		Versions: bundledProvider(lifetime),
		Epoch:    1,
		Lifetime: lifetime,
		Dispatch: func(uint64, SessionEvent) {},
	}
}

func TestSessionOpensGuardedDeviceWithReviewedOptions(t *testing.T) {
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := OpenSession(newSessionTestConfig(st, lifetime))
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	c := s.client
	if !c.EnableAutoReconnect {
		t.Error("EnableAutoReconnect = false")
	}
	if c.InitialAutoReconnect {
		t.Error("InitialAutoReconnect = true")
	}
	if !c.DisableLoginAutoReconnect {
		t.Error("DisableLoginAutoReconnect = false")
	}
	if c.BackgroundEventCtx != lifetime {
		t.Error("BackgroundEventCtx is not the generation lifetime")
	}
	if c.GetClientPayload == nil {
		t.Error("version/hash callback missing")
	}
	if !c.ManualHistorySyncDownload {
		t.Error("automatic history blob transfer is enabled")
	}
	if c.UseRetryMessageStore {
		t.Error("retry message store enabled")
	}
	if c.EmitAppStateEventsOnFullSync {
		t.Error("full-sync event emission enabled")
	}
	if s.mediaClient == nil || s.mediaClient.Timeout != 10*time.Second {
		t.Errorf("app-state HTTP client timeout = %v", s.mediaClient.Timeout)
	}
	if _, ok := c.Log.(waLog.Logger); !ok {
		t.Errorf("log bridge type = %T", c.Log)
	}
	if s.epoch != 1 {
		t.Errorf("epoch = %d", s.epoch)
	}
	// Retire drains the guarded session without closing the shared store.
	if err := s.Retire(context.Background()); err != nil {
		t.Fatalf("retire: %v", err)
	}
}

func TestSessionVersionSelectedBeforeConnect(t *testing.T) {
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	vp := bundledProvider(lifetime)
	// The candidate is selected before Connect; the handshake callback must use it consistently.
	vp.selected.Store(&Version{2, 3000, 4242})
	selected := wastore.WAVersionContainer{2, 3000, 4242}
	cfg := newSessionTestConfig(st, lifetime)
	cfg.Versions = vp
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	payload := s.client.GetClientPayload()
	if payload.UserAgent.AppVersion == nil || payload.UserAgent.AppVersion.GetTertiary() != 4242 {
		t.Fatalf("app version = %v", payload.UserAgent.AppVersion)
	}
	if payload.DevicePairingData == nil {
		t.Fatal("fresh device payload lost registration data")
	}
	want := selected.Hash()
	if string(payload.DevicePairingData.BuildHash) != string(want[:]) {
		t.Fatalf("build hash = %x, want %x", payload.DevicePairingData.BuildHash, want)
	}
	if err := s.Retire(context.Background()); err != nil {
		t.Fatalf("retire: %v", err)
	}
	// Without a selection the bundled version is used.
	cfg2 := newSessionTestConfig(st, lifetime)
	s2, err := OpenSession(cfg2)
	if err != nil {
		t.Fatalf("open bundled session: %v", err)
	}
	payload = s2.client.GetClientPayload()
	if payload.UserAgent.AppVersion.GetTertiary() != wastore.GetWAVersion()[2] {
		t.Errorf("bundled app version = %v", payload.UserAgent.AppVersion)
	}
	bundledHash := wastore.GetWAVersion().Hash()
	if payload.DevicePairingData == nil || string(payload.DevicePairingData.BuildHash) != string(bundledHash[:]) {
		t.Errorf("bundled build hash mismatch: %v", payload.DevicePairingData)
	}
	if err := s2.Retire(context.Background()); err != nil {
		t.Fatalf("retire: %v", err)
	}
}

func TestOwnerSelectedVersionAtConnect(t *testing.T) {
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	seed, err := OpenSession(newSessionTestConfig(st, lifetime))
	if err != nil {
		t.Fatal(err)
	}
	armAndSave(t, seed)
	if err := seed.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	vp := bundledProvider(lifetime)
	vp.selected.Store(&Version{2, 3000, 4242})
	observed := make(chan uint32, 4)
	o := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: vp, Sessions: func(cfg SessionConfig) (SessionHandle, error) {
		s, err := OpenSession(cfg)
		if err != nil {
			return nil, err
		}
		return &payloadAtConnect{Session: s, observed: observed}, nil
	}})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := o.stopForTest(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	select {
	case got := <-observed:
		if got != 4242 {
			t.Fatalf("version at Connect = %d, want 4242", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner did not call Connect")
	}
}

func TestSessionPayloadCloneLeavesLibraryGlobalsUnchanged(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	vp := bundledProvider(lifetime)
	vp.selected.Store(&Version{2, 3000, 4242})
	cfg := newSessionTestConfig(st, lifetime)
	cfg.Versions = vp
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	before := wastore.GetWAVersion()
	p1 := s.client.GetClientPayload()
	p2 := s.client.GetClientPayload()
	if p1 == p2 {
		t.Fatal("handshake payloads share one object")
	}
	// The callback must not hand back library globals: mutating the returned payload must not
	// change the next one.
	primary := uint32(99)
	p1.UserAgent.AppVersion.Primary = &primary
	p2 = s.client.GetClientPayload()
	if p2.UserAgent.AppVersion.GetPrimary() == 99 {
		t.Fatal("the callback returned a shared global payload")
	}
	if wastore.GetWAVersion() != before {
		t.Fatalf("library version changed to %v", wastore.GetWAVersion())
	}
	base := wastore.BaseClientPayload
	if base.UserAgent.AppVersion.GetPrimary() == 99 {
		t.Fatal("BaseClientPayload was mutated")
	}
	if base.UserAgent.AppVersion.GetTertiary() != before[2] {
		t.Fatal("BaseClientPayload version drifted from the library version")
	}

	// A saved identity gets the login payload: patched app version, no registration data.
	armAndSave(t, s)
	if err := s.Retire(context.Background()); err != nil {
		t.Fatalf("retire: %v", err)
	}
	st.Close(context.Background())
	st2, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close(context.Background())
	lifetime2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	cfg2 := newSessionTestConfig(st2, lifetime2)
	cfg2.Versions = vp
	s2, err := OpenSession(cfg2)
	if err != nil {
		t.Fatalf("reopen session: %v", err)
	}
	payload := s2.client.GetClientPayload()
	if payload.DevicePairingData != nil {
		t.Error("login payload carried registration data")
	}
	if payload.UserAgent.AppVersion.GetTertiary() != 4242 {
		t.Errorf("login app version = %v", payload.UserAgent.AppVersion)
	}
}

func TestSessionSaveFailureKeepsPairingIncomplete(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := OpenSession(newSessionTestConfig(st, lifetime))
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	// A pairing that cannot durably save the identity must not be treated as paired: quiesce the
	// shared store first, so the guarded save fails, then attempt the save a real pairing performs.
	if err := s.Retire(context.Background()); err != nil {
		t.Fatalf("retire: %v", err)
	}
	st.Close(context.Background())
	jid := types.NewJID(testOwnPhone, types.DefaultUserServer)
	s.dev.ID = &jid
	s.dev.PushName = "Test Account"
	s.dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("d"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	err = s.dev.Save(context.Background())
	if err == nil {
		t.Fatal("save succeeded although the store is closed")
	}
	if !errors.Is(err, store.ErrClosed) {
		t.Errorf("save error = %v, want store.ErrClosed", err)
	}
	// Reopening finds no identity: the failed save must not have produced a device row.
	st2, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close(context.Background())
	dev2, barrier2, err := st2.LoadDevice(context.Background())
	if err != nil {
		t.Fatalf("LoadDevice: %v", err)
	}
	if dev2.ID != nil {
		t.Fatalf("a failed save produced the identity %v", *dev2.ID)
	}
	_ = barrier2.Retire(context.Background())
}

func TestSessionPairSuccessBeforeConnected(t *testing.T) {
	st := openWADir(t)
	var cap capture
	o := NewOwner(OwnerConfig{
		Log:      slog.New(slog.DiscardHandler),
		Store:    st,
		Versions: bundledProvider(context.Background()),
		Sessions: cap.factory(),
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	s := cap.handle.(*Session)
	status := o.Snapshot()
	if status.LoggedIn {
		t.Fatal("unpaired owner claims a saved identity")
	}
	armAndSave(t, s)
	cap.cfg.Dispatch(cap.cfg.Epoch, SessionPairSuccess)
	status = o.Snapshot()
	if !status.LoggedIn {
		t.Error("PairSuccess did not record the durable identity")
	}
	if status.Connected {
		t.Error("PairSuccess must not claim a completed connection")
	}
	if status.PairingRequired {
		t.Error("PairSuccess left pairing_required set")
	}
	if snap, ok := o.PairingSnapshot(); ok {
		t.Errorf("PairSuccess opened a window: %+v, %v", snap, ok)
	}
	cap.cfg.Dispatch(cap.cfg.Epoch, SessionConnected)
	status = o.Snapshot()
	if !status.Connected {
		t.Error("Connected event did not complete the status")
	}
	if err := o.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestSessionValidIdentityRestartsWithoutQR(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var cap capture
	o := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: bundledProvider(context.Background()), Sessions: cap.factory()})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	armAndSave(t, cap.handle.(*Session))
	if err := o.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	st.Close(context.Background())

	// Restart with the identity on disk: the saved session reconnects; no pairing window opens.
	st2, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close(context.Background())
	network := &countingTransport{}
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cap2 capture
	var rec *qrRecorder
	o2 := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st2, Versions: bundledProvider(lifetime), Sessions: func(cfg SessionConfig) (SessionHandle, error) {
		cfg.PreLoginHTTP = countingClient(network)
		cfg.WebsocketHTTP = countingClient(network)
		s, err := OpenSession(cfg)
		if err != nil {
			return nil, err
		}
		cap2.cfg, cap2.handle = cfg, s
		rec = &qrRecorder{SessionHandle: s}
		return rec, nil
	}})
	if err := o2.Start(context.Background()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	o2.waitStartupDoneForTest(t)
	status := o2.Snapshot()
	if !status.LoggedIn {
		t.Error("saved identity lost after failed restart")
	}
	if status.Connected {
		t.Error("status claims a completed connection")
	}
	if status.PairingRequired {
		t.Error("a saved session triggered pairing")
	}
	if _, ok := o2.PairingSnapshot(); ok {
		t.Error("a pairing window opened for a saved session")
	}
	if rec.qrCalls.Load() != 0 {
		t.Errorf("GetQRChannel called %d times for a saved session", rec.qrCalls.Load())
	}
	if network.count() == 0 {
		t.Error("the synthetic transport never saw the connect attempt")
	}
	if err := o2.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// The durable identity is preserved: one device row with the same JID.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "whatsapp.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var jid string
	if err := db.QueryRow("SELECT jid FROM whatsmeow_device").Scan(&jid); err != nil {
		t.Fatalf("device row: %v", err)
	}
	if !strings.HasPrefix(jid, testOwnPhone) {
		t.Errorf("device row = %q, want the saved JID", jid)
	}
	var rows int
	if err := db.QueryRow("SELECT count(*) FROM whatsmeow_device").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("device rows = %d, %v", rows, err)
	}
}

func TestSessionRefusesUnsafeStorageBeforeNetwork(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var cap capture
	o := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: bundledProvider(context.Background()), Sessions: cap.factory()})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	armAndSave(t, cap.handle.(*Session))
	if err := o.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	st.Close(context.Background())

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "whatsapp.db"))+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	blob := func(n int) string { return "x'" + strings.Repeat("00", n) + "'" }
	if _, err := db.Exec(`INSERT INTO whatsmeow_device
		(jid, registration_id, noise_key, identity_key, signed_pre_key, signed_pre_key_id, signed_pre_key_sig,
		 adv_key, adv_details, adv_account_sig, adv_account_sig_key, adv_device_sig)
		VALUES ('490000000002@s.whatsapp.net', 1, ` + blob(32) + `, ` + blob(32) + `, ` + blob(32) + `, 1, ` + blob(64) + `,
		 ` + blob(1) + `, ` + blob(1) + `, ` + blob(64) + `, ` + blob(32) + `, ` + blob(64) + `)`,
	); err != nil {
		t.Fatalf("second device row: %v", err)
	}
	db.Close()

	network := &countingTransport{}
	st2, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close(context.Background())
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := newSessionTestConfig(st2, lifetime)
	cfg.PreLoginHTTP = countingClient(network)
	cfg.WebsocketHTTP = countingClient(network)
	if _, err := OpenSession(cfg); err == nil {
		t.Fatal("unsafe storage was accepted")
	} else if !errors.Is(err, store.ErrUnsafeStorage) {
		t.Errorf("error = %v, want ErrUnsafeStorage", err)
	}
	if network.count() != 0 {
		t.Errorf("the refusal touched the network %d times", network.count())
	}
}

func TestSessionNetworkFailureKeepsCredentials(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var cap capture
	o := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: bundledProvider(context.Background()), Sessions: cap.factory()})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	armAndSave(t, cap.handle.(*Session))
	if err := o.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	st.Close(context.Background())
	before := deviceRows(t, dir)
	if len(before) != 1 {
		t.Fatalf("initial device rows = %d, want 1", len(before))
	}

	st2, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close(context.Background())
	network := &countingTransport{}
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cap2 capture
	o2 := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st2, Versions: bundledProvider(lifetime), Sessions: refusedSessionFactory(&cap2, network)})
	if err := o2.Start(context.Background()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	o2.waitStartupDoneForTest(t)
	status := o2.Snapshot()
	if !status.LoggedIn || status.Connected {
		t.Fatalf("status after network failure = %+v", status)
	}
	if network.count() == 0 {
		t.Error("no connect attempt was made")
	}
	if err := o2.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	st2.Close(context.Background())
	if after := deviceRows(t, dir); !reflect.DeepEqual(before, after) {
		t.Error("device row changed after failed connection")
	}
}

func TestSessionLateSaveAfterCloseDoesNotReplaceCredentials(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := OpenSession(newSessionTestConfig(st, lifetime))
	if err != nil {
		t.Fatal(err)
	}
	armAndSave(t, s)
	if err := s.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	st.Close(context.Background())
	before := deviceRows(t, dir)
	if len(before) != 1 {
		t.Fatalf("initial device rows = %d, want 1", len(before))
	}
	s.dev.PushName = "late synthetic replacement"
	if err := s.dev.Save(context.Background()); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("late save error = %v, want store.ErrClosed", err)
	}
	if after := deviceRows(t, dir); !reflect.DeepEqual(before, after) {
		t.Error("late save changed the persisted device row")
	}
}

func TestSessionPasskeyConfirmationFailsBeforeIQ(t *testing.T) {
	st := openWADir(t)
	network := &countingTransport{}
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := newSessionTestConfig(st, lifetime)
	cfg.PreLoginHTTP = countingClient(network)
	cfg.WebsocketHTTP = countingClient(network)
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	qrch, err := s.client.GetQRChannel(lifetime)
	if err != nil {
		t.Fatalf("GetQRChannel: %v", err)
	}
	// The SkipHandoffUX confirmation path must fail on an unarmed cache before any IQ: the
	// synthetic transport refuses every request, so a single attempt would be visible here.
	s.client.DangerousInternals().DispatchEvent(&events.PairPasskeyConfirmation{SkipHandoffUX: true, Code: "SYNT-HETIC"})
	select {
	case item := <-qrch:
		if item.Event != whatsmeow.QRChannelEventError {
			t.Fatalf("channel item = %+v", item)
		}
		if item.Error == nil || !strings.Contains(item.Error.Error(), "no passkey linking cache available") {
			t.Fatalf("error = %v", item.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no confirmation failure within 5s")
	}
	if network.count() != 0 {
		t.Errorf("the confirmation attempted %d network requests", network.count())
	}
	// The owner-side consumer must reject the same unsupported state safely in a live window.
}

// qrHybrid pairs the real pinned QR channel with a scripted connection, so window tests exercise
// the pinned channel handling and the owner's consumer without a network.
type qrHybrid struct {
	*Session
	connect func(context.Context) error
	ready   chan struct{}
	once    sync.Once
}

func (h *qrHybrid) Connect(ctx context.Context) error { return h.connect(ctx) }
func (h *qrHybrid) Disconnect()                       {}

func (h *qrHybrid) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	ch, err := h.Session.GetQRChannel(ctx)
	h.once.Do(func() { close(h.ready) })
	return ch, err
}

// hybridSessionFactory builds real sessions with denied transports and a scripted connect, so
// window tests exercise the pinned channel handling and the owner's consumer without a network.
func hybridSessionFactory(c *capture, connect func(context.Context) error, network *countingTransport) (*qrHybrid, SessionFactory) {
	h := &qrHybrid{connect: connect, ready: make(chan struct{})}
	return h, func(cfg SessionConfig) (SessionHandle, error) {
		if network != nil {
			cfg.PreLoginHTTP = countingClient(network)
			cfg.WebsocketHTTP = countingClient(network)
		}
		s, err := OpenSession(cfg)
		if err != nil {
			return nil, err
		}
		h.Session = s
		c.cfg, c.handle = cfg, s
		return h, nil
	}
}

func TestSessionWindowSafelyRejectsPasskeyPath(t *testing.T) {
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	network := &countingTransport{}
	var cap capture
	hybrid, factory := hybridSessionFactory(&cap, func(context.Context) error { return nil }, network)
	o := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: bundledProvider(lifetime), Sessions: factory})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	snap, err := o.RequestPairing(context.Background())
	if err != nil {
		t.Fatalf("RequestPairing: %v", err)
	}
	if snap.Result != PairingActive {
		t.Fatalf("result = %v", snap.Result)
	}
	// The unsupported passkey path must be rejected safely: the window fails, no confirmation is
	// sent and no credential material is kept.
	select {
	case <-hybrid.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the pairing channel was never acquired")
	}
	s := cap.handle.(*Session)
	s.client.DangerousInternals().DispatchEvent(&events.PairPasskeyRequest{PublicKey: &types.WebAuthnPublicKey{}})
	o.waitWindowResultForTest(t, PairingFailed)
	if status := o.Snapshot(); status.LoggedIn || status.PairingRequired {
		t.Errorf("status after passkey rejection = %+v", status)
	}
	if snap, ok := o.PairingSnapshot(); !ok || snap.Result != PairingFailed || snap.Code != "" {
		t.Errorf("pairing snapshot after rejection = %+v, %v; want failed with cleared code", snap, ok)
	}
	if network.count() != 0 {
		t.Errorf("the rejection touched the network %d times", network.count())
	}
	if err := o.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestSessionWindowDeliversCodeAndPairError(t *testing.T) {
	st := openWADir(t)
	network := &countingTransport{}
	var cap capture
	hybrid, factory := hybridSessionFactory(&cap, func(context.Context) error { return nil }, network)
	o := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: bundledProvider(context.Background()), Sessions: factory})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := o.RequestPairing(context.Background()); err != nil {
		t.Fatalf("RequestPairing: %v", err)
	}
	select {
	case <-hybrid.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the pairing channel was never acquired")
	}
	s := cap.handle.(*Session)
	s.client.DangerousInternals().DispatchEvent(&events.QR{Codes: []string{"synthetic-code-1"}})
	deadline := time.Now().Add(5 * time.Second)
	var code string
	for time.Now().Before(deadline) {
		if snap, ok := o.PairingSnapshot(); ok && snap.Code != "" {
			code = snap.Code
			break
		}
		time.Sleep(time.Millisecond)
	}
	if code != "synthetic-code-1" {
		t.Fatalf("code = %q", code)
	}
	if status := o.Snapshot(); !status.PairingRequired {
		t.Error("valid code did not set pairing_required")
	}
	// A PairError (e.g. a failed durable save) must fail the window and leave no identity.
	cap.cfg.Dispatch(cap.cfg.Epoch, SessionPairError)
	o.waitWindowResultForTest(t, PairingFailed)
	if snap, ok := o.PairingSnapshot(); !ok || snap.Result != PairingFailed || snap.Code != "" {
		t.Errorf("pairing snapshot after PairError = %+v, %v", snap, ok)
	}
	if status := o.Snapshot(); status.LoggedIn {
		t.Error("PairError left a saved identity in the status")
	}
	if err := o.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestSessionWindowRealPairSaveFailure(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	network := &countingTransport{}
	var cap capture
	hybrid, factory := hybridSessionFactory(&cap, func(context.Context) error { return nil }, network)
	o := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: bundledProvider(context.Background()), Sessions: factory})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := o.stopForTest(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if snap, err := o.RequestPairing(context.Background()); err != nil || snap.Result != PairingActive {
		t.Fatalf("RequestPairing = %+v, %v", snap, err)
	}
	select {
	case <-hybrid.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("pairing channel was not acquired")
	}
	s := cap.handle.(*Session)
	errorsSeen := make(chan error, 1)
	s.client.AddEventHandler(func(event any) {
		if e, ok := event.(*events.PairError); ok {
			errorsSeen <- e.Error
		}
	})
	// The trigger forces the real guarded device INSERT to fail, without closing the active store.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "whatsapp.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER synthetic_reject_pair BEFORE INSERT ON whatsmeow_device BEGIN SELECT RAISE(ABORT, 'synthetic save failure'); END`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

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
	node := &waBinary.Node{Attrs: waBinary.Attrs{"id": "synthetic", "t": "0"}, Content: []waBinary.Node{{Tag: "pair-success", Content: []waBinary.Node{
		{Tag: "device-identity", Content: identity},
		{Tag: "device", Attrs: waBinary.Attrs{"jid": jid, "lid": lid}},
	}}}}
	s.client.DangerousInternals().HandlePairSuccess(context.Background(), node)
	select {
	case err := <-errorsSeen:
		var dbErr *whatsmeow.PairDatabaseError
		if !errors.As(err, &dbErr) || !errors.Is(err, store.ErrIO) || !strings.Contains(err.Error(), "failed to save device store") {
			t.Fatalf("PairError = %v, want the injected device save failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no upstream PairError")
	}
	o.waitWindowResultForTest(t, PairingFailed)
	if snap, ok := o.PairingSnapshot(); !ok || snap.Result != PairingFailed || snap.Code != "" {
		t.Errorf("pairing snapshot = %+v, %v", snap, ok)
	}
	if status := o.Snapshot(); status.LoggedIn || status.Connected {
		t.Errorf("status after failed save = %+v", status)
	}
	if rows := deviceRows(t, dir); len(rows) != 0 {
		t.Errorf("failed save persisted %d device rows", len(rows))
	}
	if n := network.count(); n != 0 {
		t.Errorf("synthetic pairing attempted %d HTTP requests", n)
	}
}
