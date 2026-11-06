package wa

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// repairDeadline is how long the owner waits for one app-state fetch. It bounds the caller, not the
// SDK call: the call keeps its slot until it returns.
const repairDeadline = 10 * time.Second

var (
	errRepairDeadline = errors.New("app-state repair exceeded its deadline")
	errRepairBusy     = errors.New("an earlier app-state call is still outstanding")
	errRepairFault    = errors.New("a guarded write fault requires a restart")
)

// repairBarrier is the part of the guarded device barrier the controller uses.
type repairBarrier interface {
	PendingRepairs(context.Context) ([]string, error)
	BeginRepair(context.Context, string) (context.Context, *store.RepairTicket, error)
	CompleteRepair(context.Context, *store.RepairTicket) error
	CancelRepair(*store.RepairTicket)
	DeferredCollections() []string
	StorageFault() bool
}

var _ repairBarrier = (*store.DeviceBarrier)(nil)

// repairController recovers the pending app-state collections of one generation after an authorized
// connection: one full resync per collection per generation, a ten-second caller wait, and a marker
// that is cleared only by a fenced, ticketed completion. It never retries on its own.
type repairController struct {
	barrier repairBarrier
	// fetch performs one app-state call. full requests a complete resync of the collection.
	fetch func(ctx context.Context, name string, full bool) error
	// slot is shared by every generation of the owner: at most one owner-invoked call is outstanding,
	// and it is released only when that invocation returns, not when the caller gives up.
	slot     chan struct{}
	deadline time.Duration
	log      *slog.Logger

	mu        sync.Mutex
	attempted map[string]bool
}

func newRepairController(b repairBarrier, fetch func(context.Context, string, bool) error, slot chan struct{}, log *slog.Logger) *repairController {
	if slot == nil {
		slot = make(chan struct{}, 1)
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &repairController{barrier: b, fetch: fetch, slot: slot, deadline: repairDeadline, log: log, attempted: map[string]bool{}}
}

// call runs one fetch under the slot with a bounded caller wait. A late result is discarded: the
// caller has already returned and the slot is released by the goroutine that owns the invocation.
func (c *repairController) call(ctx context.Context, name string, full bool) error {
	select {
	case c.slot <- struct{}{}:
	default:
		return errRepairBusy
	}
	callCtx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()
	result := make(chan error, 1) // delivery never blocks the invocation
	go func() {
		err := c.fetch(callCtx, name, full)
		<-c.slot // released only after the invocation returned
		result <- err
	}()
	select {
	case err := <-result:
		// Recheck the deadline and cancellation before accepting a success.
		if err == nil && callCtx.Err() != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errRepairDeadline
		}
		return err
	case <-callCtx.Done():
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errRepairDeadline
	}
}

// run repairs the pending collections once, then replays coalesced ordinary demand if recovery is
// complete. A failure leaves its marker in place and does not stop the other collections, except a
// write fault or a busy slot, which stop the run.
func (c *repairController) run(ctx context.Context) error {
	pending, err := c.barrier.PendingRepairs(ctx)
	if err != nil {
		return err
	}
	var first error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}
	for _, name := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.barrier.StorageFault() {
			return errRepairFault
		}
		c.mu.Lock()
		spent := c.attempted[name]
		c.attempted[name] = true
		c.mu.Unlock()
		if spent {
			continue // the one full resync of this collection in this generation is used
		}
		rctx, ticket, err := c.barrier.BeginRepair(ctx, name)
		if err != nil {
			c.log.Warn("repair could not start", "event", "repair_begin_failed", "collection", name)
			note(err)
			if errors.Is(err, store.ErrRepairFault) {
				return err
			}
			continue
		}
		err = c.call(rctx, name, true)
		if errors.Is(err, errRepairBusy) {
			// Nothing was invoked, so the budget is untouched; the marker stays for a later run.
			c.mu.Lock()
			c.attempted[name] = false
			c.mu.Unlock()
			c.barrier.CancelRepair(ticket)
			note(err)
			return first
		}
		if err == nil && c.barrier.StorageFault() {
			err = errRepairFault
		}
		if err != nil {
			// Timeout, failure or fault: invalidate the ticket, keep the marker, discard late results.
			c.barrier.CancelRepair(ticket)
			c.log.Warn("repair did not complete; the marker is kept", "event", "repair_incomplete", "collection", name)
			note(err)
			if errors.Is(err, errRepairFault) {
				return err
			}
			continue
		}
		if err := c.barrier.CompleteRepair(ctx, ticket); err != nil {
			c.log.Warn("repair clear was rejected; the marker is kept", "event", "repair_clear_rejected", "collection", name)
			note(err)
		}
	}
	if first != nil {
		return first
	}
	return c.replay(ctx)
}

// replay answers the ordinary collection demand that was deferred during recovery, incrementally
// through the same slot. A failed replay waits for new demand instead of spinning.
func (c *repairController) replay(ctx context.Context) error {
	remaining, err := c.barrier.PendingRepairs(ctx)
	if err != nil || len(remaining) != 0 || c.barrier.StorageFault() {
		return err
	}
	for _, name := range c.barrier.DeferredCollections() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.call(ctx, name, false); err != nil {
			c.log.Warn("deferred sync replay failed; waiting for new demand", "event", "repair_replay_failed", "collection", name)
			if errors.Is(err, errRepairBusy) {
				return err
			}
		}
	}
	return nil
}
