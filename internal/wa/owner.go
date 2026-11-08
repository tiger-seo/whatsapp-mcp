package wa

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// Owner timing: every owner-initiated attempt gets its own watchdog, which is not a lifetime
// deadline for a healthy connection. The desired startup budget of three five-second retries is
// disabled until its safety proof exists; see the connection-owner contract.
const (
	connectWatchdog = 30 * time.Second
)

// barrierDrainBound is how long retirement waits for the store barrier to drain before it takes the forced path.
const barrierDrainBound = 10 * time.Second

// defaultStopBound is the lifecycle's shutdown timeout.
const defaultStopBound = 10 * time.Second

// Owner errors distinguish the outcomes callers must tell apart.
var (
	ErrStopping      = errors.New("the owner is stopping")
	ErrAlreadyPaired = errors.New("a device is already paired")
	ErrPairingActive = errors.New("a pairing attempt is already active")
	ErrStarted       = errors.New("the owner is already started")
)

// OwnerConfig constructs one connection owner.
type OwnerConfig struct {
	// Log carries process and upstream output; the shared bridge redacts upstream arguments.
	Log *slog.Logger
	// Store is the shared store; only LoadDevice is used, through the adapter factory.
	Store *store.Store
	// Versions is the selected-version source; the owner refreshes it before connecting.
	Versions *VersionProvider
	// Sessions builds adapter generations; nil uses the real pinned adapter.
	Sessions SessionFactory
	// ShellStartupBudget marks the long-running shells. Saved-session startup retries are denied
	// by default, so a failed or timed-out first attempt ends disconnected with a diagnostic for
	// both the shells and the pair command.
	ShellStartupBudget bool
	// StopBound is the whole orderly stop, from BeginStop to the decision to clear the marker. The command passes
	// the shutdown timeout it already uses (10 s); zero means that value. It is not a setting.
	StopBound time.Duration

	// drainBound replaces barrierDrainBound; tests only.
	drainBound time.Duration
	// trace, when set, receives the name of each retirement step as it happens; tests only.
	trace func(step string)
	// afterConnect runs right after a Connect returned, before the owner clears the recorded abort; tests only.
	afterConnect func()
	// afterPostPairPublish runs once the fresh post-pair generation is published, before its Connect; tests only.
	afterPostPairPublish func()
}

// Owner owns the one WhatsApp connection: state, epochs, attempts, watchdogs and pairing windows.
// Exactly one attempt is in flight at any time, and every attempt holds its slot until the call and
// the generation access have drained.
type connectionOwner struct {
	cfg    OwnerConfig
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	stopOnce sync.Once

	// slot is the sole connection attempt; capacity one, held until drain.
	slot chan struct{}
	// monitor tracks the sampling goroutine, which stop must end before the owner reports done.
	monitor sync.WaitGroup
	// repairSlot is the one owner-wide slot for owner-invoked app-state calls, shared by all generations.
	repairSlot chan struct{}

	// windowMu serializes window opens; window fields are guarded by mu.
	windowMu sync.Mutex

	mu    sync.Mutex
	epoch uint64
	gen   *genState
	// pairing is the current or last pairing window; a terminal window stays queryable.
	pairing *pairingWindow

	started   bool
	ready     bool
	startDone chan struct{}
	stopping  bool
	identity  bool
	// connected records a confirmed connection of the current generation.
	connected bool
	// outdated records a 405 handoff for the current generation; the 405 recovery owns reconnection.
	outdated   bool
	guardArmed bool
	unresolved bool
	unsafeStop bool
	stopErr    error
	// stopAt is the end of the stop bound; set by BeginStop.
	stopAt time.Time

	phase  Phase
	health Health

	updateCh chan struct{}
	subs     map[uint64]func(Event)
	subNext  uint64
}

// genState is one client generation with its epoch and its contexts.
//
// work is the program's own work: the pairing window's QR channel, the repair pass, version refresh. It is a
// child of the owner context, so the stop signal reaches it, and retirement cancels it early. lib is the context
// the client library gives its detached handlers and the parent of every Connect attempt; it is not a child of
// the owner context's cancellation, and only the last step of retirement cancels it, after the generation's
// network and storage are blocked. A handler that is still writing keys when a stop begins can therefore finish.
type genState struct {
	epoch      uint64
	handle     SessionHandle
	work       context.Context
	workCancel context.CancelFunc
	lib        context.Context
	libCancel  context.CancelFunc
	// abort is the recorded abort of a Connect that has not returned; it is set and cleared under
	// connectionOwner.mu. abortFired records that it was called, and abortLate that a Connect which returned
	// without error still had it called (the handlers' context was cancelled with a live socket).
	abort      context.CancelCauseFunc // guarded by connectionOwner.mu
	abortFired bool                    // guarded by connectionOwner.mu
	abortLate  bool                    // guarded by connectionOwner.mu
	// quiet is this generation's quiet-stop result, written once by retirement.
	quiet      *QuietStopResult // guarded by connectionOwner.mu
	retireOnce sync.Once
	retiring   bool // guarded by connectionOwner.mu
	timingOut  bool // guarded by connectionOwner.mu
	loggedOut  bool // guarded by connectionOwner.mu
	// paired records PairSuccess for this generation; pending515 records a 515 waiting for it.
	paired bool // guarded by connectionOwner.mu
	// refused515 records that a 515 was refused, so a later PairSuccess must not leave the phase
	// at connecting with no attempt running.
	refused515 bool // guarded by connectionOwner.mu
	pending515 *time.Timer
	reconnects int // post-pair handovers started (or refused) for this generation
	// handover records that the server's restart request was accepted: the pairing socket is about to be replaced
	// by a fresh client, so its remote close must not end the link.
	handover bool // guarded by connectionOwner.mu
	// repairDone is non-nil once this generation's repair pass was started; it closes when the pass ends.
	repairDone chan struct{} // guarded by connectionOwner.mu
}

// NewOwner creates the owner; call Start once to load the device and begin the lifecycle.
func NewOwner(cfg OwnerConfig) *connectionOwner {
	if cfg.Versions == nil {
		panic("wa: owner requires a version provider")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	o := &connectionOwner{
		cfg:        cfg,
		log:        cfg.Log,
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
		startDone:  make(chan struct{}),
		slot:       make(chan struct{}, 1),
		repairSlot: make(chan struct{}, 1),
		updateCh:   make(chan struct{}),
		subs:       make(map[uint64]func(Event)),
	}
	return o
}

// Start loads the guarded device once. A saved identity reconnects under the startup budget; a
// fresh device stays unpaired without any connection or QR until an explicit request.
func (o *connectionOwner) Start(ctx context.Context) error {
	o.mu.Lock()
	if o.stopping {
		o.mu.Unlock()
		return ErrStopping
	}
	if o.started {
		o.mu.Unlock()
		return ErrStarted
	}
	o.started = true
	o.monitor.Add(1)
	o.mu.Unlock()
	go o.runMonitor()
	defer close(o.startDone)

	if o.cfg.Store != nil {
		if err := o.cfg.Store.ArmRunGuard(ctx); err != nil {
			return err
		}
		o.mu.Lock()
		o.guardArmed = true
		o.mu.Unlock()
	}
	gen, err := o.openGeneration()
	if err != nil {
		o.mu.Lock()
		o.unsafeStop = true
		if !o.stopping {
			o.phase = PhaseStopped
		}
		o.publishLocked()
		o.mu.Unlock()
		return err
	}
	o.mu.Lock()
	if o.stopping {
		o.mu.Unlock()
		o.retireGeneration(gen, quietStopDeadline)
		return ErrStopping
	}
	o.gen = gen
	o.ready = true
	o.identity = gen.handle.HasIdentity()
	if pending := gen.handle.RecoveryPending(); pending {
		o.health.RecoveryPending = true
	}
	if o.identity {
		o.phase = PhaseConnecting
	} else {
		o.phase = PhaseIdle
	}
	o.publishLocked()
	o.mu.Unlock()
	o.watchStorageFault(gen)
	if o.identity {
		go o.runStartupSequence(gen)
	}
	// A cancelled caller context stops the owner too; BeginStop is idempotent.
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				o.BeginStop()
			case <-o.done:
			}
		}()
	}
	return nil
}

// openGeneration advances the epoch and builds one adapter generation.
func (o *connectionOwner) openGeneration() (*genState, error) {
	o.mu.Lock()
	o.epoch++
	epoch := o.epoch
	work, workCancel := context.WithCancel(o.ctx)
	// WithoutCancel alone has no Done channel and could not be cancelled at the last step of retirement.
	lib, libCancel := context.WithCancel(context.WithoutCancel(o.ctx))
	cfg := SessionConfig{
		Store:      o.cfg.Store,
		Log:        o.cfg.Log,
		Versions:   o.cfg.Versions,
		Epoch:      epoch,
		Lifetime:   work,
		Library:    lib,
		trace:      o.cfg.trace,
		Dispatch:   o.dispatch,
		RepairSlot: o.repairSlot,
	}
	o.mu.Unlock()
	factory := o.cfg.Sessions
	if factory == nil {
		factory = defaultSession
	}
	handle, err := factory(cfg)
	if err != nil {
		workCancel()
		libCancel()
		return nil, err
	}
	return &genState{epoch: epoch, handle: handle, work: work, workCancel: workCancel, lib: lib, libCancel: libCancel}, nil
}

// watchStorageFault reports a guarded-write fault and stops any running sequence.
func (o *connectionOwner) watchStorageFault(gen *genState) {
	go func() {
		select {
		case <-gen.handle.StorageFaulted():
			o.storageFault(gen)
		case <-o.ctx.Done():
		case <-gen.work.Done():
		}
	}()
}

func (o *connectionOwner) storageFault(gen *genState) {
	o.mu.Lock()
	if o.gen != gen || gen.retiring || o.stopping || o.health.StorageFault {
		o.mu.Unlock()
		return
	}
	o.health.StorageFault = true
	o.connected = false
	if w := o.pairing; w != nil && w.result == PairingActive {
		w.result = PairingFailed
		w.clearCodeLocked()
	}
	if o.phase != PhaseLoggedOut && o.phase != PhaseStopping {
		o.phase = PhaseStorageFailed
	}
	o.publishLocked()
	o.mu.Unlock()
	go o.retireGeneration(gen, quietStopDeadline)
}

// dispatch admits one upstream event of the current generation; stale epochs are denied.
func (o *connectionOwner) dispatch(epoch uint64, ev SessionEvent) {
	o.mu.Lock()
	if ev == SessionLoggedOut && o.gen != nil && o.gen.epoch == epoch {
		o.gen.loggedOut = true
	}
	if o.stopping || o.gen == nil || o.gen.epoch != epoch || o.gen.retiring {
		o.mu.Unlock()
		return
	}
	gen := o.gen
	if o.health.StorageFault || o.phase == PhaseLoggedOut {
		o.mu.Unlock()
		return
	}
	w := o.pairing
	switch ev {
	case SessionConnected:
		if gen.timingOut {
			o.mu.Unlock()
			return
		}
		o.connected = true
		o.outdated = false
		o.phase = PhaseConnected
		if o.identity {
			o.unresolved = false
		}
		o.log.Debug("connection confirmed", "event", "connection_confirmed")
	case SessionDisconnected:
		o.connected = false
		if o.phase != PhasePairing && !gen.handover {
			o.phase = PhaseDisconnected
		}
	case SessionPairSuccess:
		// PairSuccess proves the durable save happened in the guarded container.
		gen.paired = true
		o.identity = true
		if o.connected {
			o.unresolved = false
		}
		if gen.refused515 {
			o.connected = false
			o.phase = PhaseDisconnected
		} else if o.phase != PhaseConnected {
			o.phase = PhaseConnecting
		}
		if w != nil && w.result == PairingActive {
			w.result = PairingSucceeded
			w.clearCodeLocked()
		}
	case SessionPairError:
		if w != nil && w.result == PairingActive {
			w.result = PairingFailed
			w.clearCodeLocked()
		}
	case SessionLoggedOut:
		// Prevent a concurrent pairing request from reusing this generation before
		// the retirement goroutine starts.
		gen.retiring = true
		o.identity = false
		o.connected = false
		o.phase = PhaseLoggedOut
		if w != nil && w.result == PairingActive {
			w.result = PairingFailed
			w.clearCodeLocked()
		}
	case SessionClientOutdated:
		o.outdated = true
		if !o.cfg.ShellStartupBudget && o.phase == PhaseConnecting {
			o.phase = PhaseDisconnected
		}
	case SessionManualLoginReconnect:
	}
	o.publishLocked()
	o.mu.Unlock()

	switch ev {
	case SessionConnected:
		o.startRepair(gen)
	case SessionLoggedOut:
		o.log.Info("logged out by the server", "event", "logged_out", "epoch", epoch)
		// Retirement runs outside event-handler locks; the shared store stays open.
		o.cancelPending515(gen, true)
		go o.retireGeneration(gen, quietStopDeadline)
	case SessionPairError:
		o.cancelPending515(gen, true)
	case SessionPairSuccess:
		o.releasePending515(gen)
	case SessionManualLoginReconnect:
		o.tryManualReconnect(gen)
	}
}

// publishLocked replaces the update signal and wakes subscribers. Subscriber callbacks must not
// call back into the owner.
func (o *connectionOwner) publishLocked() {
	close(o.updateCh)
	o.updateCh = make(chan struct{})
	ev := Event{Phase: o.phase}
	if o.gen != nil {
		ev.Epoch = o.gen.epoch
	}
	subs := make([]func(Event), 0, len(o.subs))
	for _, fn := range o.subs {
		subs = append(subs, fn)
	}
	for _, fn := range subs {
		fn(ev)
	}
}

// Snapshot returns a copied status value.
func (o *connectionOwner) Snapshot() Status {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.statusLocked()
}

func (o *connectionOwner) statusLocked() Status {
	code := ""
	expires := time.Time{}
	if w := o.pairing; w != nil && w.result == PairingActive {
		code, expires = w.code, w.codeExpires
	}
	return Status{
		Connected:       o.connected,
		LoggedIn:        o.identity,
		PairingRequired: !o.identity && code != "" && time.Now().Before(expires),
		WAVersion:       o.cfg.Versions.Current().String(),
	}
}

// PairingSnapshot returns the current or last pairing window's copied state.
func (o *connectionOwner) PairingSnapshot() (PairingState, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pairing == nil {
		return PairingState{}, false
	}
	return o.pairing.snapshotLocked(), true
}

// Subscribe registers an event callback until the returned function is called.
func (o *connectionOwner) Subscribe(fn func(Event)) func() {
	o.mu.Lock()
	id := o.subNext
	o.subNext++
	o.subs[id] = fn
	o.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			delete(o.subs, id)
			o.mu.Unlock()
		})
	}
}

// Health returns copied device-data health flags.
func (o *connectionOwner) Health() Health {
	o.mu.Lock()
	defer o.mu.Unlock()
	h := o.health
	h.Ready = o.identity && !h.RecoveryPending && !h.StorageFault
	return h
}

// BeginStop cancels the owner lifetime and begins retiring the active generation. Done closes only
// after the generation access drained.
func (o *connectionOwner) BeginStop() {
	o.stopOnce.Do(func() {
		o.mu.Lock()
		o.stopping = true
		o.stopAt = time.Now().Add(o.stopBound())
		o.connected = false
		o.phase = PhaseStopping
		if w := o.pairing; w != nil && w.result == PairingActive {
			w.result = PairingCancelled
			w.clearCodeLocked()
		}
		// The refusal count starts, and a Connect that has not returned is aborted, before anything is
		// cancelled. Neither reaches the library context of a connection that is already up.
		if gen := o.gen; gen != nil {
			o.markRetiringLocked(gen)
			o.abortAttemptLocked(gen)
		}
		o.publishLocked()
		o.mu.Unlock()
		o.cancel()
		go o.finishStop()
	})
}

// Done closes after BeginStop and the generation drain.
func (o *connectionOwner) Done() <-chan struct{} { return o.done }

// StopError reports whether shutdown left the run marker requiring recovery.
// It is final after Done closes.
func (o *connectionOwner) StopError() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stopErr
}

func (o *connectionOwner) finishStop() {
	o.mu.Lock()
	started := o.started
	o.mu.Unlock()
	if started {
		<-o.startDone
	}
	// Pairing may be constructing a generation outside mu. Wait until it has either published
	// or retired that generation before capturing the owner state.
	o.windowMu.Lock()
	o.windowMu.Unlock()
	o.retireCurrent()
	// Connect may outlive cancellation and device admission. Do not signal completion until the
	// attempt itself returns, even if the device barrier has already drained.
	o.slot <- struct{}{}
	// A generation published while the stop was in progress is retired and checked too, before the slot is
	// released: the store never closes and the lock is never released while a socket may be live.
	o.retireCurrent()
	<-o.slot
	o.mu.Lock()
	clear := o.guardArmed && !o.unresolved && !o.unsafeStop && !o.health.StorageFault
	if o.guardArmed && !clear {
		if o.stopErr == nil {
			o.stopErr = store.ErrUnsafeRun
		}
		o.log.Error("run remains unsafe; offline recovery is required", "event", "run_guard_recovery_required")
	}
	o.mu.Unlock()
	if clear {
		if err := o.cfg.Store.ClearRunGuard(context.Background()); err != nil {
			o.mu.Lock()
			o.stopErr = err
			o.mu.Unlock()
			o.log.Error("run marker could not be cleared; restore disk space and perform offline recovery", "event", "run_guard_clear_failed", "error", err)
		}
	}
	o.monitor.Wait()
	o.sample("stop")
	o.mu.Lock()
	o.phase = PhaseStopped
	o.publishLocked()
	o.mu.Unlock()
	close(o.done)
}

func (o *connectionOwner) traceStep(step string) {
	if o.cfg.trace != nil {
		o.cfg.trace(step)
	}
}

func (o *connectionOwner) stopBound() time.Duration {
	if o.cfg.StopBound > 0 {
		return o.cfg.StopBound
	}
	return defaultStopBound
}

func (o *connectionOwner) drainBound() time.Duration {
	if o.cfg.drainBound > 0 {
		return o.cfg.drainBound
	}
	return barrierDrainBound
}

// checkQuiet runs the generation's quiet-stop check after it drained and records the result. A failed check keeps
// the marker unsafe for the whole run. A generation that never attempted Connect is not checked. The check is
// capped by the time left in the stop bound.
func (o *connectionOwner) checkQuiet(gen *genState, deadline time.Duration) {
	checker, ok := gen.handle.(interface {
		QuietStop(context.Context, time.Duration) QuietStopResult
	})
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	o.mu.Lock()
	stopAt := o.stopAt
	o.mu.Unlock()
	if !stopAt.IsZero() {
		cancel()
		ctx, cancel = context.WithDeadline(context.Background(), stopAt)
	}
	defer cancel()
	res := checker.QuietStop(ctx, deadline)
	o.traceStep("quiet_decided")
	o.mu.Lock()
	gen.quiet = &res
	if res.Reason == QuietNotChecked {
		o.mu.Unlock()
		return
	}
	if !res.Passed {
		o.unsafeStop = true
		o.health.QuietPassed = false
		if o.health.QuietReason == "" {
			o.health.QuietReason = string(res.Reason)
		}
	} else if o.health.QuietReason == "" {
		o.health.QuietPassed = true
	}
	o.mu.Unlock()
	if res.Passed {
		o.log.Info("quiet stop passed", "event", "quiet_stop_passed", "epoch", gen.epoch, "duration_ms", res.Duration.Milliseconds())
		return
	}
	o.log.Error("quiet stop failed; offline recovery is required", "event", "quiet_stop_failed", "epoch", gen.epoch,
		"reason", string(res.Reason), "library_goroutines", res.LibraryGoroutines, "network_refused", res.NetworkRefused,
		"storage_refused", res.StorageRefused, "calls_refused", res.CallsRefused, "events_dropped", res.EventsDropped,
		"duration_ms", res.Duration.Milliseconds())
}

// markRetiringLocked starts the generation's storage refusal count. It takes no lock of its own (an atomic
// store), so it may run under o.mu; no path takes o.mu while holding a barrier lock.
func (o *connectionOwner) markRetiringLocked(gen *genState) {
	if m, ok := gen.handle.(interface{ MarkRetiring() }); ok {
		m.MarkRetiring()
	}
}

// abortAttemptLocked aborts a Connect that has not returned, synchronously, through the recorded abort. The
// refusal count starts first. It is the only way the stop signal, a watchdog or retirement reaches an attempt
// context; there is no AfterFunc, which would run late in another goroutine. Call it with o.mu held.
func (o *connectionOwner) abortAttemptLocked(gen *genState) {
	if gen.abort == nil {
		return
	}
	o.markRetiringLocked(gen)
	gen.abortFired = true
	gen.abort(errAttemptAborted)
}

// retireCurrent retires whatever generation exists, repeating until none is left.
func (o *connectionOwner) retireCurrent() {
	for {
		o.mu.Lock()
		gen := o.gen
		o.mu.Unlock()
		if gen == nil {
			return
		}
		o.retireGeneration(gen, quietStopDeadline)
	}
}

// retireGeneration retires one generation in the contract order: mark it retiring and set the session's retire
// bit, end the pairing window, abort an unreturned Connect and cancel the work context, wait for the repair
// pass, then let the session disconnect, close its network fence and drain the store barrier, and only then
// cancel the library context. It is idempotent and never closes the shared store.
func (o *connectionOwner) retireGeneration(gen *genState, deadline time.Duration) {
	gen.retireOnce.Do(func() {
		o.mu.Lock()
		gen.retiring = true
		o.markRetiringLocked(gen)
		o.mu.Unlock()
		if b, ok := gen.handle.(interface{ BeginRetire() }); ok {
			b.BeginRetire()
		}
		o.endPairingWindow(gen)
		o.mu.Lock()
		o.abortAttemptLocked(gen)
		repairDone := gen.repairDone
		o.mu.Unlock()
		gen.workCancel()
		o.traceStep("work_cancelled")
		if repairDone != nil {
			<-repairDone
		}
		// The barrier waits for admitted calls, which a pinned handler may still be finishing. A drain that does
		// not finish inside its bound is the forced path: the library context is released so those calls fail,
		// and the stop is unsafe.
		drainCtx, drainCancel := context.WithTimeout(context.Background(), o.drainBound())
		err := gen.handle.Retire(drainCtx)
		drainCancel()
		if err != nil {
			o.log.Warn("generation drain did not quiesce", "event", "owner_drain_failed")
			o.mu.Lock()
			o.unsafeStop = true
			o.mu.Unlock()
			// Storage is not blocked yet; releasing the handlers' context is the only way left to end them.
			gen.libCancel()
			o.traceStep("lib_cancelled")
			if errors.Is(err, context.DeadlineExceeded) {
				_ = gen.handle.Retire(context.Background())
			}
		} else {
			// Storage is blocked now, so a handler released by this cancel can only be refused and counted.
			gen.libCancel()
			o.traceStep("lib_cancelled")
		}
		if err == nil {
			o.checkQuiet(gen, deadline)
		}
		// The watcher can exit on cancellation before a previously admitted write
		// reports its fault. Retirement has drained those writes, so inspect the
		// latched signal before releasing this generation or clearing the marker.
		select {
		case <-gen.handle.StorageFaulted():
			o.mu.Lock()
			o.health.StorageFault = true
			o.unsafeStop = true
			o.mu.Unlock()
		default:
		}
		o.mu.Lock()
		confirmed := gen.loggedOut
		o.mu.Unlock()
		if cleanup, ok := gen.handle.(interface {
			FinishRevocation(context.Context, bool) error
		}); ok {
			if err := cleanup.FinishRevocation(context.Background(), confirmed); err != nil {
				o.mu.Lock()
				o.unsafeStop = true
				o.stopErr = err
				o.mu.Unlock()
				o.log.Error("revocation cleanup failed; offline recovery is required", "event", "revocation_cleanup_failed", "error", err)
			}
		} else if confirmed && o.cfg.Store != nil {
			o.mu.Lock()
			o.unsafeStop = true
			o.mu.Unlock()
		}
		o.mu.Lock()
		if o.gen == gen {
			o.gen = nil
		}
		o.publishLocked()
		o.mu.Unlock()
	})
}

// attemptOutcome is one attempt's terminal result.
type attemptOutcome int

const (
	outcomeConnected attemptOutcome = iota
	outcomeFailed
	outcomeTimeout
	outcomeStopped
	outcomeLoggedOut
	outcomeFault
	outcomeOutdated
)

func (k attemptOutcome) terminal() bool {
	return k == outcomeConnected || k == outcomeStopped || k == outcomeLoggedOut ||
		k == outcomeFault || k == outcomeOutdated
}

// runStartupSequence refreshes the version, then connects with the watchdog. With the shell budget
// a failed or timed-out attempt is final, because retries are denied by default. The identity is
// retained and the owner stays disconnected without pairing.
func (o *connectionOwner) runStartupSequence(gen *genState) {
	if err := o.cfg.Versions.Refresh(gen.work); err != nil {
		o.log.Warn("version discovery failed; keeping the last usable version", "event", "version_refresh_failed", "reason", versionFailureReason(err))
	}
	o.mu.Lock()
	if o.phase == PhaseIdle {
		o.phase = PhaseConnecting
		o.publishLocked()
	}
	o.mu.Unlock()
	for {
		select {
		case <-gen.handle.StorageFaulted():
			o.storageFault(gen)
		default:
		}
		if o.terminalState() {
			if !o.cfg.ShellStartupBudget {
				o.setPhaseIf(PhaseDisconnected, PhaseConnecting)
			}
			return
		}
		outcome := o.runAttempt(gen, attemptStartup)
		if outcome.terminal() {
			if outcome == outcomeOutdated && !o.cfg.ShellStartupBudget {
				o.setPhaseIf(PhaseDisconnected, PhaseConnecting)
			}
			return
		}
		// A failed or timed-out first attempt is final: a retry would be another production
		// Connect while detached upstream work of the earlier one cannot be excluded.
		if o.cfg.ShellStartupBudget && !o.stoppedOrTerminal() {
			o.denyReconnect("startup_retry")
		}
		o.setPhaseIf(PhaseDisconnected, PhaseConnecting)
		return
	}
}

// denyReconnect reports that an automatic Connect was refused. The diagnostic is fixed text.
func (o *connectionOwner) denyReconnect(path string) {
	o.log.Warn("automatic reconnect is not available; the saved identity was kept and offline recovery is required before the next attempt",
		"event", "reconnect_denied", "path", path)
}

func (o *connectionOwner) setPhaseIf(want, from Phase) {
	o.mu.Lock()
	if o.phase == from {
		o.phase = want
		o.publishLocked()
	}
	o.mu.Unlock()
}

func (o *connectionOwner) terminalState() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.connected || o.stopping || o.phase == PhaseLoggedOut || o.health.StorageFault || o.outdated
}

func (o *connectionOwner) stoppedOrTerminal() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stopping || o.phase == PhaseLoggedOut || o.health.StorageFault || o.outdated
}

// runAttempt performs one connection attempt under its own 30-second watchdog. The watchdog is
// disarmed by the connection confirmation; a timeout cancels the dial first and disconnects after,
// so socketLock can unwind. The attempt slot is held until the call returned and drained.
func (o *connectionOwner) runAttempt(gen *genState, mode attemptMode) attemptOutcome {
	select {
	case o.slot <- struct{}{}:
	default:
		return outcomeFailed
	}
	defer func() { <-o.slot }()
	o.mu.Lock()
	stopped := o.stopping || o.gen != gen || gen.retiring || o.health.StorageFault || o.outdated || o.phase == PhaseLoggedOut || o.connected
	o.mu.Unlock()
	if stopped {
		return outcomeStopped
	}
	return o.attempt(gen, mode)
}

// attemptMode distinguishes a startup attempt, where a disconnect during the wait counts as
// failure, from a manual 515 reconnect, which starts from a dropped socket and relies on the
// connect error and its watchdog.
type attemptMode int

const (
	attemptStartup attemptMode = iota
	// attemptPostPair starts from an intentionally dropped socket, so a disconnect event does not
	// end it; only the connect result, the watchdog and the terminal states do.
	attemptPostPair
)

func (o *connectionOwner) attempt(gen *genState, mode attemptMode) attemptOutcome {
	// The stop check and the recording of the abort share one critical section, so a stop either sees the
	// abort and calls it, or is seen here and prevents the Connect.
	o.mu.Lock()
	gen.timingOut = false
	if o.stopping || gen.retiring {
		o.mu.Unlock()
		return outcomeStopped
	}
	attemptCtx, abort := context.WithCancelCause(gen.lib)
	gen.abort, gen.abortFired = abort, false
	o.mu.Unlock()
	fired := make(chan struct{})
	finished := make(chan struct{})
	timer := time.AfterFunc(connectWatchdog, func() {
		defer close(finished)
		o.mu.Lock()
		active := o.gen == gen && !gen.retiring && !o.connected && !o.stopping && !o.health.StorageFault && o.phase != PhaseLoggedOut
		if active {
			gen.timingOut = true
			// Only a Connect that has not returned is aborted. After it returned the handlers' context is
			// left alone and the watchdog just disconnects.
			o.abortAttemptLocked(gen)
		}
		o.mu.Unlock()
		if active {
			o.log.Warn("connection attempt timed out", "event", "connect_timeout")
			gen.handle.Disconnect()
			close(fired)
		}
	})
	connected := false
	defer func() {
		if !timer.Stop() {
			<-finished
		}
		_ = connected
	}()

	err := gen.handle.Connect(attemptCtx)
	if o.cfg.afterConnect != nil {
		o.cfg.afterConnect()
	}
	o.mu.Lock()
	if err == nil && gen.abortFired {
		gen.abortLate = true
	}
	gen.abort = nil
	o.mu.Unlock()
	if err != nil {
		return outcomeFailed
	}
	for {
		o.mu.Lock()
		done, has := attemptOutcome(0), false
		switch {
		case o.connected:
			done, has = outcomeConnected, true
		case o.stopping:
			done, has = outcomeStopped, true
		case o.phase == PhaseLoggedOut:
			done, has = outcomeLoggedOut, true
		case o.health.StorageFault:
			done, has = outcomeFault, true
		case o.outdated:
			done, has = outcomeOutdated, true
		case mode == attemptStartup && o.phase == PhaseDisconnected && !o.connected:
			done, has = outcomeFailed, true
		}
		if has {
			o.mu.Unlock()
			if done == outcomeConnected {
				connected = true
			}
			return done
		}
		ch := o.updateCh
		o.mu.Unlock()
		select {
		case <-ch:
		case <-fired:
			return outcomeTimeout
		case <-o.ctx.Done():
			return outcomeStopped
		}
	}
}

// tryManualReconnect handles the post-login 515. The 515 is only a request. It waits (bounded) for PairSuccess,
// which proves the pinned pairing work reached its durable save and event dispatch, and any failure or stop
// refuses it. Once accepted, the pairing generation is retired and checked and a fresh client makes the one
// connection that completes the link.
func (o *connectionOwner) tryManualReconnect(gen *genState) {
	o.mu.Lock()
	if o.stopping || o.gen != gen || gen.retiring || o.phase == PhaseLoggedOut || o.health.StorageFault {
		o.mu.Unlock()
		return
	}
	if gen.reconnects > 0 || gen.pending515 != nil && !gen.paired {
		o.mu.Unlock() // duplicates share the one request
		return
	}
	if gen.paired {
		o.beginHandoverLocked(gen)
		o.mu.Unlock()
		go o.runPostPairHandover(gen)
		return
	}
	gen.pending515 = time.AfterFunc(connectWatchdog, func() { o.cancelPending515(gen, true) })
	o.mu.Unlock()
}

// cancelPending515 drops a waiting 515 request; refuse reports the denial.
func (o *connectionOwner) cancelPending515(gen *genState, refuse bool) {
	o.mu.Lock()
	timer := gen.pending515
	gen.pending515 = nil
	waiting := timer != nil && !gen.paired
	if timer != nil {
		timer.Stop()
	}
	// The timer can fire long after the generation ended; only the current, live generation
	// may change the phase.
	live := o.gen == gen && !gen.retiring && !o.stopping && !o.health.StorageFault && o.phase != PhaseLoggedOut
	if waiting {
		gen.reconnects++ // a later PairSuccess must not revive the refused request
		gen.refused515 = true
		if live {
			o.connected = false
			o.phase = PhaseDisconnected
			o.publishLocked()
		}
	}
	o.mu.Unlock()
	if waiting && refuse && live {
		o.denyReconnect("login_515")
	}
}

// releasePending515 starts the reconnect for a 515 that arrived before PairSuccess.
func (o *connectionOwner) releasePending515(gen *genState) {
	o.mu.Lock()
	timer := gen.pending515
	gen.pending515 = nil
	if timer == nil || gen.reconnects > 0 || o.stopping || o.gen != gen || gen.retiring {
		o.mu.Unlock()
		return
	}
	timer.Stop()
	o.beginHandoverLocked(gen)
	o.mu.Unlock()
	go o.runPostPairHandover(gen)
}

// beginHandoverLocked accepts the server's restart request for a paired generation. The owner is connecting from
// this moment, before the attempt slot is taken: the server's close of the pairing socket can follow the request
// within milliseconds, and it must not read as a failed link. Call it with o.mu held.
func (o *connectionOwner) beginHandoverLocked(gen *genState) {
	gen.reconnects++
	gen.handover = true
	o.connected = false
	o.phase = PhaseConnecting
	o.publishLocked()
}

// runPostPairHandover completes a pairing on a fresh client. It takes the attempt slot, retires and checks the
// pairing generation (which ends its pairing window), and only after that check passed opens a new generation
// from the saved identity and makes the one connection under the attempt watchdog. A failed check, a failed
// open, a failure, a stall and a cancellation are final: no second attempt follows. The slot is held across the
// whole path, so `finishStop` retires whatever generation was published meanwhile.
func (o *connectionOwner) runPostPairHandover(pairGen *genState) {
	select {
	case o.slot <- struct{}{}:
	case <-pairGen.work.Done():
		return
	}
	defer func() { <-o.slot }()
	if o.postPairHalted(pairGen) {
		return
	}
	o.retireGeneration(pairGen, postPairQuietDeadline)
	o.mu.Lock()
	failed := o.unsafeStop || o.health.StorageFault || pairGen.loggedOut || o.phase == PhaseLoggedOut
	if res := pairGen.quiet; res != nil && !res.Passed {
		failed = true
	}
	stopped := o.stopping
	o.mu.Unlock()
	if stopped {
		return
	}
	if failed {
		o.failPostPair()
		return
	}
	fresh, err := o.openGeneration()
	if err != nil {
		o.failPostPair()
		return
	}
	// Publication and the stop check share one critical section: a stop that began first finds no generation
	// to wait for, so this one is discarded without ever connecting.
	o.mu.Lock()
	if o.stopping {
		o.mu.Unlock()
		o.retireGeneration(fresh, quietStopDeadline)
		return
	}
	o.gen = fresh
	o.connected = false
	o.phase = PhaseConnecting
	o.identity = fresh.handle.HasIdentity()
	o.publishLocked()
	o.mu.Unlock()
	o.watchStorageFault(fresh)
	if o.cfg.afterPostPairPublish != nil {
		o.cfg.afterPostPairPublish()
	}
	if !fresh.handle.HasIdentity() {
		o.failPostPair()
		return
	}
	if outcome := o.attempt(fresh, attemptPostPair); outcome == outcomeConnected {
		return
	}
	if !o.stoppedOrTerminal() {
		o.denyReconnect("login_515")
	}
	o.setPhaseIf(PhaseDisconnected, PhaseConnecting)
}

// postPairHalted reports whether the paired generation can no longer hand over: a stop, a retirement, a logout,
// a storage fault or a 405 landed while the handover waited for the slot.
func (o *connectionOwner) postPairHalted(gen *genState) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stopping || o.gen != gen || gen.retiring || o.phase == PhaseLoggedOut || o.health.StorageFault || o.outdated
}

// failPostPair is the incomplete-link path: the pairing stays unresolved, so the marker stays unsafe, and the
// command reports that linking could not be completed safely.
func (o *connectionOwner) failPostPair() {
	if !o.stoppedOrTerminal() {
		o.denyReconnect("login_515")
	}
	o.setPhaseIf(PhaseDisconnected, PhaseConnecting)
}

// CancelPairing cancels the active pairing window, if any.
func (o *connectionOwner) CancelPairing() {
	o.mu.Lock()
	w := o.pairing
	active := w != nil && w.result == PairingActive
	o.mu.Unlock()
	if active {
		w.finish(PairingCancelled, false)
	}
}

// startRepair runs the generation's repair pass once, after a confirmed connection, when the
// session has pending collections. The pass is bounded by the generation lifetime and joined by
// retirement, so it never outlives its client.
func (o *connectionOwner) startRepair(gen *genState) {
	runner, ok := gen.handle.(RepairRunner)
	if !ok || !gen.handle.RecoveryPending() {
		return
	}
	o.mu.Lock()
	if gen.repairDone != nil || gen.retiring || o.stopping || o.gen != gen {
		o.mu.Unlock()
		return
	}
	done := make(chan struct{})
	gen.repairDone = done
	o.mu.Unlock()
	go func() {
		defer close(done)
		err := runner.RunRepairs(gen.work)
		o.mu.Lock()
		if o.gen == gen {
			o.health.RecoveryPending = gen.handle.RecoveryPending()
			o.publishLocked()
		}
		o.mu.Unlock()
		if err != nil && gen.work.Err() == nil {
			o.log.Warn("app-state repair is incomplete; the marker is kept", "event", "repair_incomplete")
		}
	}()
}

// monitorInterval is the fixed cadence of the runtime samples. It is an observation cadence for an
// operator, not a resource ceiling.
const monitorInterval = time.Minute

// runMonitor emits one coarse sample at start and then once per interval until stop. Nothing is
// kept between samples.
func (o *connectionOwner) runMonitor() {
	defer o.monitor.Done()
	o.sample("start")
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-o.ctx.Done():
			return
		case <-ticker.C:
			o.sample("periodic")
		}
	}
}

// sample logs the process heap and goroutine count at debug level. The fields are numbers and a
// fixed reason; no account, QR, key, ticket or body data is reachable from here.
func (o *connectionOwner) sample(reason string) {
	if !o.log.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	o.log.Debug("runtime sample", "event", "runtime_sample", "reason", reason,
		"heap_alloc_bytes", m.HeapAlloc, "goroutines", runtime.NumGoroutine())
}
