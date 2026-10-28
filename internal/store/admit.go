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
// 5 s or its own deadline. A second semaphore of 2 slots limits the deep sorted listings.
type gate struct {
	active  chan struct{}
	deep    chan struct{}
	waiting atomic.Int32

	mu      sync.Mutex
	closed  bool
	closing chan struct{} // closed by close, to wake every waiter
	running sync.WaitGroup

	ctx    context.Context // cancelled by close after the grace period
	cancel context.CancelFunc
}

func newGate() *gate {
	g := &gate{
		active:  make(chan struct{}, maxActive),
		deep:    make(chan struct{}, deepSlots),
		closing: make(chan struct{}),
	}
	g.ctx, g.cancel = context.WithCancel(context.Background())
	return g
}

func (g *gate) isClosed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}

// acquire takes a slot of sem. A free slot is taken at once, without counting as a waiter, so a full cap of
// waiters for one semaphore never refuses a caller of another that has room. Otherwise the caller waits, counted
// toward the 64-waiter cap, until a slot frees, wait ends, admissionWait passes or the gate closes.
func (g *gate) acquire(wait context.Context, sem chan struct{}) error {
	select {
	case <-g.closing:
		return ErrClosed
	default:
	}
	select {
	case sem <- struct{}{}:
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
	case sem <- struct{}{}:
		return nil
	case <-g.closing:
		return ErrClosed
	case <-timer.C:
		return ErrBusy
	case <-wait.Done():
		return wait.Err()
	}
}

// enter admits one operation. release must be called once. opCtx is the caller's context with the 10 s
// operation limit, and it is cancelled when the store closes.
func (g *gate) enter(ctx context.Context) (release func(), opCtx context.Context, err error) {
	return g.enterWithin(ctx, ctx)
}

// enterWithin is enter with a separate context for the wait, so a caller that already spent part of its
// admission wait on a deep slot gives both waits one deadline.
func (g *gate) enterWithin(wait, ctx context.Context) (func(), context.Context, error) {
	if err := g.acquire(wait, g.active); err != nil {
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

// enterDeep takes one of the 2 slots for a deep sorted listing. A caller waiting for it counts toward the
// waiter cap and holds no admission slot. Callers take it before any database connection.
func (g *gate) enterDeep(ctx context.Context) (release func(), err error) {
	if err := g.acquire(ctx, g.deep); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { <-g.deep }) }, nil
}

// close marks the gate closed, so new and waiting callers get ErrClosed, waits up to the grace period for running
// operations, cancels the rest and waits a little longer for them to release. ctx can shorten both waits.
func (g *gate) close(ctx context.Context) error {
	g.mu.Lock()
	if !g.closed {
		g.closed = true
		close(g.closing)
	}
	g.mu.Unlock()

	done := make(chan struct{})
	go func() {
		g.running.Wait()
		close(done)
	}()
	if waitOrExpire(ctx, done, closeGrace) {
		g.cancel()
		return nil
	}
	g.cancel()
	waitOrExpire(ctx, done, closeCancelWait)
	return nil
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
