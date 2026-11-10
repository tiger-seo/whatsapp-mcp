package wa

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types/events"
)

func TestRunGuardLatePinnedLogoutAfterRetirement(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	cap := &capture{}
	factory := cap.factory()
	var late func()
	o := NewOwner(OwnerConfig{Store: st, Versions: bundledProvider(context.Background()),
		// The pinned logout handler, delayed before its logout branch, runs after the barrier drained and before the
		// quiet-stop observation has finished: its delete is refused and its event dropped.
		trace: func(step string) {
			if step == "lib_cancelled" && late != nil {
				late()
			}
		},
		Sessions: func(cfg SessionConfig) (SessionHandle, error) {
			cfg.PreLoginHTTP = countingClient(forbiddenTransport{})
			cfg.WebsocketHTTP = countingClient(forbiddenTransport{})
			return factory(cfg)
		}})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := cap.handle.(*Session)
	armAndSave(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = s.Connect(ctx)
	late = func() {
		s.client.DangerousInternals().HandleConnectFailure(context.Background(), &waBinary.Node{Attrs: waBinary.Attrs{"reason": "401"}})
	}
	o.BeginStop()
	<-o.Done()
	if err := s.dev.Delete(context.Background()); !errors.Is(err, store.ErrRetired) {
		t.Fatalf("late delete = %v", err)
	}
	if o.StopError() == nil {
		t.Error("a late pinned logout during the check reported a successful stop")
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" || len(deviceRows(t, dir)) != 1 {
		t.Fatalf("reopened marker = %q, %v", state, err)
	}
}

type faultDuringDrainSession struct {
	*fakeSession
	entered chan struct{}
	release chan struct{}
}

func (s *faultDuringDrainSession) Retire(context.Context) error {
	close(s.entered)
	<-s.release
	return nil
}

func TestRunGuardStorageFaultDuringDrain(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &faultDuringDrainSession{fakeSession: &fakeSession{faultCh: make(chan struct{})}, entered: make(chan struct{}), release: make(chan struct{})}
	o := NewOwner(OwnerConfig{Store: st, Versions: bundledProvider(context.Background()), Sessions: func(cfg SessionConfig) (SessionHandle, error) {
		s.cfg = cfg
		return s, nil
	}})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	o.BeginStop()
	<-s.entered
	close(s.faultCh)
	close(s.release)
	<-o.Done()
	if o.StopError() == nil || !o.Health().StorageFault {
		t.Errorf("stop = %v, health = %+v", o.StopError(), o.Health())
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" {
		t.Fatalf("reopened marker = %q, %v", state, err)
	}
}

func TestRunGuardUnsafeBlocksClientConstruction(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	db, err := sql.Open("sqlite", filepath.Join(dir, "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS unsafe_run (id INTEGER PRIMARY KEY, state TEXT NOT NULL, repair_bits INTEGER NOT NULL);
		INSERT INTO unsafe_run (id, state, repair_bits) VALUES (1, 'unsafe', 0)
		ON CONFLICT(id) DO UPDATE SET state='unsafe'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var created atomic.Int64
	o := NewOwner(OwnerConfig{
		Store: st, Versions: bundledProvider(context.Background()),
		Sessions: fakeOwnerFactory(func(SessionConfig) *fakeSession {
			created.Add(1)
			return &fakeSession{}
		}),
	})
	err = o.Start(context.Background())
	o.BeginStop()
	<-o.Done()
	if err == nil {
		t.Error("unsafe marker did not refuse owner startup")
	}
	if got := created.Load(); got != 0 {
		t.Errorf("created %d account-capable sessions with unsafe marker", got)
	}
}

func TestRunGuardConfirmedLogoutCleanup(t *testing.T) {
	for _, deleteFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "event-first", true: "delete-first"}[deleteFirst], func(t *testing.T) {
			dir := waDataDir(t)
			st, err := store.Open(context.Background(), dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close(context.Background()) })
			cap := &capture{}
			o := NewOwner(OwnerConfig{Store: st, Versions: bundledProvider(context.Background()), Sessions: cap.factory()})
			if err := o.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			s := cap.handle.(*Session)
			armAndSave(t, s)
			if err := s.dev.AppState.PutAppStateVersion(context.Background(), "regular", 1, [128]byte{}); err != nil {
				t.Fatal(err)
			}
			if deleteFirst {
				if err := s.dev.Delete(context.Background()); !errors.Is(err, store.ErrDeleteDeferred) {
					t.Fatalf("delete: %v", err)
				}
			}
			s.handleEvent(&events.LoggedOut{})
			o.BeginStop()
			<-o.Done()
			state, bits, err := st.RunGuard(context.Background())
			if err != nil || state != "clean" || bits != 1<<3 || o.StopError() != nil {
				t.Fatalf("state = %q, bits %d, %v; stop = %v", state, bits, err, o.StopError())
			}
			if rows := deviceRows(t, dir); len(rows) != 0 {
				t.Fatalf("identity remains: %v", rows)
			}
		})
	}
}

func TestRunGuardLogoutCleanupFailureRetainsIdentity(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	cap := &capture{}
	o := NewOwner(OwnerConfig{Store: st, Versions: bundledProvider(context.Background()), Sessions: cap.factory()})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := cap.handle.(*Session)
	armAndSave(t, s)
	db, err := sql.Open("sqlite", filepath.Join(dir, "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_revocation BEFORE DELETE ON whatsmeow_device BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s.handleEvent(&events.LoggedOut{})
	o.BeginStop()
	<-o.Done()
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" || o.StopError() == nil {
		t.Fatalf("state %q, %v; stop %v", state, err, o.StopError())
	}
	if rows := deviceRows(t, dir); len(rows) != 1 {
		t.Fatalf("identity rows = %v", rows)
	}
}

func TestRunGuardUnconfirmedDeleteRetainsIdentity(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	cap := &capture{}
	o := NewOwner(OwnerConfig{Store: st, Versions: bundledProvider(context.Background()), Sessions: cap.factory()})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := cap.handle.(*Session)
	armAndSave(t, s)
	if err := s.dev.Delete(context.Background()); !errors.Is(err, store.ErrDeleteDeferred) {
		t.Fatal(err)
	}
	o.BeginStop()
	<-o.Done()
	if !errors.Is(o.StopError(), store.ErrDeleteDeferred) {
		t.Fatalf("stop = %v", o.StopError())
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" || len(deviceRows(t, dir)) != 1 {
		t.Fatalf("reopened state %q, %v", state, err)
	}
}

func TestRunGuardLogoutCallbackDrainsBeforeClear(t *testing.T) {
	dir := waDataDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	cap := &capture{}
	o := NewOwner(OwnerConfig{Store: st, Versions: bundledProvider(context.Background()), Sessions: cap.factory()})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := cap.handle.(*Session)
	armAndSave(t, s)
	o.mu.Lock()
	callbackDone := make(chan struct{})
	go func() { s.handleEvent(&events.LoggedOut{}); close(callbackDone) }()
	deadline := time.Now().Add(5 * time.Second)
	for s.callbackState.Load()&^(uint64(1)<<63) == 0 {
		if time.Now().After(deadline) {
			o.mu.Unlock()
			t.Fatal("callback not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	go o.BeginStop()
	o.mu.Unlock()
	<-callbackDone
	<-o.Done()
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "clean" || o.StopError() != nil || len(deviceRows(t, dir)) != 0 {
		t.Fatalf("state %q, %v; stop %v", state, err, o.StopError())
	}
}

func TestRunGuardOwnerLifecycle(t *testing.T) {
	st, err := store.Open(context.Background(), waDataDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	o := NewOwner(OwnerConfig{Store: st, Versions: bundledProvider(context.Background()), Sessions: fakeOwnerFactory(func(SessionConfig) *fakeSession { return &fakeSession{} })})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" {
		t.Fatalf("running marker = %q, %v", state, err)
	}
	o.BeginStop()
	<-o.Done()
	state, _, err = st.RunGuard(context.Background())
	if err != nil || state != "clean" {
		t.Fatalf("stopped marker = %q, %v", state, err)
	}
}

func TestRunGuardUnresolvedPairingBlocksRetryAndClear(t *testing.T) {
	st, err := store.Open(context.Background(), waDataDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	o := NewOwner(OwnerConfig{Store: st, Versions: bundledProvider(context.Background()), Sessions: fakeOwnerFactory(func(SessionConfig) *fakeSession { return &fakeSession{} })})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := o.RequestPairing(context.Background()); err != nil {
		t.Fatal(err)
	}
	o.mu.Lock()
	w := o.pairing
	o.mu.Unlock()
	<-w.done
	if _, err := o.RequestPairing(context.Background()); !errors.Is(err, store.ErrUnsafeRun) {
		t.Errorf("retry = %v", err)
	}
	o.BeginStop()
	<-o.Done()
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" {
		t.Fatalf("unresolved marker = %q, %v", state, err)
	}
}

func TestRunGuardLogoutRetainsUnsafeWithoutDurableCleanup(t *testing.T) {
	st, err := store.Open(context.Background(), waDataDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	var sess *fakeSession
	o := NewOwner(OwnerConfig{Store: st, Versions: bundledProvider(context.Background()), Sessions: fakeOwnerFactory(func(SessionConfig) *fakeSession {
		sess = &fakeSession{}
		return sess
	})})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	sess.send(SessionLoggedOut)
	o.BeginStop()
	<-o.Done()
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" || !errors.Is(o.StopError(), store.ErrUnsafeRun) {
		t.Fatalf("logout marker = %q, %v; stop = %v", state, err, o.StopError())
	}
}
