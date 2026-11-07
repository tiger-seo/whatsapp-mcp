package wa

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// The 405 (client outdated) budget of three five-second retries with a refresh before each is
// behind the proof gate in contracts/connection-owner.md and is not enabled. These tests assert
// what is shipped: a 405 triggers no Connect, no discovery fetch and no new window, duplicates
// change nothing, and the owner says so with a secret-free diagnostic.

func TestOwnerClientOutdatedIsNotRetriedAndIsDiagnosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, errScript(map[int64]error{1: errRefused}))
		var rev, fetches atomic.Int64
		ot.o.cfg.Versions.client = newBoundedHTTPClient(15*time.Second, 1<<20, counting(&rev, &fetches))
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		synctest.Wait()
		fetchesAtStart := fetches.Load()
		connects := ot.sess.connectN.Load()
		for i := 0; i < 5; i++ { // duplicate and concurrent 405 events share one outcome
			ot.sess.send(SessionClientOutdated)
		}
		synctest.Sleep(10 * time.Minute)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != connects {
			t.Errorf("connects = %d, want %d; a 405 must not trigger a retry", got, connects)
		}
		if got := fetches.Load(); got != fetchesAtStart {
			t.Errorf("%d discovery fetches after a 405; a retry's refresh must not run while retries are denied", got-fetchesAtStart)
		}
		if got := ot.log.count("reconnect_denied"); got != 1 {
			t.Errorf("%d reconnect_denied diagnostics for five duplicate 405 events, want exactly 1:\n%v", got, ot.log.lines)
		}
		if st := ot.o.Snapshot(); !st.LoggedIn || st.Connected {
			t.Errorf("status = %+v; the identity must be kept and the owner disconnected", st)
		}
		if _, ok := ot.o.PairingSnapshot(); ok {
			t.Error("a 405 reopened a pairing window")
		}
		stopInBubble(t, ot)
	})
}

// After stop, a late 405 is ignored: no Connect, no diagnostic noise, no new window.
func TestOwnerClientOutdatedAfterStopIsIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, errScript(map[int64]error{}))
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		synctest.Wait()
		connects := ot.sess.connectN.Load()
		stopInBubble(t, ot)
		ot.sess.send(SessionClientOutdated)
		synctest.Sleep(time.Minute)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != connects {
			t.Errorf("connects = %d, want %d after stop", got, connects)
		}
	})
}

func (l *memLog) count(s string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, s) {
			n++
		}
	}
	return n
}
