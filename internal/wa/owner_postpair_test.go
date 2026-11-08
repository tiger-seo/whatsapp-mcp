package wa

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// The connection WhatsApp requests right after pairing (the 515) runs on a fresh client. These tests describe the
// rules: the 515 is a request that waits for PairSuccess, any failure refuses it, an accepted request retires and
// checks the pairing generation first, and then exactly one fresh generation makes the one watchdog-bounded Connect.

// postPairRig is an owner whose factory returns one scripted session per generation: the first has no identity
// (the pairing generation), every later one has the saved identity.
type postPairRig struct {
	*ownerTest
	mu   sync.Mutex
	gens []*hookQuiet
	// check returns the quiet-stop result of generation idx; nil means every check passes.
	check func(idx int) QuietStopResult
	// onOpen runs inside the factory when generation idx is built.
	onOpen func(idx int)
	// onRetire runs inside Retire of generation idx, after the fake counted it.
	onRetire func(idx int)
	// faultCh is the storage-fault channel of the pairing generation; nil never fires.
	faultCh chan struct{}
	// script is the Connect script every fresh generation shares.
	script func(int64, context.Context) error
}

type hookQuiet struct {
	*quietSession
	rig *postPairRig
	idx int
}

func (h *hookQuiet) Retire(ctx context.Context) error {
	err := h.fakeSession.Retire(ctx)
	if h.rig.onRetire != nil {
		h.rig.onRetire(h.idx)
	}
	return err
}

func (h *hookQuiet) QuietStop(ctx context.Context, d time.Duration) QuietStopResult {
	if h.rig.check != nil {
		h.quietSession.result = h.rig.check(h.idx)
	}
	return h.quietSession.QuietStop(ctx, d)
}

func newPostPairRig(t *testing.T, script func(int64, context.Context) error) *postPairRig {
	t.Helper()
	r := &postPairRig{script: script}
	r.ownerTest = newOwnerTest(t, false, false, script)
	r.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) {
		r.mu.Lock()
		idx := len(r.gens)
		r.mu.Unlock()
		if r.onOpen != nil {
			r.onOpen(idx)
		}
		fs := &fakeSession{identity: idx > 0, script: r.script}
		if idx == 0 {
			fs.faultCh = r.faultCh
		}
		fs.cfg = cfg
		h := &hookQuiet{quietSession: &quietSession{fakeSession: fs, result: passed()}, rig: r, idx: idx}
		r.mu.Lock()
		r.gens = append(r.gens, h)
		r.mu.Unlock()
		return h, nil
	}
	return r
}

func (r *postPairRig) gen(i int) *hookQuiet {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gens[i]
}

func (r *postPairRig) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.gens)
}

func (r *postPairRig) start(t *testing.T) {
	t.Helper()
	if err := r.o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// RequestPairing marks the pairing unresolved until a fresh connection confirms the saved identity.
	r.o.mu.Lock()
	r.o.unresolved = true
	r.o.mu.Unlock()
	synctest.Wait()
}

// pairingRequest is what the pairing socket delivers: PairSuccess, then the server's restart request.
func (r *postPairRig) paired() {
	r.gen(0).send(SessionPairSuccess)
	r.gen(0).send(SessionManualLoginReconnect)
}

func (r *postPairRig) unsafe() bool {
	r.o.mu.Lock()
	defer r.o.mu.Unlock()
	return r.o.unsafeStop
}

func TestPostPairHandoverOpensOneFreshGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newPostPairRig(t, func(int64, context.Context) error { return nil })
		r.onOpen = func(idx int) {
			// A fresh generation exists only after the pairing generation was retired and passed its check.
			if idx == 1 && (r.gen(0).retiredN.Load() != 1 || len(r.gen(0).asked) != 1) {
				t.Error("the fresh generation was built before the pairing generation was retired and checked")
			}
		}
		r.start(t)
		r.paired()
		synctest.Wait()
		if got := r.count(); got != 2 {
			t.Fatalf("generations = %d, want the pairing one and exactly one fresh one", got)
		}
		if got := r.gen(0).connectN.Load(); got != 0 {
			t.Errorf("the pairing generation connected %d times; the test never ran its window", got)
		}
		if got := r.gen(0).asked[0]; got != postPairQuietDeadline {
			t.Errorf("the pairing generation was checked with %v, want %v", got, postPairQuietDeadline)
		}
		if got := r.gen(1).connectN.Load(); got != 1 {
			t.Fatalf("fresh connects = %d, want exactly 1", got)
		}
		r.gen(1).send(SessionConnected)
		synctest.Wait()
		if st := r.o.Snapshot(); !st.Connected || !st.LoggedIn {
			t.Errorf("status = %+v, want connected with the saved identity", st)
		}
		// Duplicate and later 515 events, from either generation, never start another Connect or generation.
		r.gen(0).send(SessionManualLoginReconnect)
		r.gen(1).send(SessionManualLoginReconnect)
		synctest.Sleep(time.Minute)
		if r.count() != 2 || r.gen(1).connectN.Load() != 1 {
			t.Errorf("a duplicate 515 started more work: %d generations, %d fresh connects", r.count(), r.gen(1).connectN.Load())
		}
		stopInBubble(t, r.ownerTest)
		if r.unsafe() {
			t.Error("an orderly stop of a linked pair marked the run unsafe")
		}
	})
}

func TestPostPairHandoverWaitsForPairSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newPostPairRig(t, func(int64, context.Context) error { return nil })
		r.start(t)
		r.gen(0).send(SessionManualLoginReconnect)
		r.gen(0).send(SessionManualLoginReconnect)
		synctest.Sleep(10 * time.Second)
		if r.count() != 1 || r.gen(0).retiredN.Load() != 0 {
			t.Fatalf("the 515 acted before PairSuccess: %d generations, %d retirements", r.count(), r.gen(0).retiredN.Load())
		}
		r.gen(0).send(SessionPairSuccess)
		synctest.Wait()
		if r.count() != 2 || r.gen(1).connectN.Load() != 1 {
			t.Errorf("after PairSuccess: %d generations, %d fresh connects, want 2 and 1 for both requests", r.count(), r.gen(1).connectN.Load())
		}
		stopInBubble(t, r.ownerTest)
	})
}

func TestPostPairHandoverRefusedOnFailure(t *testing.T) {
	// A logout ends the session through its own path, so only a pairing error needs the diagnostic.
	for name, fail := range map[string]SessionEvent{
		"pair error": SessionPairError,
		"logout":     SessionLoggedOut,
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newPostPairRig(t, func(int64, context.Context) error { return nil })
				r.start(t)
				r.gen(0).send(SessionManualLoginReconnect)
				r.gen(0).send(fail)
				synctest.Wait()
				r.gen(0).send(SessionPairSuccess)
				synctest.Sleep(time.Minute)
				for i := 1; i < r.count(); i++ {
					if r.gen(i).connectN.Load() != 0 {
						t.Errorf("generation %d connected after %s", i, name)
					}
				}
				if fail == SessionPairError && !r.log.contains("reconnect_denied") {
					t.Errorf("no limitation diagnostic:\n%v", r.log.lines)
				}
				stopInBubble(t, r.ownerTest)
			})
		})
	}
}

func TestPostPairHandoverWaitIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newPostPairRig(t, func(int64, context.Context) error { return nil })
		r.start(t)
		r.gen(0).send(SessionManualLoginReconnect)
		synctest.Sleep(connectWatchdog + time.Second)
		if !r.log.contains("reconnect_denied") {
			t.Fatalf("no diagnostic once the wait expired:\n%v", r.log.lines)
		}
		r.gen(0).send(SessionPairSuccess)
		synctest.Sleep(time.Minute)
		if r.count() != 1 {
			t.Errorf("a PairSuccess after the bounded wait opened a generation (%d)", r.count())
		}
		if p := r.o.phaseForTest(); p != PhaseDisconnected {
			t.Errorf("phase = %v, want disconnected", p)
		}
		stopInBubble(t, r.ownerTest)
	})
}

func TestPostPairHandoverStopWhilePending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newPostPairRig(t, func(int64, context.Context) error { return nil })
		r.start(t)
		r.gen(0).send(SessionManualLoginReconnect)
		synctest.Wait()
		r.o.BeginStop()
		synctest.Wait()
		r.gen(0).send(SessionPairSuccess)
		synctest.Sleep(time.Minute)
		if r.count() != 1 {
			t.Errorf("generations after stop = %d, want 1", r.count())
		}
		<-r.o.Done()
	})
}

// The server's close can follow the 515 within milliseconds, before the attempt slot is taken. It must not read
// as a failed link.
func TestPostPairServerCloseDuringHandoverKeepsTheOwnerConnecting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newPostPairRig(t, func(int64, context.Context) error { return nil })
		r.start(t)
		r.o.slot <- struct{}{} // the slot is busy, so the handover waits
		r.paired()
		r.gen(0).send(SessionDisconnected)
		synctest.Wait()
		if p := r.o.phaseForTest(); p != PhaseConnecting {
			t.Fatalf("phase after the server's close = %v, want connecting", p)
		}
		<-r.o.slot
		synctest.Wait()
		if r.count() != 2 || r.gen(1).connectN.Load() != 1 {
			t.Errorf("handover after the close: %d generations, %d fresh connects", r.count(), r.gen(1).connectN.Load())
		}
		stopInBubble(t, r.ownerTest)
	})
}

func TestPostPairFailedCheckStartsNoConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newPostPairRig(t, func(int64, context.Context) error { return nil })
		r.check = func(int) QuietStopResult { return QuietStopResult{Reason: QuietWorkRunning, LibraryGoroutines: 2} }
		r.start(t)
		r.paired()
		synctest.Sleep(time.Minute)
		if r.count() != 1 {
			t.Fatalf("generations = %d; a failed check must not open a fresh client", r.count())
		}
		if p := r.o.phaseForTest(); p != PhaseDisconnected {
			t.Errorf("phase = %v, want disconnected (incomplete link)", p)
		}
		if !r.unsafe() {
			t.Error("a failed check left the marker rules clean")
		}
		if !r.log.contains("quiet_stop_failed") || !r.log.contains("reconnect_denied") {
			t.Errorf("missing events:\n%v", r.log.lines)
		}
		stopInBubble(t, r.ownerTest)
	})
}

func TestPostPairFreshFailureIsFinal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newPostPairRig(t, errScript(map[int64]error{1: errRefused, 2: errRefused}))
		r.start(t)
		r.paired()
		synctest.Sleep(5 * time.Minute)
		if r.count() != 2 || r.gen(1).connectN.Load() != 1 {
			t.Errorf("%d generations, %d fresh connects; a failed fresh connect is not retried", r.count(), r.gen(1).connectN.Load())
		}
		if p := r.o.phaseForTest(); p != PhaseDisconnected {
			t.Errorf("phase = %v, want disconnected", p)
		}
		if st := r.o.Snapshot(); !st.LoggedIn || st.Connected {
			t.Errorf("status = %+v; the identity must be kept and not reported connected", st)
		}
		if !r.log.contains("reconnect_denied") {
			t.Errorf("no diagnostic after the failed connect:\n%v", r.log.lines)
		}
		stopInBubble(t, r.ownerTest)
		if !r.unsafe() && !r.o.unresolvedForTest() {
			t.Error("a failed link left nothing that keeps the marker unsafe")
		}
	})
}

func TestPostPairFreshStalledConnectIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newPostPairRig(t, func(_ int64, ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		r.start(t)
		r.paired()
		synctest.Sleep(connectWatchdog + 5*time.Minute)
		if r.count() != 2 || r.gen(1).connectN.Load() != 1 {
			t.Errorf("%d generations, %d fresh connects, want 2 and 1", r.count(), r.gen(1).connectN.Load())
		}
		if !r.o.trySlotForTest() {
			t.Error("the attempt slot was not released after the watchdog drained the call")
		}
		if p := r.o.phaseForTest(); p != PhaseDisconnected {
			t.Errorf("phase = %v, want disconnected", p)
		}
		stopInBubble(t, r.ownerTest)
	})
}

// The slot is held from the pairing generation's retirement to the end of the fresh attempt.
func TestPostPairHoldsTheSlotAcrossRetireAndConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		entered := make(chan struct{})
		r := newPostPairRig(t, waitConnectScript(1, entered, release))
		var slotFree [2]bool
		r.onRetire = func(idx int) {
			if idx == 0 {
				slotFree[0] = r.o.trySlotForTest()
			}
		}
		r.start(t)
		r.paired()
		synctest.Wait()
		<-entered
		slotFree[1] = r.o.trySlotForTest()
		if slotFree[0] || slotFree[1] {
			t.Errorf("the attempt slot was free during retire (%v) or connect (%v)", slotFree[0], slotFree[1])
		}
		close(release)
		r.gen(1).send(SessionConnected)
		synctest.Wait()
		if !r.o.trySlotForTest() {
			t.Error("the slot was not released after the attempt")
		}
		stopInBubble(t, r.ownerTest)
	})
}

// A stop at each point of the handover: nothing connects after the stop began, every generation that exists is
// retired before the owner is done, and the unresolved pairing keeps the marker unsafe.
func TestPostPairStopInterleavings(t *testing.T) {
	type tc struct {
		name         string
		arm          func(r *postPairRig)
		wantGens     int
		wantFreshRun int64
	}
	// BeginStop runs on the caller's goroutine: the hook sits where the stop can land, and finishStop then waits
	// for the same retirement or slot the handover holds.
	stopNow := func(r *postPairRig) { r.o.BeginStop() }
	cases := []tc{
		{"during the pairing generation's retirement", func(r *postPairRig) {
			r.onRetire = func(idx int) {
				if idx == 0 {
					stopNow(r)
				}
			}
		}, 1, 0},
		{"between retirement and the fresh generation", func(r *postPairRig) {
			r.onOpen = func(idx int) {
				if idx == 1 {
					stopNow(r)
				}
			}
		}, 2, 0},
		{"between creation and connect", func(r *postPairRig) {
			r.o.cfg.afterPostPairPublish = func() { stopNow(r) }
		}, 2, 0},
		{"during the attempt", func(r *postPairRig) {
			r.script = stallScript()
		}, 2, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newPostPairRig(t, func(int64, context.Context) error { return nil })
				c.arm(r)
				r.start(t)
				r.paired()
				synctest.Sleep(time.Minute)
				if c.name == "during the attempt" {
					r.o.BeginStop()
					synctest.Sleep(time.Minute)
				}
				<-r.o.Done()
				if got := r.count(); got != c.wantGens {
					t.Errorf("generations = %d, want %d", got, c.wantGens)
				}
				if c.wantGens > 1 {
					if got := r.gen(1).connectN.Load(); got != c.wantFreshRun {
						t.Errorf("fresh connects = %d, want %d", got, c.wantFreshRun)
					}
				}
				for i := 0; i < r.count(); i++ {
					if got := r.gen(i).retiredN.Load(); got < 1 {
						t.Errorf("generation %d was never retired", i)
					}
				}
				if !r.o.unresolvedForTest() && !r.unsafe() {
					t.Error("an interrupted link left nothing that keeps the marker unsafe")
				}
			})
		})
	}
}

func (o *connectionOwner) unresolvedForTest() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.unresolved
}

// A pending-515 timer must not rewrite a terminal phase after the fact.
func TestPostPairPendingTimerKeepsTerminalPhase(t *testing.T) {
	t.Run("stopped", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newPostPairRig(t, func(int64, context.Context) error { return nil })
			r.start(t)
			r.gen(0).send(SessionManualLoginReconnect)
			synctest.Wait()
			r.o.BeginStop()
			synctest.Wait()
			<-r.o.Done()
			synctest.Sleep(connectWatchdog + time.Minute)
			if p := r.o.phaseForTest(); p != PhaseStopped {
				t.Errorf("phase after the timer = %v, want stopped", p)
			}
		})
	})
	t.Run("storage failed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newPostPairRig(t, func(int64, context.Context) error { return nil })
			r.faultCh = make(chan struct{})
			r.start(t)
			r.gen(0).send(SessionManualLoginReconnect)
			synctest.Wait()
			close(r.faultCh)
			synctest.Wait()
			if p := r.o.phaseForTest(); p != PhaseStorageFailed {
				t.Fatalf("phase after the fault = %v, want storage_failed", p)
			}
			synctest.Sleep(connectWatchdog + time.Minute)
			if p := r.o.phaseForTest(); p != PhaseStorageFailed {
				t.Errorf("phase after the timer = %v, want storage_failed", p)
			}
			stopInBubble(t, r.ownerTest)
		})
	})
}
