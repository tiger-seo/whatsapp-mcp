package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
	"time"
)

// gate admits store operations: at most 16 run at once, at most 64 callers wait, and a caller waits for at most
// 5 s or its own deadline.
type gate struct {
	mu             sync.Mutex
	closed         bool
	closing        chan struct{} // closed by quiesce, to wake every waiter
	running        sync.WaitGroup
	active         int
	waiting        int
	readers        int
	changed        chan struct{}
	tool           bool
	toolReader     bool
	toolCancel     context.CancelFunc
	toolGeneration uint64
	toolWatch      *time.Timer
	toolFailed     bool
	toolFailure    chan struct{}
	lastCheckpoint time.Time

	ctx    context.Context // cancelled by quiesce after the grace period
	cancel context.CancelFunc

	quiesced    chan struct{} // closed exactly once when admission is closed and running has drained
	quiesceOnce sync.Once     // closes quiesced exactly once
	waiterOnce  sync.Once     // starts the one completion waiter, however often quiesce is called
}

func newGate() *gate {
	g := &gate{
		closing:     make(chan struct{}),
		quiesced:    make(chan struct{}),
		changed:     make(chan struct{}),
		toolFailure: make(chan struct{}),
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
func (g *gate) notify() {
	close(g.changed)
	g.changed = make(chan struct{})
}

// enter admits one operation. release must be called once. opCtx is the caller's context with the 10 s
// operation limit, and it is cancelled when the store closes.
func (g *gate) enter(ctx context.Context) (release func(), opCtx context.Context, err error) {
	return g.enterOrdinary(ctx, false)
}

func (g *gate) enterRead(ctx context.Context) (func(), context.Context, error) {
	return g.enterOrdinary(ctx, true)
}

// nestedReader accounts for a reader used within an already admitted device lease.
func (g *gate) nestedReader() func() {
	g.mu.Lock()
	if g.toolReader && g.readers >= readConns-1 && g.toolCancel != nil {
		g.toolCancel()
	}
	g.readers++
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		g.readers--
		g.notify()
		g.mu.Unlock()
	}
}

func (g *gate) enterOrdinary(ctx context.Context, reader bool) (func(), context.Context, error) {
	g.mu.Lock()
	registered := false
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		if g.closed || ctx.Err() != nil {
			if registered {
				g.waiting--
			}
			closed := g.closed
			g.mu.Unlock()
			if closed {
				return nil, nil, ErrClosed
			}
			return nil, nil, ctx.Err()
		}
		if reader && g.toolReader && g.readers >= readConns-1 && g.toolCancel != nil {
			g.toolCancel()
		}
		if g.active < maxActive {
			if registered {
				g.waiting--
			}
			g.active++
			if reader {
				g.readers++
			}
			g.running.Add(1)
			g.mu.Unlock()
			break
		}
		if g.toolCancel != nil {
			g.toolCancel()
		}
		if !registered {
			cap := maxWaiting
			if g.tool {
				cap++
			}
			if g.waiting >= cap {
				g.mu.Unlock()
				return nil, nil, ErrBusy
			}
			g.waiting++
			registered = true
			timer = time.NewTimer(admissionWait)
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-changed:
		case <-g.closing:
		case <-ctx.Done():
		case <-timer.C:
			g.mu.Lock()
			g.waiting--
			if g.toolReader && g.toolCancel != nil && !g.toolFailed {
				g.toolFailed = true
				close(g.toolFailure)
			}
			g.mu.Unlock()
			return nil, nil, ErrBusy
		}
		g.mu.Lock()
	}
	opCtx, cancel := context.WithTimeout(ctx, operationLimit)
	stop := context.AfterFunc(g.ctx, cancel)
	var once sync.Once
	return func() {
		once.Do(func() {
			stop()
			cancel()
			g.mu.Lock()
			g.active--
			if reader {
				g.readers--
			}
			g.notify()
			g.running.Done()
			g.mu.Unlock()
		})
	}, opCtx, nil
}

// enterTool reserves the shared tool place without waiting. Checkpoints use a
// non-reader place; only reader tools are cancelled by ordinary pressure.
func (g *gate) enterTool(ctx context.Context, reader bool) (func(), context.Context, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if g.toolFailed || g.tool || g.waiting != 0 || g.active == maxActive || (reader && g.readers >= readConns) {
		return nil, nil, ErrBusy
	}
	var opCtx context.Context
	var cancel context.CancelFunc
	if reader {
		opCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
	} else {
		opCtx, cancel = context.WithCancel(ctx)
	}
	g.tool, g.toolReader = true, reader
	g.toolGeneration++
	generation := g.toolGeneration
	stopWatch := func() bool { return true }
	if reader {
		g.toolCancel = cancel
		drainWait := admissionWait
		stopWatch = context.AfterFunc(opCtx, func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if !g.tool || !g.toolReader || g.toolGeneration != generation {
				return
			}
			g.toolWatch = time.AfterFunc(drainWait, func() {
				g.mu.Lock()
				defer g.mu.Unlock()
				if g.tool && g.toolReader && g.toolGeneration == generation && !g.toolFailed {
					g.toolFailed = true
					close(g.toolFailure)
				}
			})
		})
	}
	g.active++
	g.running.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			stopWatch()
			cancel()
			g.mu.Lock()
			if g.toolWatch != nil {
				g.toolWatch.Stop()
				g.toolWatch = nil
			}
			g.tool, g.toolReader, g.toolCancel = false, false, nil
			g.active--
			g.notify()
			g.running.Done()
			g.mu.Unlock()
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
		if g.toolCancel != nil {
			g.toolCancel()
		}
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
func (g *gate) activeCount() int { g.mu.Lock(); defer g.mu.Unlock(); return g.active }

// waitingCount is the number of callers waiting for a slot.
func (g *gate) waitingCount() int { g.mu.Lock(); defer g.mu.Unlock(); return g.waiting }

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
