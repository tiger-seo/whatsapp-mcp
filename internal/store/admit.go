package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// gate admits store operations: at most 16 run at once, at most 64 callers wait, and a caller waits for at most
// 5 s or its own deadline.
type gate struct {
	active  chan struct{}
	waiting atomic.Int32

	mu      sync.Mutex
	closed  bool
	closing chan struct{} // closed by quiesce, to wake every waiter
	running sync.WaitGroup

	ctx    context.Context // cancelled by quiesce after the grace period
	cancel context.CancelFunc

	quiesced    chan struct{} // closed exactly once when admission is closed and running has drained
	quiesceOnce sync.Once     // closes quiesced exactly once
	waiterOnce  sync.Once     // starts the one completion waiter, however often quiesce is called
}

func newGate() *gate {
	g := &gate{
		active:   make(chan struct{}, maxActive),
		closing:  make(chan struct{}),
		quiesced: make(chan struct{}),
	}
	g.ctx, g.cancel = context.WithCancel(context.Background())
	return g
}

func (g *gate) isClosed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}

// isQuiesced reports whether admission is closed and every admitted operation has finished.
func (g *gate) isQuiesced() bool {
	select {
	case <-g.quiesced:
		return true
	default:
		return false
	}
}

// acquire takes an admission slot. A free slot is taken at once, without counting as a waiter. Otherwise the
// caller waits, counted toward the 64-waiter cap, until a slot frees, ctx ends, admissionWait passes or the gate
// closes.
func (g *gate) acquire(ctx context.Context) error {
	select {
	case <-g.closing:
		return ErrClosed
	default:
	}
	select {
	case g.active <- struct{}{}:
		return nil
	default:
	}
	if g.waiting.Add(1) > maxWaiting {
		g.waiting.Add(-1)
		return ErrBusy
	}
	defer g.waiting.Add(-1)
	timer := time.NewTimer(admissionWait)
	defer timer.Stop()
	select {
	case g.active <- struct{}{}:
		return nil
	case <-g.closing:
		return ErrClosed
	case <-timer.C:
		return ErrBusy
	case <-ctx.Done():
		return ctx.Err()
	}
}

// enter admits one operation. release must be called once. opCtx is the caller's context with the 10 s
// operation limit, and it is cancelled when the store closes.
func (g *gate) enter(ctx context.Context) (release func(), opCtx context.Context, err error) {
	if err := g.acquire(ctx); err != nil {
		return nil, nil, err
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		<-g.active
		return nil, nil, ErrClosed
	}
	g.running.Add(1)
	g.mu.Unlock()

	opCtx, cancel := context.WithTimeout(ctx, operationLimit)
	stop := context.AfterFunc(g.ctx, cancel)
	var once sync.Once
	return func() {
		once.Do(func() {
			stop()
			cancel()
			<-g.active
			g.running.Done()
		})
	}, opCtx, nil
}

// quiesce permanently closes admission, so new and waiting callers get ErrClosed. It waits up to the grace
// period for the running operations, cancels the rest and waits a little longer for them to release. ctx can
// shorten both waits. It reports nil only when quiescence is proved (zero admitted operations), and
// ErrNotQuiescent while work is still active; a bounded wait returning never implies a drain. One shared
// completion waiter publishes the drain on the quiesced signal, however often quiesce is called, so repeated
// attempts never leak a waiter.
func (g *gate) quiesce(ctx context.Context) error {
	g.mu.Lock()
	if !g.closed {
		g.closed = true
		close(g.closing)
	}
	g.waiterOnce.Do(func() {
		go func() {
			g.running.Wait()
			g.quiesceOnce.Do(func() { close(g.quiesced) })
		}()
	})
	g.mu.Unlock()

	if !waitOrExpire(ctx, g.quiesced, closeGrace) {
		g.cancel()
		waitOrExpire(ctx, g.quiesced, closeCancelWait)
	}
	select {
	case <-g.quiesced:
		g.cancel()
		return nil
	default:
		return ErrNotQuiescent
	}
}

func waitOrExpire(ctx context.Context, done <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// activeCount is the number of admitted operations, for tests and diagnostics.
func (g *gate) activeCount() int { return len(g.active) }

// waitingCount is the number of callers waiting for a slot.
func (g *gate) waitingCount() int { return int(g.waiting.Load()) }

// commitOutcome classifies the error of a commit. A commit that fails after cancellation, or on a broken
// connection, may or may not have been applied, so it is indeterminate; callers retry, which is idempotent for
// the same (chat, id). Any other failure was rolled back.
func commitOutcome(ctx context.Context, err error) error {
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil || errors.Is(err, driver.ErrBadConn):
		return ErrIndeterminate
	}
	return mapDriverError(err)
}
