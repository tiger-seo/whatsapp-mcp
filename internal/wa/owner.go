package wa

import (
	"context"
	"errors"
	"log/slog"
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
	// PostPairReconnect enables the one owner-initiated reconnect that completes a fresh pairing
	// after the server's 515. It is off until the path has its safety proof and owner review; with
	// it off the 515 is refused and linking ends unfinished. It is a code-level switch, not a setting.
	PostPairReconnect bool
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

	phase  Phase
	health Health

	updateCh chan struct{}
	subs     map[uint64]func(Event)
	subNext  uint64
}

// genState is one client generation with its epoch and lifetime.
type genState struct {
	epoch      uint64
	handle     SessionHandle
	lifetime   context.Context
	cancel     context.CancelFunc
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
	reconnects int // post-pair reconnects started for this generation
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
		cfg:       cfg,
		log:       cfg.Log,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		startDone: make(chan struct{}),
		slot:      make(chan struct{}, 1),
		updateCh:  make(chan struct{}),
		subs:      make(map[uint64]func(Event)),
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
	o.mu.Unlock()
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
		o.retireGeneration(gen)
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
	lifetime, cancel := context.WithCancel(o.ctx)
	cfg := SessionConfig{
		Store:    o.cfg.Store,
		Log:      o.cfg.Log,
		Versions: o.cfg.Versions,
		Epoch:    epoch,
		Lifetime: lifetime,
		Dispatch: o.dispatch,
	}
	o.mu.Unlock()
	factory := o.cfg.Sessions
	if factory == nil {
		factory = defaultSession
	}
	handle, err := factory(cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	return &genState{epoch: epoch, handle: handle, lifetime: lifetime, cancel: cancel}, nil
}

// watchStorageFault reports a guarded-write fault and stops any running sequence.
func (o *connectionOwner) watchStorageFault(gen *genState) {
	go func() {
		select {
		case <-gen.handle.StorageFaulted():
			o.storageFault(gen)
		case <-o.ctx.Done():
		case <-gen.lifetime.Done():
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
	go o.retireGeneration(gen)
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
		if o.phase != PhasePairing {
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
	case SessionLoggedOut:
		// Retirement runs outside event-handler locks; the shared store stays open.
		o.cancelPending515(gen, true)
		go o.retireGeneration(gen)
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
		o.connected = false
		o.phase = PhaseStopping
		if w := o.pairing; w != nil && w.result == PairingActive {
			w.result = PairingCancelled
			w.clearCodeLocked()
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
	o.mu.Lock()
	gen := o.gen
	o.mu.Unlock()
	if gen != nil {
		o.retireGeneration(gen)
	}
	// Connect may outlive cancellation and device admission. Do not signal completion until the
	// attempt itself returns, even if the device barrier has already drained.
	o.slot <- struct{}{}
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
	o.mu.Lock()
	o.phase = PhaseStopped
	o.publishLocked()
	o.mu.Unlock()
	close(o.done)
}

// retireGeneration cancels, disconnects and drains one generation. It is idempotent and never
// closes the shared store.
func (o *connectionOwner) retireGeneration(gen *genState) {
	gen.retireOnce.Do(func() {
		o.mu.Lock()
		gen.retiring = true
		o.mu.Unlock()
		gen.cancel()
		gen.handle.Disconnect()
		if err := gen.handle.Retire(context.Background()); err != nil {
			o.log.Warn("generation drain did not quiesce", "event", "owner_drain_failed")
			o.mu.Lock()
			o.unsafeStop = true
			o.mu.Unlock()
		}
		if upstream, ok := gen.handle.(interface{ UpstreamDrainUnproven() bool }); ok && upstream.UpstreamDrainUnproven() {
			o.mu.Lock()
			o.unsafeStop = true
			o.mu.Unlock()
			o.log.Error("upstream handlers may outlive retirement; offline recovery is required", "event", "upstream_drain_unproven")
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
	if err := o.cfg.Versions.Refresh(gen.lifetime); err != nil {
		o.log.Warn("version discovery failed; keeping the last usable version", "event", "version_refresh_failed")
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
	o.mu.Lock()
	gen.timingOut = false
	o.mu.Unlock()
	attemptCtx, cancel := context.WithCancel(gen.lifetime)
	fired := make(chan struct{})
	finished := make(chan struct{})
	timer := time.AfterFunc(connectWatchdog, func() {
		defer close(finished)
		o.mu.Lock()
		active := o.gen == gen && !gen.retiring && !o.connected && !o.stopping && !o.health.StorageFault && o.phase != PhaseLoggedOut
		if active {
			gen.timingOut = true
		}
		o.mu.Unlock()
		if active {
			o.log.Warn("connection attempt timed out", "event", "connect_timeout")
			cancel()
			gen.handle.Disconnect()
			close(fired)
		}
	})
	connected := false
	defer func() {
		if !timer.Stop() {
			<-finished
		}
		if !connected {
			cancel()
		}
	}()

	if err := gen.handle.Connect(attemptCtx); err != nil {
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

// tryManualReconnect handles the post-login 515. Unless the candidate path is enabled it refuses:
// another production Connect while detached upstream work of the earlier one cannot be excluded.
// The owner then stays disconnected, keeps the unsafe marker and reports that linking was not
// completed safely.
//
// With the candidate enabled the 515 is only a request. It waits (bounded) for PairSuccess, which
// proves the pinned pairing work reached its durable save and event dispatch, and any failure or
// stop refuses it.
func (o *connectionOwner) tryManualReconnect(gen *genState) {
	o.mu.Lock()
	if o.stopping || o.gen != gen || gen.retiring || o.phase == PhaseLoggedOut || o.health.StorageFault {
		o.mu.Unlock()
		return
	}
	if !o.cfg.PostPairReconnect {
		gen.refused515 = true
		o.connected = false
		if o.phase != PhaseStopping {
			o.phase = PhaseDisconnected
		}
		o.publishLocked()
		o.mu.Unlock()
		o.denyReconnect("login_515")
		return
	}
	if gen.reconnects > 0 || gen.pending515 != nil && !gen.paired {
		o.mu.Unlock() // duplicates share the one request
		return
	}
	if gen.paired {
		gen.reconnects++
		o.mu.Unlock()
		go o.runPostPairReconnect(gen)
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
	gen.reconnects++
	o.mu.Unlock()
	go o.runPostPairReconnect(gen)
}

// runPostPairReconnect makes the single owner-initiated Connect that completes a pairing: it takes
// the attempt slot, drops the paired socket and connects once under the attempt watchdog. A failure
// is final.
func (o *connectionOwner) runPostPairReconnect(gen *genState) {
	select {
	case o.slot <- struct{}{}:
	case <-gen.lifetime.Done():
		return
	}
	defer func() { <-o.slot }()
	o.mu.Lock()
	if o.stopping || o.gen != gen || gen.retiring || o.phase == PhaseLoggedOut || o.health.StorageFault || o.outdated {
		o.mu.Unlock()
		return
	}
	o.connected = false
	o.phase = PhaseConnecting
	o.publishLocked()
	o.mu.Unlock()
	gen.handle.Disconnect()
	o.mu.Lock()
	if o.stopping || o.gen != gen || gen.retiring || o.phase == PhaseLoggedOut || o.health.StorageFault || o.outdated {
		o.mu.Unlock()
		return
	}
	o.mu.Unlock()
	if outcome := o.attempt(gen, attemptPostPair); outcome == outcomeConnected {
		return
	}
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
