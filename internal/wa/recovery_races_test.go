package wa

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
)

// Adverse interleavings of the pairing window against logout, timeout, retirement and delayed
// upstream events. Each case shows that a rejected action changes neither the current account state
// nor the QR material.

func openWindowWithCode(t *testing.T, code string) (*ownerTest, *qrSession) {
	t.Helper()
	ot, qs := pairingRig(t)
	if _, err := ot.o.RequestPairing(context.Background()); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	qs.code(code, 20*time.Second)
	synctest.Wait()
	return ot, qs
}

// A replacement QR queued behind a logout never becomes visible, and the logout leaves no window.
func TestRaceReplacementQRVersusLogout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := openWindowWithCode(t, "first")
		qs.send(SessionLoggedOut)
		qs.code("replacement", 20*time.Second)
		synctest.Wait()
		snap, _ := ot.o.PairingSnapshot()
		if snap.Code != "" || snap.Result == PairingActive {
			t.Fatalf("window after logout and a queued replacement: %+v", snap)
		}
		if st := ot.o.Snapshot(); st.LoggedIn || st.Connected {
			t.Fatalf("status after logout: %+v", st)
		}
		stopInBubble(t, ot)
	})
}

// A PairSuccess that arrives after the window timed out and its generation retired is stale: it
// changes neither the window result nor the login state.
func TestRaceLatePairSuccessVersusTimeoutAndRetire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := openWindowWithCode(t, "only")
		qs.qr <- whatsmeow.QRChannelItem{Event: "timeout"}
		synctest.Wait()
		if qs.retiredN.Load() != 1 {
			t.Fatalf("generation retired %d times", qs.retiredN.Load())
		}
		qs.send(SessionPairSuccess)
		synctest.Wait()
		snap, _ := ot.o.PairingSnapshot()
		if snap.Result != PairingTimedOut || snap.Code != "" {
			t.Fatalf("late success rewrote the terminal result: %+v", snap)
		}
		if st := ot.o.Snapshot(); st.LoggedIn {
			t.Fatalf("late success published a login: %+v", st)
		}
		stopInBubble(t, ot)
	})
}

// A delayed client-outdated (405) event after the final timeout cannot reopen the terminal window,
// start a connection or restore a code.
func TestRaceFinalTimeoutVersusDelayed405(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := openWindowWithCode(t, "only")
		qs.qr <- whatsmeow.QRChannelItem{Event: "timeout"}
		synctest.Wait()
		connects := qs.connectN.Load()
		qs.send(SessionClientOutdated)
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		snap, _ := ot.o.PairingSnapshot()
		if snap.Result != PairingTimedOut || snap.Code != "" {
			t.Fatalf("405 reopened the terminal window: %+v", snap)
		}
		if qs.connectN.Load() != connects {
			t.Errorf("connects %d -> %d; a delayed 405 must not start an attempt", connects, qs.connectN.Load())
		}
		if _, err := ot.o.RequestPairing(context.Background()); !errors.Is(err, store.ErrUnsafeRun) {
			t.Errorf("request after the timeout and a 405: %v", err)
		}
		stopInBubble(t, ot)
	})
}

// Stop racing the final timeout from either side ends with one retirement, no code and no
// connection left behind.
func TestRaceStopVersusFinalTimeout(t *testing.T) {
	for _, stopFirst := range []bool{false, true} {
		name := "timeout_first"
		if stopFirst {
			name = "stop_first"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ot, qs := openWindowWithCode(t, "only")
				if stopFirst {
					ot.o.BeginStop()
					qs.qr <- whatsmeow.QRChannelItem{Event: "timeout"}
				} else {
					qs.qr <- whatsmeow.QRChannelItem{Event: "timeout"}
					ot.o.BeginStop()
				}
				synctest.Wait()
				select {
				case <-ot.o.Done():
				default:
					t.Fatal("owner did not finish stopping")
				}
				if qs.retiredN.Load() != 1 {
					t.Errorf("retired %d times, want 1", qs.retiredN.Load())
				}
				if snap, _ := ot.o.PairingSnapshot(); snap.Code != "" || snap.Result == PairingActive {
					t.Errorf("window after stop: %+v", snap)
				}
			})
		})
	}
}

// A PairError cannot be turned into a success by a later PairSuccess from the same generation.
func TestRacePairErrorThenPairSuccessKeepsFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := openWindowWithCode(t, "only")
		qs.send(SessionPairError)
		synctest.Wait()
		qs.send(SessionPairSuccess)
		synctest.Wait()
		snap, _ := ot.o.PairingSnapshot()
		if snap.Result != PairingFailed || snap.Code != "" {
			t.Fatalf("failure was overwritten: %+v", snap)
		}
		stopInBubble(t, ot)
	})
}

// An ordinary write that arrives during a repair is deferred, not lost and not allowed to clear or
// supersede the repair: the repair completes and the demand is replayed once.
func TestRaceOrdinaryWriteDuringRepairIsDeferredAndReplayed(t *testing.T) {
	dir := waDataDir(t)
	seedInterruptedProgress(t, dir)
	s, closeAll := reopenSession(t, dir)
	defer closeAll()
	var fulls, replays int
	s.repair.fetch = func(ctx context.Context, name string, full bool) error {
		if !full {
			replays++
			return nil
		}
		fulls++
		err := s.dev.AppState.PutAppStateVersion(context.Background(), name, 11, [128]byte{11})
		if !errors.Is(err, store.ErrRecoveryPending) {
			t.Errorf("ordinary write during repair = %v, want recovery pending", err)
		}
		return s.dev.AppState.PutAppStateVersion(ctx, name, 4, [128]byte{4})
	}
	if err := s.RunRepairs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fulls != 1 || replays != 1 {
		t.Fatalf("full resyncs = %d, replays = %d; want 1 and 1", fulls, replays)
	}
	if pending, _ := s.barrier.PendingRepairs(context.Background()); len(pending) != 0 {
		t.Fatalf("pending after repair = %v", pending)
	}
}

// A repair abandoned by one generation cannot affect the next: the new generation (a newer epoch)
// repairs and clears, and the old generation's late writes, including ones detached from their
// context, are rejected and recreate nothing.
func TestRaceNewEpochRepairVersusAbandonedGeneration(t *testing.T) {
	dir := waDataDir(t)
	seedInterruptedProgress(t, dir)
	st := openWADirAt(t, dir)

	life1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	s1, err := OpenSession(newSessionTestConfig(st, life1))
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	entered := make(chan struct{})
	late := make(chan [2]error, 1)
	s1.repair.deadline = 50 * time.Millisecond
	s1.repair.fetch = func(ctx context.Context, name string, full bool) error {
		close(entered)
		<-release
		// The old generation's tagged write, and one detached from the cancelled repair context.
		a := s1.dev.AppState.PutAppStateVersion(ctx, name, 4, [128]byte{4})
		b := s1.dev.AppState.PutAppStateVersion(context.WithoutCancel(ctx), name, 5, [128]byte{5})
		late <- [2]error{a, b}
		return a
	}
	if err := s1.RunRepairs(context.Background()); !errors.Is(err, errRepairDeadline) {
		t.Fatalf("generation 1 repair = %v", err)
	}
	<-entered
	cancel1()
	if err := s1.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}

	life2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	cfg2 := newSessionTestConfig(st, life2)
	cfg2.Epoch = 2
	s2, err := OpenSession(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Retire(context.Background())
	s2.repair.fetch = func(ctx context.Context, name string, full bool) error {
		if !full {
			return nil
		}
		return s2.dev.AppState.PutAppStateVersion(ctx, name, 6, [128]byte{6})
	}
	if err := s2.RunRepairs(context.Background()); err != nil {
		t.Fatalf("generation 2 repair: %v", err)
	}
	if pending, _ := s2.barrier.PendingRepairs(context.Background()); len(pending) != 0 {
		t.Fatalf("pending after generation 2 repair = %v", pending)
	}

	close(release)
	errs := <-late
	for i, err := range errs {
		if err == nil {
			t.Errorf("late write %d from the retired generation succeeded", i)
		}
	}
	if pending, _ := s2.barrier.PendingRepairs(context.Background()); len(pending) != 0 {
		t.Fatalf("a retired generation recreated or restored a marker: %v", pending)
	}
}

// A guarded write failure latched during a repair requires a restart: nothing clears, and a new
// generation with a healthy store can recover.
func TestRaceWriteFaultDuringRepairNeverClearsAndRestartRecovers(t *testing.T) {
	dir := waDataDir(t)
	seedInterruptedProgress(t, dir)
	s, closeAll := reopenSession(t, dir)
	s.repair.fetch = func(ctx context.Context, name string, full bool) error {
		// An invalid collection name is a guarded error that upstream would discard.
		_ = s.dev.AppState.PutAppStateVersion(ctx, "not_a_collection", 1, [128]byte{})
		return nil
	}
	if err := s.RunRepairs(context.Background()); err == nil {
		t.Fatal("a repair with a discarded guarded error reported success")
	}
	if !s.RecoveryPending() {
		t.Error("a repair with a discarded guarded error cleared recovery")
	}
	closeAll()

	s2, closeAll2 := reopenSession(t, dir)
	defer closeAll2()
	s2.repair.fetch = func(context.Context, string, bool) error { return nil }
	if err := s2.RunRepairs(context.Background()); err != nil {
		t.Fatalf("restart repair: %v", err)
	}
	if s2.RecoveryPending() {
		t.Error("the restarted generation did not recover")
	}
}
