package wa

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// The post-pair 515 reconnect is a candidate path, enabled only by OwnerConfig.PostPairReconnect.
// These tests describe its rules: the 515 is a request that waits for PairSuccess, refuses on any
// failure, and then makes exactly one watchdog-bounded Connect on the same generation.

func newPostPairOwner(t *testing.T, script func(int64, context.Context) error) *ownerTest {
	t.Helper()
	ot := newOwnerTest(t, false, false, script)
	ot.o.cfg.PostPairReconnect = true
	return ot
}

func startPostPair(t *testing.T, ot *ownerTest) {
	t.Helper()
	if err := ot.o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	synctest.Wait()
}

func TestPostPairReconnectAfterPairSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var ot *ownerTest
		ot = newPostPairOwner(t, func(n int64, _ context.Context) error {
			if ot.sess.disconnectN.Load() == 0 {
				t.Error("the reconnect began before the paired socket was disconnected")
			}
			return nil
		})
		startPostPair(t, ot)
		ot.sess.send(SessionPairSuccess)
		ot.sess.send(SessionManualLoginReconnect)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Fatalf("connects = %d, want exactly 1", got)
		}
		ot.sess.send(SessionConnected)
		synctest.Wait()
		if st := ot.o.Snapshot(); !st.Connected || !st.LoggedIn {
			t.Errorf("status = %+v, want connected with the saved identity", st)
		}
		// Duplicate and later 515 events never start another Connect.
		ot.sess.send(SessionManualLoginReconnect)
		synctest.Sleep(time.Minute)
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects after duplicate 515 = %d, want 1", got)
		}
		stopInBubble(t, ot)
	})
}

func TestPostPairReconnectWaitsForPairSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newPostPairOwner(t, func(int64, context.Context) error { return nil })
		startPostPair(t, ot)
		ot.sess.send(SessionManualLoginReconnect)
		ot.sess.send(SessionManualLoginReconnect)
		synctest.Sleep(10 * time.Second)
		if got := ot.sess.connectN.Load(); got != 0 {
			t.Fatalf("connects before PairSuccess = %d, want 0", got)
		}
		if got := ot.sess.disconnectN.Load(); got != 0 {
			t.Fatalf("disconnects before PairSuccess = %d, want 0", got)
		}
		ot.sess.send(SessionPairSuccess)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects after PairSuccess = %d, want exactly 1 for both requests", got)
		}
		stopInBubble(t, ot)
	})
}

func TestPostPairReconnectDeniedOnFailure(t *testing.T) {
	// A logout ends the session through its own path, so only a pairing error needs the diagnostic.
	for name, fail := range map[string]SessionEvent{
		"pair error": SessionPairError,
		"logout":     SessionLoggedOut,
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ot := newPostPairOwner(t, func(int64, context.Context) error { return nil })
				startPostPair(t, ot)
				ot.sess.send(SessionManualLoginReconnect)
				ot.sess.send(fail)
				synctest.Wait()
				ot.sess.send(SessionPairSuccess)
				synctest.Sleep(time.Minute)
				if got := ot.sess.connectN.Load(); got != 0 {
					t.Errorf("connects = %d, want 0 after %s", got, name)
				}
				if fail == SessionPairError && !ot.log.contains("reconnect_denied") {
					t.Errorf("no limitation diagnostic:\n%v", ot.log.lines)
				}
				stopInBubble(t, ot)
			})
		})
	}
}

func TestPostPairReconnectWaitIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newPostPairOwner(t, func(int64, context.Context) error { return nil })
		startPostPair(t, ot)
		ot.sess.send(SessionManualLoginReconnect)
		synctest.Sleep(connectWatchdog + time.Second)
		if !ot.log.contains("reconnect_denied") {
			t.Fatalf("no diagnostic once the wait expired:\n%v", ot.log.lines)
		}
		ot.sess.send(SessionPairSuccess)
		synctest.Sleep(time.Minute)
		if got := ot.sess.connectN.Load(); got != 0 {
			t.Errorf("a PairSuccess after the bounded wait started a Connect (%d)", got)
		}
		stopInBubble(t, ot)
	})
}

func TestPostPairReconnectStopWhilePending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newPostPairOwner(t, func(int64, context.Context) error { return nil })
		startPostPair(t, ot)
		ot.sess.send(SessionManualLoginReconnect)
		synctest.Wait()
		ot.o.BeginStop()
		synctest.Wait()
		ot.sess.send(SessionPairSuccess)
		synctest.Sleep(time.Minute)
		if got := ot.sess.connectN.Load(); got != 0 {
			t.Errorf("connects after stop = %d, want 0", got)
		}
	})
}

func TestPostPairReconnectFailureIsFinal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newPostPairOwner(t, errScript(map[int64]error{1: errRefused, 2: errRefused}))
		startPostPair(t, ot)
		ot.sess.send(SessionPairSuccess)
		ot.sess.send(SessionManualLoginReconnect)
		synctest.Sleep(5 * time.Minute)
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects = %d, want 1; a failed post-pair connect is not retried", got)
		}
		if p := ot.o.phaseForTest(); p != PhaseDisconnected {
			t.Errorf("phase = %v, want disconnected", p)
		}
		if st := ot.o.Snapshot(); !st.LoggedIn || st.Connected {
			t.Errorf("status = %+v; the identity must be kept and not reported connected", st)
		}
		if !ot.log.contains("reconnect_denied") {
			t.Errorf("no diagnostic after the failed connect:\n%v", ot.log.lines)
		}
		stopInBubble(t, ot)
	})
}

func TestPostPairReconnectStalledConnectIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newPostPairOwner(t, func(_ int64, ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		startPostPair(t, ot)
		ot.sess.send(SessionPairSuccess)
		ot.sess.send(SessionManualLoginReconnect)
		synctest.Sleep(connectWatchdog + 5*time.Minute)
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects = %d, want 1", got)
		}
		if !ot.o.trySlotForTest() {
			t.Error("the attempt slot was not released after the watchdog drained the call")
		}
		stopInBubble(t, ot)
	})
}
