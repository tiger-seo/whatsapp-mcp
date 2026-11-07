package wa

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// The post-pair reconnect rechecks the owner's state twice before it Connects: after it wins the
// attempt slot, and again after it drops the paired socket. These tests pin both rechecks. A
// logout, a 405, a storage fault or a stop that lands in either window must leave the identity
// alone: no further Connect on the real account.

// hookSession runs a callback inside Disconnect, after the fake has counted it. The reconnect calls
// Disconnect between its two rechecks and holds no owner lock there, so the callback can dispatch
// an event at exactly that point.
type hookSession struct {
	*fakeSession
	afterDisconnect func()
}

func (h *hookSession) Disconnect() {
	h.fakeSession.Disconnect()
	if h.afterDisconnect != nil {
		h.afterDisconnect()
	}
}

// newHookedPostPairOwner returns a post-pair owner whose sessions run afterDisconnect inside Disconnect.
func newHookedPostPairOwner(t *testing.T, afterDisconnect func(ot *ownerTest)) *ownerTest {
	t.Helper()
	ot := newPostPairOwner(t, func(int64, context.Context) error { return nil })
	ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) {
		ot.sess.cfg = cfg
		var fired atomic.Bool
		return &hookSession{fakeSession: ot.sess, afterDisconnect: func() {
			if fired.CompareAndSwap(false, true) {
				afterDisconnect(ot)
			}
		}}, nil
	}
	return ot
}

// Second recheck: the event arrives while the paired socket is being dropped.
func TestPostPairReconnectRecheckAfterDisconnect(t *testing.T) {
	for name, event := range map[string]func(ot *ownerTest){
		"logout":   func(ot *ownerTest) { ot.sess.send(SessionLoggedOut) },
		"outdated": func(ot *ownerTest) { ot.sess.send(SessionClientOutdated) },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ot := newHookedPostPairOwner(t, event)
				startPostPair(t, ot)
				ot.sess.send(SessionPairSuccess)
				ot.sess.send(SessionManualLoginReconnect)
				synctest.Sleep(time.Minute)
				if got := ot.sess.disconnectN.Load(); got < 1 {
					t.Errorf("disconnects = %d: the reconnect did not reach its second recheck", got)
				}
				if got := ot.sess.connectN.Load(); got != 0 {
					t.Errorf("connects = %d, want 0 after %s during the reconnect", got, name)
				}
				stopInBubble(t, ot)
			})
		})
	}
}

// Second recheck, stop: BeginStop can only run off the reconnect's goroutine, so it is started from
// the hook and given a moment to take effect before Disconnect returns.
func TestPostPairReconnectRecheckStopDuringDisconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newHookedPostPairOwner(t, func(ot *ownerTest) {
			go ot.o.BeginStop()
			time.Sleep(time.Second)
		})
		startPostPair(t, ot)
		ot.sess.send(SessionPairSuccess)
		ot.sess.send(SessionManualLoginReconnect)
		synctest.Sleep(time.Minute)
		if got := ot.sess.disconnectN.Load(); got < 1 {
			t.Errorf("disconnects = %d: the reconnect did not reach its second recheck", got)
		}
		if got := ot.sess.connectN.Load(); got != 0 {
			t.Errorf("connects = %d, want 0 after a stop during the reconnect", got)
		}
		<-ot.o.Done()
	})
}

// First recheck: the reconnect is queued behind the attempt slot while a 405 or a storage fault
// lands. Neither cancels the generation's lifetime, so the reconnect wins the slot once it is
// released and must stop at the first recheck.
func TestPostPairReconnectRecheckAfterSlot(t *testing.T) {
	for name, event := range map[string]func(ot *ownerTest){
		"outdated": func(ot *ownerTest) { ot.sess.send(SessionClientOutdated) },
		"storage fault": func(ot *ownerTest) {
			close(ot.sess.faultCh)
		},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ot := newPostPairOwner(t, func(int64, context.Context) error { return nil })
				ot.sess.faultCh = make(chan struct{})
				startPostPair(t, ot)
				ot.o.slot <- struct{}{} // another attempt holds the slot
				ot.sess.send(SessionPairSuccess)
				ot.sess.send(SessionManualLoginReconnect)
				synctest.Wait()
				event(ot)
				synctest.Wait()
				disconnects := ot.sess.disconnectN.Load()
				<-ot.o.slot // the other attempt finishes
				synctest.Sleep(time.Minute)
				if got := ot.sess.connectN.Load(); got != 0 {
					t.Errorf("connects = %d, want 0 after %s while queued", got, name)
				}
				if got := ot.sess.disconnectN.Load(); got != disconnects {
					t.Errorf("the queued reconnect dropped the socket after %s (%d disconnects, was %d)", name, got, disconnects)
				}
				stopInBubble(t, ot)
			})
		})
	}
}
