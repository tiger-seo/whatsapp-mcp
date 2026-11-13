package wa

import (
	"context"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
	"rsc.io/qr"
)

// Pairing bounds: a code is at most 4 KiB and lives for the earlier of the upstream expiry and 60
// seconds from receipt. One window exists at a time and is bound to its generation's epoch.
const (
	codeMaxBytes = 4 * 1024
	codeTTL      = 60 * time.Second
)

var ErrQRCodeTooLong = errors.New("the pairing code exceeds the 4 KiB bound")

// pairingWindow is one owner-approved pairing attempt. Its fields are guarded by the owner mutex;
// the terminal result stays queryable until the next window replaces it.
type pairingWindow struct {
	o   *connectionOwner
	gen *genState

	result      PairingResult
	qrCancel    context.CancelFunc // ends the QR channel's context; guarded by the owner mutex
	ended       bool               // the terminal outcome was logged; guarded by the owner mutex
	code        string
	codeExpires time.Time
	expiry      *time.Timer
	done        chan struct{}
}

// RequestPairing explicitly authorizes one window, or shares the active one. The caller's context
// only bounds the wait, never the window itself.
func (o *connectionOwner) RequestPairing(ctx context.Context) (PairingState, error) {
	o.windowMu.Lock()
	defer o.windowMu.Unlock()
	o.mu.Lock()
	if o.stopping {
		o.mu.Unlock()
		return PairingState{}, ErrStopping
	}
	if o.unsafeStop || !o.ready {
		o.mu.Unlock()
		return PairingState{}, store.ErrUnsafeRun
	}
	if o.identity {
		o.mu.Unlock()
		return PairingState{}, ErrAlreadyPaired
	}
	if w := o.pairing; w != nil && w.result == PairingActive {
		snap := w.snapshotLocked()
		o.mu.Unlock()
		return snap, nil
	}
	if o.unresolved {
		o.mu.Unlock()
		return PairingState{}, store.ErrUnsafeRun
	}
	previous := o.pairing
	o.mu.Unlock()
	if previous != nil {
		select {
		case <-previous.done:
		case <-ctx.Done():
			return PairingState{}, ctx.Err()
		case <-o.ctx.Done():
			return PairingState{}, ErrStopping
		}
	}

	gen, err := o.ensureFreshGeneration()
	if err != nil {
		return PairingState{}, err
	}
	w := &pairingWindow{o: o, gen: gen, result: PairingActive, done: make(chan struct{})}
	o.mu.Lock()
	if o.stopping || o.gen != gen || gen.retiring {
		o.mu.Unlock()
		o.retireGeneration(gen, quietStopDeadline)
		return PairingState{}, ErrStopping
	}
	o.pairing = w
	o.unresolved = true
	o.phase = PhasePairing
	o.publishLocked()
	o.mu.Unlock()
	o.log.Info("pairing requested", "event", "pairing_requested", "epoch", gen.epoch)
	go w.run()
	o.mu.Lock()
	snap := w.snapshotLocked()
	o.mu.Unlock()
	return snap, nil
}

// ensureFreshGeneration returns a fresh generation, retiring and draining an old one first. If the
// reload after the drain finds a durable identity, pairing is refused instead of overwriting keys.
func (o *connectionOwner) ensureFreshGeneration() (*genState, error) {
	o.mu.Lock()
	gen := o.gen
	reusable := gen != nil && !gen.retiring && gen.work.Err() == nil && !gen.handle.HasIdentity()
	o.mu.Unlock()
	if reusable {
		return gen, nil
	}
	if gen != nil {
		o.retireGeneration(gen, quietStopDeadline)
	}
	o.mu.Lock()
	blocked := o.unsafeStop || o.stopping
	o.mu.Unlock()
	if blocked {
		return nil, store.ErrUnsafeRun
	}
	fresh, err := o.openGeneration()
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	o.gen = fresh
	o.publishLocked()
	o.mu.Unlock()
	o.watchStorageFault(fresh)
	o.watchIngestFailure(fresh)
	if fresh.handle.HasIdentity() {
		o.mu.Lock()
		o.identity = true
		if fresh.handle.RecoveryPending() {
			o.health.RecoveryPending = true
		}
		if o.phase == PhasePairing || o.phase == PhaseIdle {
			o.phase = PhaseDisconnected
		}
		o.publishLocked()
		o.mu.Unlock()
		return nil, ErrAlreadyPaired
	}
	return fresh, nil
}

// run is the window controller and the single fast consumer of the upstream channel.
func (w *pairingWindow) run() {
	o := w.o
	defer close(w.done)
	// The window holds the attempt slot from open to terminal, so no other attempt can overlap.
	select {
	case o.slot <- struct{}{}:
	default:
		w.finish(PairingFailed, false)
		return
	}
	defer func() { <-o.slot }()
	o.mu.Lock()
	active := w.result == PairingActive && !o.stopping && o.gen == w.gen
	o.mu.Unlock()
	if !active {
		w.finish(PairingCancelled, false)
		return
	}

	// Version discovery happens before the connection; a failure keeps the last usable version.
	if err := o.cfg.Versions.Refresh(w.gen.work); err != nil {
		o.log.Warn("version discovery failed; keeping the last usable version", "event", "version_refresh_failed", "reason", versionFailureReason(err))
	}
	src, ok := w.gen.handle.(QRChannelSource)
	if !ok {
		w.finish(PairingFailed, false)
		return
	}
	// The QR channel belongs to the window, so it uses the work context and can be ended on its own when the
	// generation retires. It never reaches the context the socket's handlers run with.
	qrCtx, qrCancel := context.WithCancel(w.gen.work)
	o.mu.Lock()
	w.qrCancel = qrCancel
	o.mu.Unlock()
	defer qrCancel()
	qrch, err := src.GetQRChannel(qrCtx)
	if err != nil {
		w.finish(PairingFailed, false)
		return
	}
	// The initial pairing connection carries its own watchdog, disarmed by the first valid code. Its context is
	// the handlers' context for the pairing socket, so it is a child of the library context and is aborted only
	// while the Connect has not returned. The stop check and the recorded abort share one critical section.
	o.mu.Lock()
	refused := o.stopping || w.gen.retiring
	var attemptCtx context.Context
	if !refused {
		var abort context.CancelCauseFunc
		attemptCtx, abort = context.WithCancelCause(w.gen.lib)
		w.gen.abort, w.gen.abortFired = abort, false
	}
	o.mu.Unlock()
	fired := make(chan struct{})
	expired := false
	timer := time.AfterFunc(connectWatchdog, func() {
		o.mu.Lock()
		expired = o.gen == w.gen && !w.gen.retiring && !o.stopping && !o.connected
		if expired {
			w.gen.timingOut = true
			o.abortAttemptLocked(w.gen)
		}
		o.mu.Unlock()
		if expired {
			w.gen.handle.Disconnect()
		}
		close(fired)
	})
	watchdogStopped := false
	watchdogFired := false
	drainWatchdog := func() bool {
		if watchdogStopped {
			return watchdogFired
		}
		watchdogStopped = true
		if timer.Stop() {
			return false
		}
		<-fired
		watchdogFired = expired
		return watchdogFired
	}
	defer drainWatchdog()

	// end records the terminal result; a torn-down generation gets its channel drained so no
	// upstream dispatcher stays blocked on it.
	end := func(result PairingResult, keepGeneration bool) {
		// Drain before waiting for a running Disconnect: an upstream terminal event may itself
		// be blocked on delivery to the QR channel.
		go func() {
			for {
				select {
				case _, ok := <-qrch:
					if !ok {
						return
					}
				case <-w.gen.work.Done():
					return
				}
			}
		}()
		timedOut := drainWatchdog()
		if timedOut {
			result, keepGeneration = PairingFailed, false
		}
		w.finish(result, keepGeneration)
		if timedOut {
			// A simultaneous PairSuccess can already have published success; the timed-out
			// connection still must be retired after the watchdog disconnect completes.
			o.retireGeneration(w.gen, quietStopDeadline)
			o.setPhaseIf(PhaseDisconnected, PhaseConnecting)
		}
	}
	if refused {
		end(PairingCancelled, false)
		return
	}
	err = w.gen.handle.Connect(attemptCtx)
	o.mu.Lock()
	if err == nil && w.gen.abortFired {
		w.gen.abortLate = true
	}
	w.gen.abort = nil
	o.mu.Unlock()
	if err != nil {
		end(PairingFailed, false)
		return
	}
	for {
		o.mu.Lock()
		result, identity, stopping, genCurrent := w.result, o.identity, o.stopping, o.gen == w.gen
		upch := o.updateCh
		o.mu.Unlock()
		switch {
		case result == PairingSucceeded || identity:
			end(PairingSucceeded, true)
			return
		case result != PairingActive:
			end(result, false)
			return
		case stopping:
			end(PairingCancelled, false)
			return
		case !genCurrent:
			end(PairingFailed, false)
			return
		}
		select {
		case item, ok := <-qrch:
			if !ok {
				end(PairingFailed, false)
				return
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				if validatePairingCode(item.Code) == nil {
					if drainWatchdog() {
						end(PairingFailed, false)
						return
					}
					w.setValidCode(item.Code, item.Timeout)
				}
			case whatsmeow.QRChannelEventPasskeyRequest, whatsmeow.QRChannelEventPasskeyResponse:
				// Unsupported pairing flows are safely rejected: no confirmation, no credentials.
				end(PairingFailed, false)
				return
			case whatsmeow.QRChannelEventError:
				end(PairingFailed, false)
				return
			case "success":
				end(PairingSucceeded, true)
				return
			case "timeout":
				end(PairingTimedOut, false)
				return
			default:
				end(PairingFailed, false)
				return
			}
		case <-fired:
			if drainWatchdog() {
				end(PairingFailed, false)
				return
			}
			fired = nil
		case <-o.ctx.Done():
			end(PairingCancelled, false)
			return
		case <-upch:
		}
	}
}

// endPairingWindow ends the QR channel of a generation's pairing window, so the library's emitter goroutine can
// return. The window's diagnostics and its unresolved-pairing accounting are unchanged.
func (o *connectionOwner) endPairingWindow(gen *genState) {
	o.mu.Lock()
	var cancel context.CancelFunc
	if w := o.pairing; w != nil && w.gen == gen {
		cancel = w.qrCancel
	}
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// setCode records the current code after the bounds check. Invalid, empty or over-limit input
// cannot replace a valid state; the earlier of the upstream expiry and 60 seconds bounds it.
func (w *pairingWindow) setCode(code string, upstream time.Duration) error {
	if err := validatePairingCode(code); err != nil {
		return err
	}
	return w.setValidCode(code, upstream)
}

func validatePairingCode(code string) error {
	switch {
	case code == "":
		return ErrQRCodeInvalid
	case len(code) > codeMaxBytes:
		return ErrQRCodeTooLong
	case !utf8.ValidString(code):
		return ErrQRCodeInvalid
	}
	for _, r := range code {
		if unicode.IsControl(r) {
			return ErrQRCodeInvalid
		}
	}
	if _, err := qr.Encode(code, qr.M); err != nil {
		return ErrQRCodeEncode
	}
	return nil
}

func (w *pairingWindow) setValidCode(code string, upstream time.Duration) error {
	ttl := codeTTL
	if upstream > 0 && upstream < ttl {
		ttl = upstream
	}
	o := w.o
	o.mu.Lock()
	if w.result != PairingActive || o.stopping || o.gen != w.gen || o.pairing != w {
		o.mu.Unlock()
		return ErrStopping
	}
	w.code = code
	w.codeExpires = time.Now().Add(ttl)
	expires := w.codeExpires
	if w.expiry != nil {
		w.expiry.Stop()
	}
	w.expiry = time.AfterFunc(ttl, func() {
		o.mu.Lock()
		if w.result == PairingActive && w.codeExpires == expires {
			w.clearCodeLocked()
			o.publishLocked()
		}
		o.mu.Unlock()
	})
	o.publishLocked()
	o.mu.Unlock()
	return nil
}

// clearCodeLocked invalidates the code without ending the window.
func (w *pairingWindow) clearCodeLocked() {
	w.code = ""
	w.codeExpires = time.Time{}
	if w.expiry != nil {
		w.expiry.Stop()
		w.expiry = nil
	}
}

func (w *pairingWindow) snapshotLocked() PairingState {
	return PairingState{Code: w.code, ExpiresAt: w.codeExpires, Result: w.result}
}

// finish records a terminal result, clears the material and, unless the pairing succeeded, tears
// the generation down outside the locks and drains it before releasing the attempt slot.
func (w *pairingWindow) finish(result PairingResult, _ bool) {
	o := w.o
	o.mu.Lock()
	if w.result == PairingActive {
		w.result = result
	} else {
		result = w.result
	}
	w.clearCodeLocked()
	gen := w.gen
	switch result {
	case PairingSucceeded:
		// Pair success proves linking, not a completed connection; the generation stays alive for
		// the Connected confirmation.
		if o.phase == PhasePairing {
			o.phase = PhaseConnecting
		}
	default:
		if o.phase == PhasePairing {
			o.phase = PhaseDisconnected
		}
	}
	logEnd := !w.ended
	w.ended = true
	o.publishLocked()
	o.mu.Unlock()
	if logEnd {
		// The outcome is one of a fixed set of words; it never carries the code or the account.
		o.log.Info("pairing ended", "event", "pairing_ended", "outcome", string(result), "epoch", gen.epoch)
	}

	if result == PairingSucceeded {
		return
	}
	o.retireGeneration(gen, quietStopDeadline)
}
