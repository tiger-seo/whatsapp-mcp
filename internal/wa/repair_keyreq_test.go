package wa

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waServerSync"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
)

// These tests cover the pin's detached key request: when a decoded app-state patch names a key the
// device does not have, the pinned client starts `go requestMissingAppStateKeys(context.WithoutCancel(ctx), ...)`
// after the repair call has already failed. They run the real pinned client against the simulator,
// answer its full-resync request with a patch for an unknown key, and put a spy in front of the
// guarded key store to see the detached lookup. No network and no account are involved.

var unknownSyncKey = []byte{0xde, 0xad, 0xbe, 0xef, 0x01}

// keySpy records every key lookup the SDK makes and can hold the lookups that are detached from the
// repair caller.
type keySpy struct {
	wastore.AppStateSyncKeyStore

	mu       sync.Mutex
	calls    []keyCall
	hold     bool          // when set, a detached lookup waits for release
	entered  chan struct{} // closed when the first held lookup arrives
	release  chan struct{}
	enterOne sync.Once
	results  chan keyResult
}

type keyCall struct {
	detached bool // the context can never be cancelled: WithoutCancel
	err      error
}

type keyResult struct {
	ctxErr error
	err    error
	found  bool
}

func newKeySpy(inner wastore.AppStateSyncKeyStore) *keySpy {
	return &keySpy{
		AppStateSyncKeyStore: inner,
		entered:              make(chan struct{}),
		release:              make(chan struct{}),
		results:              make(chan keyResult, 8),
	}
}

func (k *keySpy) GetAppStateSyncKey(ctx context.Context, id []byte) (*wastore.AppStateSyncKey, error) {
	detached := ctx.Done() == nil
	k.mu.Lock()
	hold := k.hold && detached
	k.calls = append(k.calls, keyCall{detached: detached})
	k.mu.Unlock()
	if hold {
		k.enterOne.Do(func() { close(k.entered) })
		<-k.release
	}
	key, err := k.AppStateSyncKeyStore.GetAppStateSyncKey(ctx, id)
	if detached {
		select {
		case k.results <- keyResult{ctxErr: ctx.Err(), err: err, found: key != nil}:
		default:
		}
	}
	return key, err
}

func (k *keySpy) counts() (all, detached int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, c := range k.calls {
		all++
		if c.detached {
			detached++
		}
	}
	return
}

// syncServer answers app-state IQs with a patch for an unknown key and counts them.
type syncServer struct {
	mu    sync.Mutex
	syncs int
}

func newSyncServer(conn *wasim.Conn) *syncServer {
	s := &syncServer{}
	conn.SetIQHandler(func(n *waBinary.Node) *waBinary.Node {
		id, _ := n.Attrs["id"].(string)
		ns, _ := n.Attrs["xmlns"].(string)
		if ns != "w:sync:app:state" {
			return nil
		}
		s.mu.Lock()
		s.syncs++
		s.mu.Unlock()
		name := repairCollection
		if sync, ok := n.GetOptionalChildByTag("sync"); ok {
			if col, ok := sync.GetOptionalChildByTag("collection"); ok {
				if v, ok := col.Attrs["name"].(string); ok {
					name = v
				}
			}
		}
		raw, err := proto.Marshal(&waServerSync.SyncdPatch{
			Version:     &waServerSync.SyncdVersion{Version: proto.Uint64(1)},
			KeyID:       &waServerSync.KeyId{ID: unknownSyncKey},
			SnapshotMAC: make([]byte, 32),
			PatchMAC:    make([]byte, 32),
		})
		if err != nil {
			panic(err)
		}
		return &waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{
			"from": types.ServerJID, "id": id, "type": "result",
		}, Content: []waBinary.Node{{Tag: "sync", Content: []waBinary.Node{{
			Tag:   "collection",
			Attrs: waBinary.Attrs{"name": name},
			Content: []waBinary.Node{{Tag: "patches", Content: []waBinary.Node{{
				Tag: "patch", Content: raw,
			}}}},
		}}}}}
	})
	return s
}

func (s *syncServer) syncCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncs
}

// keyReqRig is a reopened session with a pending repair marker, connected to the simulator.
type keyReqRig struct {
	t     *testing.T
	sess  *Session
	spy   *keySpy
	srv   *syncServer
	conn  *wasim.Conn
	st    *store.Store
	dir   string
	log   *memLog
	close func()
}

func newKeyReqRig(t *testing.T) *keyReqRig {
	t.Helper()
	dir := waDataDir(t)
	seedInterruptedProgress(t, dir)

	st := openWADirAt(t, dir)
	sim := wasim.Start(t)
	events := make(chan SessionEvent, 64)
	life, stop := context.WithCancel(context.Background())
	cfg := newSessionTestConfig(st, life)
	logger, mem := newMemLog()
	cfg.Log = logger
	cfg.PreLoginHTTP = sim.Client()
	cfg.WebsocketHTTP = sim.Client()
	cfg.Dispatch = func(_ uint64, ev SessionEvent) { events <- ev }
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !s.HasIdentity() || !s.RecoveryPending() {
		t.Fatalf("seed: identity=%v pending=%v", s.HasIdentity(), s.RecoveryPending())
	}
	if err := st.ArmRunGuard(context.Background()); err != nil {
		t.Fatalf("arm: %v", err)
	}
	connectErr := make(chan error, 1)
	go func() { connectErr <- s.Connect(life) }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := sim.Accept(ctx)
	if err != nil {
		t.Fatalf("no connection: %v (failures %v)", err, sim.Failures())
	}
	if conn.ClientPayload.GetDevicePairingData() != nil {
		t.Fatal("the saved identity registered again")
	}
	srv := newSyncServer(conn)
	if err := conn.SendSuccess(ctx, types.NewJID(testOwnPhone, types.HiddenUserServer)); err != nil {
		t.Fatal(err)
	}
	if err := <-connectErr; err != nil {
		t.Fatalf("connect: %v", err)
	}
	for connected := false; !connected; {
		select {
		case ev := <-events:
			connected = ev == SessionConnected
		case <-ctx.Done():
			t.Fatal("never connected")
		}
	}
	spy := newKeySpy(s.dev.AppStateKeys)
	s.dev.AppStateKeys = spy
	r := &keyReqRig{t: t, sess: s, spy: spy, srv: srv, conn: conn, st: st, dir: dir, log: mem}
	r.close = func() {
		_ = s.Retire(context.Background())
		stop()
		conn.Close()
		_ = st.Close(context.Background())
	}
	t.Cleanup(r.close)
	return r
}

func (r *keyReqRig) pending() int {
	r.t.Helper()
	p, err := r.sess.barrier.PendingRepairs(context.Background())
	if err != nil {
		r.t.Fatalf("pending: %v", err)
	}
	return len(p)
}

func (r *keyReqRig) waitDetached(n int) {
	r.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(2 * time.Millisecond) {
		if _, d := r.spy.counts(); d >= n {
			return
		}
		if time.Now().After(deadline) {
			all, d := r.spy.counts()
			r.t.Fatalf("detached key lookups = %d (all %d), want %d", d, all, n)
		}
	}
}

// The detached key request outlives the repair caller: the repair fails on the missing key, the
// caller's context is cancelled, and the pinned client's goroutine still performs its key lookup
// with a context that can no longer be cancelled. The failed repair keeps its marker and makes no
// second full-resync call.
func TestKeyRequestIsDetachedFromRepairCaller(t *testing.T) {
	r := newKeyReqRig(t)
	r.spy.mu.Lock()
	r.spy.hold = true
	r.spy.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.sess.RunRepairs(ctx) }()

	// The detached lookup is in flight (held) while the caller still exists.
	select {
	case <-r.spy.entered:
	case <-time.After(10 * time.Second):
		t.Fatalf("no detached key lookup; syncs=%d", r.srv.syncCount())
	}
	// The caller ends: the repair failed on the missing key, and we also cancel its context.
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a repair over an unknown key reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the repair caller stayed blocked behind the detached lookup")
	}
	cancel()
	if ctx.Err() == nil {
		t.Fatal("caller context not cancelled")
	}

	// Let the held detached lookup proceed after the caller is gone and cancelled.
	close(r.spy.release)
	select {
	case res := <-r.spy.results:
		if res.ctxErr != nil {
			t.Errorf("the detached lookup saw a cancelled context: %v", res.ctxErr)
		}
		if res.err != nil {
			t.Errorf("the detached lookup failed while its generation was live: %v", res.err)
		}
		if res.found {
			t.Error("an unknown key was found")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the detached lookup never completed")
	}

	if n := r.srv.syncCount(); n != 1 {
		t.Errorf("app-state requests = %d, want exactly 1 (no extra full resync)", n)
	}
	if _, d := r.spy.counts(); d != 1 {
		t.Errorf("detached lookups = %d, want 1", d)
	}
	if !r.sess.RecoveryPending() || r.pending() != 1 {
		t.Error("the missing-key failure cleared the repair marker")
	}
}

// After the generation is retired the held detached lookup is refused by the guarded store, and
// nothing is written: the marker count is unchanged and no device row appears or disappears.
func TestKeyRequestAfterRetirementIsRefusedAndWritesNothing(t *testing.T) {
	r := newKeyReqRig(t)
	r.spy.mu.Lock()
	r.spy.hold = true
	r.spy.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- r.sess.RunRepairs(context.Background()) }()
	select {
	case <-r.spy.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no detached key lookup")
	}
	if err := <-done; err == nil {
		t.Fatal("repair reported success")
	}
	before := repairRowCount(t, r.dir)
	if before != 1 {
		t.Fatalf("seed markers = %d, want 1", before)
	}

	retireCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.sess.Retire(retireCtx); err != nil {
		t.Fatalf("retire: %v", err)
	}
	close(r.spy.release)
	select {
	case res := <-r.spy.results:
		if res.err == nil {
			t.Error("the guarded store served a detached lookup after retirement")
		}
		if res.found {
			t.Error("a key came back after retirement")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the detached lookup never returned")
	}
	// Read the marker table directly: the retired generation can no longer be asked.
	if got := repairRowCount(t, r.dir); got != before {
		t.Errorf("repair markers changed from %d to %d after the detached lookup", before, got)
	}
	if rows := deviceRows(t, r.dir); len(rows) != 1 {
		t.Errorf("device rows = %d, want 1", len(rows))
	}
}

func repairRowCount(t *testing.T, dir string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "whatsapp.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM device_repair").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// keyRequestsSent counts the pinned client's own "Sending key request" log line, which it writes
// just before it sends a request (the adapter keeps the template and drops the arguments).
func (r *keyReqRig) keyRequestsSent() int {
	r.log.mu.Lock()
	defer r.log.mu.Unlock()
	n := 0
	for _, l := range r.log.lines {
		if strings.Contains(l, "Sending key request for app state keys") {
			n++
		}
	}
	return n
}

// Key requests stay bounded: the first repair pass makes the pinned client request the unknown key
// once; a second pass in the same generation sends one more app-state request and one more detached
// lookup, but no second key request (the client's own 24-hour per-key limit), and no further full
// resync beyond the one the controller makes per pass.
func TestKeyRequestsStayBoundedAcrossRepeatedPasses(t *testing.T) {
	r := newKeyReqRig(t)
	if err := r.sess.RunRepairs(context.Background()); err == nil {
		t.Fatal("repair reported success")
	}
	r.waitDetached(1)
	for deadline := time.Now().Add(10 * time.Second); r.keyRequestsSent() == 0; time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the first pass never sent a key request; log: %q", r.log.lines)
		}
	}
	if n := r.keyRequestsSent(); n != 1 {
		t.Fatalf("key requests after the first pass = %d, want 1", n)
	}

	r.sess.repair.mu.Lock()
	r.sess.repair.attempted = map[string]bool{}
	r.sess.repair.mu.Unlock()
	if err := r.sess.RunRepairs(context.Background()); err == nil {
		t.Fatal("second repair reported success")
	}
	r.waitDetached(2)
	time.Sleep(300 * time.Millisecond)
	if n := r.srv.syncCount(); n != 2 {
		t.Errorf("app-state requests = %d, want one per pass", n)
	}
	if n := r.keyRequestsSent(); n != 1 {
		t.Errorf("key requests after the second pass = %d, want still 1", n)
	}
	if r.pending() != 1 {
		t.Error("the marker was cleared")
	}
}
