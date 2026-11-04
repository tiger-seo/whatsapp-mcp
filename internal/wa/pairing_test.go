package wa

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

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
	gen := &genState{handle: s, lifetime: ctx, cancel: cancel}
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
