package wa

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// The owner starts no Connect beyond the first production attempt of a generation: startup
// retries, 515 reconnects and every other automatic path are denied until their safety proof and
// owner review exist. These tests assert refusal and the diagnostic, never a retry.

func TestOwnerStartupFailureIsNotRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, errScript(map[int64]error{1: errRefused, 2: errRefused, 3: errRefused, 4: errRefused}))
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		synctest.Sleep(5 * time.Minute)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects = %d, want exactly 1; startup retries are denied", got)
		}
		if p := ot.o.phaseForTest(); p != PhaseDisconnected {
			t.Errorf("phase = %v, want disconnected", p)
		}
		if st := ot.o.Snapshot(); !st.LoggedIn || st.Connected {
			t.Errorf("status = %+v; the identity must be retained", st)
		}
		if !ot.log.contains("reconnect_denied") {
			t.Errorf("no secret-free limitation diagnostic:\n%v", ot.log.lines)
		}
		if _, ok := ot.o.PairingSnapshot(); ok {
			t.Error("a denied retry reopened a pairing window")
		}
		if ot.sess.retiredN.Load() != 0 {
			t.Error("a failed startup retired the generation")
		}
		stopInBubble(t, ot)
	})
}

func TestOwnerStartupTimeoutIsNotRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, func(_ int64, ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		synctest.Sleep(connectWatchdog + 5*time.Minute)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects = %d, want exactly 1 after a watchdog timeout", got)
		}
		if ot.sess.disconnectN.Load() < 1 {
			t.Error("the stalled attempt was not disconnected")
		}
		if !ot.log.contains("reconnect_denied") {
			t.Errorf("no limitation diagnostic after the timeout:\n%v", ot.log.lines)
		}
		stopInBubble(t, ot)
	})
}

func TestOwnerManualLoginReconnectIsDenied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, false, true, func(int64, context.Context) error { return nil })
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		synctest.Wait()
		ot.sess.send(SessionConnected)
		synctest.Wait()
		// Concurrent 515 events, also while the attempt slot is occupied, never start a Connect.
		ot.sess.send(SessionManualLoginReconnect)
		ot.sess.send(SessionManualLoginReconnect)
		ot.o.slot <- struct{}{}
		ot.sess.send(SessionManualLoginReconnect)
		synctest.Sleep(time.Minute)
		<-ot.o.slot
		synctest.Sleep(time.Minute)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects = %d, want 1; the 515 reconnect is denied", got)
		}
		if ot.sess.disconnectN.Load() != 0 {
			t.Error("a denied 515 reconnect still disconnected the socket")
		}
		if st := ot.o.Snapshot(); st.Connected {
			t.Errorf("status = %+v; a denied reconnect must not report connected", st)
		}
		if p := ot.o.phaseForTest(); p != PhaseDisconnected {
			t.Errorf("phase = %v, want disconnected", p)
		}
		if !ot.log.contains("reconnect_denied") {
			t.Errorf("no limitation diagnostic:\n%v", ot.log.lines)
		}
		stopInBubble(t, ot)
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects after stop = %d, want 1", got)
		}
	})
}

func TestOwnerNoConnectAfterRetirement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, errScript(map[int64]error{1: errRefused}))
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		synctest.Wait()
		ot.sess.send(SessionLoggedOut)
		synctest.Wait()
		ot.sess.send(SessionManualLoginReconnect)
		ot.sess.send(SessionClientOutdated)
		synctest.Sleep(time.Minute)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects = %d, want 1; no Connect after retirement", got)
		}
		stopInBubble(t, ot)
	})
}
