package wa

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSession is a scripted SessionHandle for owner tests; it never touches the store or network.
type fakeSession struct {
	cfg         SessionConfig
	connectN    atomic.Int64
	disconnectN atomic.Int64
	retiredN    atomic.Int64
	identity    bool
	pending     bool
	faultCh     chan struct{}
	script      func(n int64, ctx context.Context) error

	// Concurrency observability: at most one attempt must ever run.
	inFlight atomic.Int64
	maxSeen  atomic.Int64

	attemptCtx                atomic.Pointer[context.Context]
	cancelledBeforeDisconnect atomic.Bool
}

func (f *fakeSession) Epoch() uint64         { return f.cfg.Epoch }
func (f *fakeSession) HasIdentity() bool     { return f.identity }
func (f *fakeSession) RecoveryPending() bool { return f.pending }
func (f *fakeSession) StorageFaulted() <-chan struct{} {
	// A nil channel never fires; only tests that close faultCh simulate a fault.
	return f.faultCh
}

func (f *fakeSession) Connect(ctx context.Context) error {
	f.attemptCtx.Store(&ctx)
	n := f.connectN.Add(1)
	cur := f.inFlight.Add(1)
	for {
		m := f.maxSeen.Load()
		if cur <= m || f.maxSeen.CompareAndSwap(m, cur) {
			break
		}
	}
	defer f.inFlight.Add(-1)
	if f.script != nil {
		return f.script(n, ctx)
	}
	return nil
}

func (f *fakeSession) Disconnect() {
	f.disconnectN.Add(1)
	if ctxp := f.attemptCtx.Load(); ctxp != nil && (*ctxp).Err() != nil {
		f.cancelledBeforeDisconnect.Store(true)
	}
}

func (f *fakeSession) Retire(context.Context) error {
	f.retiredN.Add(1)
	return nil
}

// send dispatches an admitted upstream event for this generation.
func (f *fakeSession) send(ev SessionEvent) {
	f.cfg.Dispatch(f.cfg.Epoch, ev)
}

// sendStale dispatches an event carrying a foreign epoch, which the owner must deny.
func (f *fakeSession) sendStale(ev SessionEvent) {
	f.cfg.Dispatch(f.cfg.Epoch+999, ev)
}

// fakeOwnerFactory builds a SessionFactory that returns one scripted session per generation.
func fakeOwnerFactory(mk func(cfg SessionConfig) *fakeSession) SessionFactory {
	return func(cfg SessionConfig) (SessionHandle, error) {
		f := mk(cfg)
		f.cfg = cfg
		return f, nil
	}
}

// errScript returns a script whose nth call fails with the given error; other calls succeed.
func errScript(calls map[int64]error) func(n int64, ctx context.Context) error {
	return func(n int64, _ context.Context) error {
		if err, ok := calls[n]; ok {
			return err
		}
		return nil
	}
}

// stallScript blocks every connect until its attempt context is cancelled.
func stallScript() func(n int64, ctx context.Context) error {
	return func(_ int64, ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
}

// waitConnectScript blocks the nth connect until release; other calls succeed at once.
func waitConnectScript(n int64, entered, release chan struct{}) func(n int64, ctx context.Context) error {
	return func(k int64, ctx context.Context) error {
		if k != n {
			return nil
		}
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (o *connectionOwner) stopForTest(ctx context.Context) error {
	o.BeginStop()
	select {
	case <-o.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *connectionOwner) phaseForTest() Phase {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.phase
}

// waitPhaseForTest waits until the phase is one of want.
func (o *connectionOwner) waitPhaseForTest(t testing.TB, want ...Phase) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p := o.phaseForTest()
		for _, w := range want {
			if p == w {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("phase = %v, want one of %v", p, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitStartupDoneForTest waits until no attempt is running and the startup phase is over.
func (o *connectionOwner) waitStartupDoneForTest(t testing.TB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		o.mu.Lock()
		p := o.phase
		o.mu.Unlock()
		if o.trySlotForTest() && (p == PhaseDisconnected || p == PhaseConnected || p == PhaseLoggedOut || p == PhaseIdle) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup did not end; phase = %v", p)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitWindowResultForTest waits until the pairing window reports want.
func (o *connectionOwner) waitWindowResultForTest(t testing.TB, want PairingResult) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if snap, ok := o.PairingSnapshot(); ok && snap.Result == want {
			return
		}
		if time.Now().After(deadline) {
			snap, ok := o.PairingSnapshot()
			t.Fatalf("window result = %+v, %v; want %v", snap, ok, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// trySlotForTest reports whether the attempt slot is free, without keeping it.
func (o *connectionOwner) trySlotForTest() bool {
	select {
	case o.slot <- struct{}{}:
		<-o.slot
		return true
	default:
		return false
	}
}
