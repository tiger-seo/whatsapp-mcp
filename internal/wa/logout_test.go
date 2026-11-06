package wa

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
)

// revocableSession is a QR-capable session that also implements the guarded store's revocation
// cleanup, so owner-level tests can observe and fail it without a database.
type revocableSession struct {
	qrSession
	mu            sync.Mutex
	finishCalls   []bool
	finishErr     error
	retireEntered chan struct{}
	retireRelease chan struct{}
	live          *atomic.Int64
	maxLive       *atomic.Int64
}

func (s *revocableSession) FinishRevocation(_ context.Context, confirmed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishCalls = append(s.finishCalls, confirmed)
	return s.finishErr
}

func (s *revocableSession) Retire(ctx context.Context) error {
	if s.retireEntered != nil {
		close(s.retireEntered)
		<-s.retireRelease
	}
	s.live.Add(-1)
	return s.qrSession.Retire(ctx)
}

// revocationRig builds an owner whose generations are revocableSessions and counts how many are
// live at once. The first session starts with a saved identity.
type revocationRig struct {
	ot       *ownerTest
	sessions []*revocableSession
	live     atomic.Int64
	maxLive  atomic.Int64
	mu       sync.Mutex
	prepare  func(i int, s *revocableSession)
}

func newRevocationRig(t *testing.T) *revocationRig {
	t.Helper()
	r := &revocationRig{ot: newOwnerTest(t, true, true, nil)}
	r.ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		s := &revocableSession{qrSession: qrSession{qr: make(chan whatsmeow.QRChannelItem, 8)}, live: &r.live, maxLive: &r.maxLive}
		s.cfg = cfg
		s.identity = len(r.sessions) == 0
		if r.prepare != nil {
			r.prepare(len(r.sessions), s)
		}
		r.sessions = append(r.sessions, s)
		if n := r.live.Add(1); n > r.maxLive.Load() {
			r.maxLive.Store(n)
		}
		return s, nil
	}
	return r
}

func (r *revocationRig) session(i int) *revocableSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.sessions) {
		return nil
	}
	return r.sessions[i]
}

func (r *revocationRig) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

func TestLogoutIsImmediateOpensNoPairingAndConfirmsCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRevocationRig(t)
		if err := r.ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		first := r.session(0)
		first.send(SessionConnected)
		if st := r.ot.o.Snapshot(); !st.LoggedIn || !st.Connected {
			t.Fatalf("before logout: %+v", st)
		}
		first.send(SessionLoggedOut)
		// The status is false at once, before the asynchronous retirement runs.
		if st := r.ot.o.Snapshot(); st.LoggedIn || st.Connected {
			t.Fatalf("status after logout = %+v; want logged out immediately", st)
		}
		synctest.Wait()
		if r.count() != 1 || first.connectN.Load() != 1 {
			t.Errorf("sessions = %d, connects = %d; logout must not start new work", r.count(), first.connectN.Load())
		}
		if snap, ok := r.ot.o.PairingSnapshot(); ok && snap.Result == PairingActive {
			t.Errorf("logout opened a pairing window: %+v", snap)
		}
		if first.retiredN.Load() != 1 {
			t.Errorf("old generation retired %d times, want 1", first.retiredN.Load())
		}
		if len(first.finishCalls) != 1 || !first.finishCalls[0] {
			t.Errorf("cleanup calls = %v; want one confirmed revocation", first.finishCalls)
		}
		if r.live.Load() != 0 {
			t.Errorf("%d generations still live", r.live.Load())
		}
		stopInBubble(t, r.ot)
		if err := r.ot.o.StopError(); err != nil {
			t.Errorf("clean revocation stopped with %v", err)
		}
	})
}

// This test uses real time: the second request blocks on the retirement's sync.Once, which a
// fake-time bubble does not treat as durably blocked.
func TestLogoutOldGenerationDrainsBeforeNewClient(t *testing.T) {
	r := newRevocationRig(t)
	r.prepare = func(i int, s *revocableSession) {
		if i == 0 {
			s.retireEntered = make(chan struct{})
			s.retireRelease = make(chan struct{})
		}
	}
	if err := r.ot.o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var first *revocableSession
	for deadline := time.Now().Add(5 * time.Second); first == nil || first.connectN.Load() == 0; first = r.session(0) {
		if time.Now().After(deadline) {
			t.Fatal("startup attempt did not begin")
		}
		time.Sleep(time.Millisecond)
	}
	first.send(SessionConnected)
	first.send(SessionLoggedOut)
	<-first.retireEntered

	requested := make(chan error, 1)
	go func() {
		_, err := r.ot.o.RequestPairing(context.Background())
		requested <- err
	}()
	select {
	case err := <-requested:
		t.Fatalf("pairing request returned before the drain finished: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if r.count() != 1 {
		t.Fatalf("a new client was built while the old generation was still draining (%d sessions)", r.count())
	}
	close(first.retireRelease)
	select {
	case err := <-requested:
		if err != nil {
			t.Fatalf("request after the drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish after the drain")
	}
	if r.count() != 2 {
		t.Fatalf("sessions = %d, want a fresh one after the drain", r.count())
	}
	if r.maxLive.Load() != 1 {
		t.Errorf("max live generations = %d; generations must never overlap", r.maxLive.Load())
	}
	if err := r.ot.o.stopForTest(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLogoutPreservesLateSavedIdentityOfFreshGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRevocationRig(t)
		if err := r.ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		first := r.session(0)
		first.send(SessionConnected)
		first.send(SessionLoggedOut)
		synctest.Wait()
		if _, err := r.ot.o.RequestPairing(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		second := r.session(1)
		second.code("fresh", 20*time.Second)
		second.send(SessionPairSuccess)
		synctest.Wait()
		// A late logout from the retired generation must neither erase nor mark the new identity.
		first.send(SessionLoggedOut)
		synctest.Wait()
		if st := r.ot.o.Snapshot(); !st.LoggedIn {
			t.Fatalf("late logout from the old generation erased the new identity: %+v", st)
		}
		if len(second.finishCalls) != 0 || second.retiredN.Load() != 0 {
			t.Errorf("fresh generation was retired or cleaned up: retired=%d cleanup=%v", second.retiredN.Load(), second.finishCalls)
		}
		stopInBubble(t, r.ot)
	})
}

func TestLogoutCleanupFailureBlocksNewGenerationsAndStopsUnsuccessfully(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRevocationRig(t)
		failure := errors.New("synthetic cleanup failure")
		r.prepare = func(i int, s *revocableSession) {
			if i == 0 {
				s.finishErr = failure
			}
		}
		if err := r.ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		first := r.session(0)
		first.send(SessionConnected)
		first.send(SessionLoggedOut)
		synctest.Wait()
		if _, err := r.ot.o.RequestPairing(context.Background()); !errors.Is(err, store.ErrUnsafeRun) {
			t.Fatalf("request after failed cleanup: %v", err)
		}
		if r.count() != 1 {
			t.Errorf("a new generation was built after failed cleanup (%d sessions)", r.count())
		}
		// Stop still finishes after the true drain, reports the unresolved state and never hangs.
		r.ot.o.BeginStop()
		synctest.Wait()
		select {
		case <-r.ot.o.Done():
		default:
			t.Fatal("stop hung after the failed cleanup")
		}
		if err := r.ot.o.StopError(); err == nil {
			t.Error("stop after failed cleanup reported success")
		}
		if r.live.Load() != 0 {
			t.Errorf("%d generations still live after stop", r.live.Load())
		}
	})
}
