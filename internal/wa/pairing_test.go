package wa

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
)

func TestPairingRejectsUnencodableAndLateCodes(t *testing.T) {
	o := NewOwner(OwnerConfig{Versions: bundledProvider(context.Background())})
	gen := &genState{}
	w := &pairingWindow{o: o, gen: gen, result: PairingActive}
	o.gen, o.pairing = gen, w
	if err := w.setCode("synthetic", time.Minute); err != nil {
		t.Fatal(err)
	}
	before := w.snapshotLocked()
	for _, malformed := range []string{"bad\x00code", "bad\x1bcode", string([]byte{0xff})} {
		if err := w.setCode(malformed, time.Minute); !errors.Is(err, ErrQRCodeInvalid) {
			t.Errorf("malformed replacement error = %v", err)
		}
		if got := w.snapshotLocked(); got != before {
			t.Error("malformed input replaced valid state")
		}
	}
	if err := w.setCode(strings.Repeat("a", 3500), time.Minute); !errors.Is(err, ErrQRCodeEncode) {
		t.Fatalf("unencodable replacement: %v", err)
	}
	if got := w.snapshotLocked(); got != before {
		t.Fatalf("invalid replacement changed state: %+v", got)
	}
	o.mu.Lock()
	w.result = PairingFailed
	w.clearCodeLocked()
	o.mu.Unlock()
	if err := w.setCode("late", time.Minute); err == nil {
		t.Fatal("late code was accepted")
	}
	if got := w.snapshotLocked(); got.Code != "" {
		t.Fatal("late code restored pairing material")
	}
	o.cancel()
}

func TestPairingFailureWinsConflictingSuccess(t *testing.T) {
	o := NewOwner(OwnerConfig{Versions: bundledProvider(context.Background())})
	ctx, cancel := context.WithCancel(context.Background())
	s := &fakeSession{}
	lib, libCancel := context.WithCancel(context.Background())
	gen := &genState{handle: s, work: ctx, workCancel: cancel, lib: lib, libCancel: libCancel}
	w := &pairingWindow{o: o, gen: gen, result: PairingFailed}
	o.gen, o.pairing = gen, w
	w.finish(PairingSucceeded, true)
	if s.retiredN.Load() != 1 {
		t.Error("conflicting success retained failed generation")
	}
}

func TestPairingFailureRetiresGeneration(t *testing.T) {
	for _, connectFails := range []bool{false, true} {
		name := "pair_error"
		if connectFails {
			name = "connect_error"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ot := newOwnerTest(t, false, false, nil)
				qs := &qrSession{qr: make(chan whatsmeow.QRChannelItem, 1)}
				if connectFails {
					qs.script = func(int64, context.Context) error { return errors.New("synthetic") }
				}
				ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) {
					qs.cfg = cfg
					return qs, nil
				}
				if err := ot.o.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				if _, err := ot.o.RequestPairing(context.Background()); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if !connectFails {
					qs.send(SessionPairError)
					synctest.Wait()
				}
				state, _ := ot.o.PairingSnapshot()
				if state.Result != PairingFailed || state.Code != "" || qs.retiredN.Load() != 1 {
					t.Fatalf("failure: state=%+v retirements=%d", state, qs.retiredN.Load())
				}
				stopInBubble(t, ot)
			})
		})
	}
}

func TestPairingConnectContextLivesUntilRetirement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, false, false, nil)
		qs := &qrSession{qr: make(chan whatsmeow.QRChannelItem, 1)}
		ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) { qs.cfg = cfg; return qs, nil }
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := ot.o.RequestPairing(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		ctx := *qs.attemptCtx.Load()
		qs.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "synthetic", Timeout: time.Minute}
		synctest.Wait()
		if ctx.Err() != nil {
			t.Error("first QR canceled retained connection context")
		}
		qs.send(SessionPairSuccess)
		qs.send(SessionConnected)
		synctest.Wait()
		if ctx.Err() != nil {
			t.Error("pairing success canceled retained connection context")
		}
		stopInBubble(t, ot)
		if ctx.Err() == nil {
			t.Fatal("retirement did not cancel connection context")
		}
	})
}

type blockingPairDisconnect struct {
	qrSession
	entered chan struct{}
	release chan struct{}
}

func (s *blockingPairDisconnect) Disconnect() {
	if s.disconnectN.Add(1) == 1 {
		close(s.entered)
		<-s.release
	}
}

func TestPairingWatchdogDrainsBeforeSlotRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, false, false, nil)
		qs := &blockingPairDisconnect{qrSession: qrSession{qr: make(chan whatsmeow.QRChannelItem, 1)}, entered: make(chan struct{}), release: make(chan struct{})}
		qs.script = func(_ int64, ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
		ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) { qs.cfg = cfg; return qs, nil }
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := ot.o.RequestPairing(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(connectWatchdog)
		<-qs.entered
		synctest.Wait()
		if ot.o.trySlotForTest() {
			t.Error("slot released while watchdog disconnect is running")
		}
		close(qs.release)
		synctest.Wait()
		if !ot.o.trySlotForTest() {
			t.Error("slot not released after watchdog drained")
		}
		stopInBubble(t, ot)
	})
}

func TestPairingSuccessWaitsForRunningWatchdog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, false, false, nil)
		qs := &blockingPairDisconnect{qrSession: qrSession{qr: make(chan whatsmeow.QRChannelItem, 1)}, entered: make(chan struct{}), release: make(chan struct{})}
		ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) { qs.cfg = cfg; return qs, nil }
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := ot.o.RequestPairing(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(connectWatchdog)
		<-qs.entered
		qs.send(SessionPairSuccess)
		qs.send(SessionConnected)
		synctest.Wait()
		if ot.o.trySlotForTest() {
			t.Error("success released slot while watchdog disconnect is running")
		}
		close(qs.release)
		synctest.Wait()
		if status := ot.o.Snapshot(); !status.LoggedIn || status.Connected {
			t.Errorf("timeout must preserve identity without confirming connection: %+v", status)
		}
		if phase := ot.o.phaseForTest(); phase != PhaseDisconnected {
			t.Errorf("timeout phase = %v, want disconnected", phase)
		}
		if qs.retiredN.Load() != 1 {
			t.Error("timed-out generation was kept after simultaneous success")
		}
		stopInBubble(t, ot)
	})
}

// pairingRig starts an owner whose only session is a qrSession. It must run inside synctest.Test.
func pairingRig(t *testing.T) (*ownerTest, *qrSession) {
	t.Helper()
	ot := newOwnerTest(t, false, false, nil)
	qs := &qrSession{qr: make(chan whatsmeow.QRChannelItem, 8)}
	ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) { qs.cfg = cfg; return qs, nil }
	if err := ot.o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return ot, qs
}

func (q *qrSession) code(code string, timeout time.Duration) {
	q.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: code, Timeout: timeout}
}

func TestPairingConcurrentRequestsShareOneWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := pairingRig(t)
		first, err := ot.o.RequestPairing(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		qs.code("first", 20*time.Second)
		synctest.Wait()
		second, err := ot.o.RequestPairing(context.Background())
		if err != nil {
			t.Fatalf("second request while the code is valid: %v", err)
		}
		if first.Result != PairingActive || second.Result != PairingActive || second.Code != "first" {
			t.Fatalf("shared window: first=%+v second=%+v", first, second)
		}
		// A request while the code awaits replacement shares the same window as well.
		time.Sleep(20 * time.Second)
		synctest.Wait()
		if snap, _ := ot.o.PairingSnapshot(); snap.Code != "" || snap.Result != PairingActive {
			t.Fatalf("expired code must clear without ending the window: %+v", snap)
		}
		third, err := ot.o.RequestPairing(context.Background())
		if err != nil || third.Result != PairingActive || third.Code != "" {
			t.Fatalf("request while awaiting replacement: %+v, %v", third, err)
		}
		qs.code("second", 20*time.Second)
		synctest.Wait()
		if snap, _ := ot.o.PairingSnapshot(); snap.Code != "second" {
			t.Fatalf("replacement code not published: %+v", snap)
		}
		if qs.connectN.Load() != 1 || qs.maxSeen.Load() != 1 {
			t.Errorf("connects = %d, max concurrent = %d; want one attempt", qs.connectN.Load(), qs.maxSeen.Load())
		}
		stopInBubble(t, ot)
	})
}

func TestPairingCodeExpiryIsBoundedAndDoesNotEndWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := pairingRig(t)
		if _, err := ot.o.RequestPairing(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		// An upstream expiry longer than the local bound is capped at 60 seconds.
		qs.code("long-lived", 10*time.Minute)
		synctest.Wait()
		time.Sleep(codeTTL - time.Second)
		synctest.Wait()
		if snap, _ := ot.o.PairingSnapshot(); snap.Code != "long-lived" {
			t.Fatalf("code cleared before the bound: %+v", snap)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if snap, _ := ot.o.PairingSnapshot(); snap.Code != "" || snap.Result != PairingActive {
			t.Fatalf("code outlived the 60 second bound or ended the window: %+v", snap)
		}
		stopInBubble(t, ot)
	})
}

func TestPairingFinalTimeoutGatesFurtherRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := pairingRig(t)
		if _, err := ot.o.RequestPairing(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		qs.code("only", 20*time.Second)
		synctest.Wait()
		qs.qr <- whatsmeow.QRChannelItem{Event: "timeout"}
		synctest.Wait()
		snap, ok := ot.o.PairingSnapshot()
		if !ok || snap.Result != PairingTimedOut || snap.Code != "" {
			t.Fatalf("final timeout: %+v, %v", snap, ok)
		}
		if qs.retiredN.Load() != 1 {
			t.Errorf("timed-out generation retired %d times, want 1", qs.retiredN.Load())
		}
		// An unresolved final timeout never authorizes another window or recovery by itself.
		if _, err := ot.o.RequestPairing(context.Background()); !errors.Is(err, store.ErrUnsafeRun) {
			t.Fatalf("request after unresolved final timeout: %v", err)
		}
		synctest.Wait()
		if qs.connectN.Load() != 1 {
			t.Errorf("connects = %d; no automatic new window may open", qs.connectN.Load())
		}
		if snap, _ := ot.o.PairingSnapshot(); snap.Result != PairingTimedOut {
			t.Errorf("rejected request replaced the terminal result: %+v", snap)
		}
		stopInBubble(t, ot)
	})
}

func TestPairingWaitContextCancellationDoesNotCancelWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := pairingRig(t)
		ctx, cancel := context.WithCancel(context.Background())
		if _, err := ot.o.RequestPairing(ctx); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		qs.code("kept", 20*time.Second)
		synctest.Wait()
		cancel()
		synctest.Wait()
		if snap, _ := ot.o.PairingSnapshot(); snap.Result != PairingActive || snap.Code != "kept" {
			t.Fatalf("caller cancellation affected the approved window: %+v", snap)
		}
		if qs.retiredN.Load() != 0 {
			t.Error("caller cancellation retired the generation")
		}
		// Explicit cancellation does end it, without another attempt.
		ot.o.CancelPairing()
		synctest.Wait()
		if snap, _ := ot.o.PairingSnapshot(); snap.Result != PairingCancelled || snap.Code != "" {
			t.Fatalf("explicit cancel: %+v", snap)
		}
		stopInBubble(t, ot)
	})
}

func TestPairingRejectedDuringShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := pairingRig(t)
		ot.o.BeginStop()
		synctest.Wait()
		if _, err := ot.o.RequestPairing(context.Background()); !errors.Is(err, ErrStopping) {
			t.Fatalf("request during shutdown: %v", err)
		}
		if qs.connectN.Load() != 0 {
			t.Error("a stopping owner opened a connection for pairing")
		}
	})
}

func TestPairingStopEndsActiveWindowAndClearsCode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := pairingRig(t)
		if _, err := ot.o.RequestPairing(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		qs.code("live", 20*time.Second)
		synctest.Wait()
		stopInBubble(t, ot)
		snap, _ := ot.o.PairingSnapshot()
		if snap.Code != "" || snap.Result == PairingActive {
			t.Fatalf("window survived shutdown: %+v", snap)
		}
		if _, err := ot.o.RequestPairing(context.Background()); !errors.Is(err, ErrStopping) {
			t.Fatalf("request after shutdown: %v", err)
		}
	})
}

func TestPairingStaleAndLateEventsAreIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := pairingRig(t)
		if _, err := ot.o.RequestPairing(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		qs.code("live", 20*time.Second)
		synctest.Wait()
		qs.sendStale(SessionPairSuccess)
		qs.sendStale(SessionPairError)
		qs.sendStale(SessionLoggedOut)
		synctest.Wait()
		if snap, _ := ot.o.PairingSnapshot(); snap.Result != PairingActive || snap.Code != "live" {
			t.Fatalf("stale epoch changed the window: %+v", snap)
		}
		if st := ot.o.Snapshot(); st.LoggedIn {
			t.Fatalf("stale pair success published a login: %+v", st)
		}
		// A code arriving after the terminal result restores nothing.
		qs.qr <- whatsmeow.QRChannelItem{Event: "timeout"}
		synctest.Wait()
		qs.code("late", 20*time.Second)
		synctest.Wait()
		if snap, _ := ot.o.PairingSnapshot(); snap.Code != "" || snap.Result != PairingTimedOut {
			t.Fatalf("late code after the terminal result: %+v", snap)
		}
		stopInBubble(t, ot)
	})
}

func TestPairingRejectsUnsupportedAndInvalidFlows(t *testing.T) {
	for _, ev := range []string{whatsmeow.QRChannelEventPasskeyRequest, whatsmeow.QRChannelEventPasskeyResponse, whatsmeow.QRChannelEventError, "unknown-event"} {
		t.Run(ev, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ot, qs := pairingRig(t)
				if _, err := ot.o.RequestPairing(context.Background()); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				qs.code("live", 20*time.Second)
				qs.qr <- whatsmeow.QRChannelItem{Event: ev, Code: "secret-looking-value"}
				synctest.Wait()
				snap, _ := ot.o.PairingSnapshot()
				if snap.Result != PairingFailed || snap.Code != "" {
					t.Fatalf("unsupported flow: %+v", snap)
				}
				if ot.log.contains("secret-looking-value") || ot.log.contains("live") {
					t.Error("pairing material in log")
				}
				stopInBubble(t, ot)
			})
		})
	}
}

// Retiring a generation with an open pairing window ends the window's QR channel, so the library's emitter
// goroutine returns. The window's outcome and the unresolved accounting are unchanged.
func TestRetiringAnOpenWindowEndsItsQRChannel(t *testing.T) {
	trace := &stepLog{}
	r := newOwnerRigWith(t, func(cfg *OwnerConfig) { cfg.trace = trace.add })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := r.owner.RequestPairing(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := r.srv.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SendPairDevice(ctx, "ref-1"); err != nil {
		t.Fatal(err)
	}
	for {
		if snap, ok := r.owner.PairingSnapshot(); ok && snap.Code != "" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("no QR code")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if !dumpHasQREmitter(t) {
		t.Fatal("the QR emitter goroutine was not running while the window was open")
	}
	r.owner.BeginStop()
	select {
	case <-r.owner.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("stop did not finish")
	}
	waitFor(t, func() bool { return !dumpHasQREmitter(t) })
	if snap, ok := r.owner.PairingSnapshot(); !ok || snap.Result != PairingCancelled {
		t.Errorf("window = %+v, %v; want cancelled", snap, ok)
	}
	if err := r.owner.StopError(); err == nil {
		t.Error("an unresolved pairing stopped successfully")
	}
}

func dumpHasQREmitter(t *testing.T) bool {
	t.Helper()
	buf := make([]byte, 4<<20)
	n := runtime.Stack(buf, true)
	return strings.Contains(string(buf[:n]), "(*qrChannel).emitQRs")
}
