package wa

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// The post-pair handover rechecks the owner's state before it retires the pairing generation, and it reads the
// result after that retirement. A logout, a 405, a storage fault or a stop that lands in either window must leave
// the identity alone: no fresh client may connect on the real account.

// Recheck after retirement: a logout confirmed by the server while the pairing generation is retired.
func TestPostPairHandoverLogoutDuringRetirementOpensNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newPostPairRig(t, func(int64, context.Context) error { return nil })
		r.onRetire = func(idx int) {
			if idx == 0 {
				r.gen(0).send(SessionLoggedOut)
			}
		}
		r.start(t)
		r.paired()
		synctest.Sleep(time.Minute)
		if r.count() != 1 {
			t.Errorf("generations = %d, want 1 after a logout during the handover", r.count())
		}
		stopInBubble(t, r.ownerTest)
	})
}

// Recheck after the slot: the handover is queued behind the attempt slot while a 405 or a storage fault lands.
// A 405 does not cancel the generation's lifetime, so the handover wins the slot once it is released and must stop
// at the recheck. A storage fault retires the generation, which cancels its lifetime, so the queued handover
// gives up its wait for the slot instead. Either way nothing is retired by the handover and nothing opens.
func TestPostPairHandoverRecheckAfterSlot(t *testing.T) {
	for name, event := range map[string]func(r *postPairRig){
		"outdated":      func(r *postPairRig) { r.gen(0).send(SessionClientOutdated) },
		"storage fault": func(r *postPairRig) { close(r.faultCh) },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newPostPairRig(t, func(int64, context.Context) error { return nil })
				r.faultCh = make(chan struct{})
				r.start(t)
				r.o.slot <- struct{}{} // another attempt holds the slot
				r.paired()
				synctest.Wait()
				event(r)
				synctest.Wait()
				retired := r.gen(0).retiredN.Load()
				<-r.o.slot // the other attempt finishes
				synctest.Sleep(time.Minute)
				if r.count() != 1 {
					t.Errorf("generations = %d, want 1 after %s while queued", r.count(), name)
				}
				if got := r.gen(0).retiredN.Load(); got != retired {
					t.Errorf("the queued handover retired the pairing generation after %s", name)
				}
				stopInBubble(t, r.ownerTest)
			})
		})
	}
}
