package wa

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
)

// Adverse interleavings of the pairing window against logout, timeout, retirement and delayed
// upstream events. Each case shows that a rejected action changes neither the current account state
// nor the QR material.

func openWindowWithCode(t *testing.T, code string) (*ownerTest, *qrSession) {
	t.Helper()
	ot, qs := pairingRig(t)
	if _, err := ot.o.RequestPairing(context.Background()); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	qs.code(code, 20*time.Second)
	synctest.Wait()
	return ot, qs
}

// A replacement QR queued behind a logout never becomes visible, and the logout leaves no window.
func TestRaceReplacementQRVersusLogout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := openWindowWithCode(t, "first")
		qs.send(SessionLoggedOut)
		qs.code("replacement", 20*time.Second)
		synctest.Wait()
		snap, _ := ot.o.PairingSnapshot()
		if snap.Code != "" || snap.Result == PairingActive {
			t.Fatalf("window after logout and a queued replacement: %+v", snap)
		}
		if st := ot.o.Snapshot(); st.LoggedIn || st.Connected {
			t.Fatalf("status after logout: %+v", st)
		}
		stopInBubble(t, ot)
	})
}

// A PairSuccess that arrives after the window timed out and its generation retired is stale: it
// changes neither the window result nor the login state.
func TestRaceLatePairSuccessVersusTimeoutAndRetire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := openWindowWithCode(t, "only")
		qs.qr <- whatsmeow.QRChannelItem{Event: "timeout"}
		synctest.Wait()
		if qs.retiredN.Load() != 1 {
			t.Fatalf("generation retired %d times", qs.retiredN.Load())
		}
		qs.send(SessionPairSuccess)
		synctest.Wait()
		snap, _ := ot.o.PairingSnapshot()
		if snap.Result != PairingTimedOut || snap.Code != "" {
			t.Fatalf("late success rewrote the terminal result: %+v", snap)
		}
		if st := ot.o.Snapshot(); st.LoggedIn {
			t.Fatalf("late success published a login: %+v", st)
		}
		stopInBubble(t, ot)
	})
}

// A delayed client-outdated (405) event after the final timeout cannot reopen the terminal window,
// start a connection or restore a code.
func TestRaceFinalTimeoutVersusDelayed405(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := openWindowWithCode(t, "only")
		qs.qr <- whatsmeow.QRChannelItem{Event: "timeout"}
		synctest.Wait()
		connects := qs.connectN.Load()
		qs.send(SessionClientOutdated)
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		snap, _ := ot.o.PairingSnapshot()
		if snap.Result != PairingTimedOut || snap.Code != "" {
			t.Fatalf("405 reopened the terminal window: %+v", snap)
		}
		if qs.connectN.Load() != connects {
			t.Errorf("connects %d -> %d; a delayed 405 must not start an attempt", connects, qs.connectN.Load())
		}
		if _, err := ot.o.RequestPairing(context.Background()); !errors.Is(err, store.ErrUnsafeRun) {
			t.Errorf("request after the timeout and a 405: %v", err)
		}
		stopInBubble(t, ot)
	})
}

// Stop racing the final timeout from either side ends with one retirement, no code and no
// connection left behind.
func TestRaceStopVersusFinalTimeout(t *testing.T) {
	for _, stopFirst := range []bool{false, true} {
		name := "timeout_first"
		if stopFirst {
			name = "stop_first"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ot, qs := openWindowWithCode(t, "only")
				if stopFirst {
					ot.o.BeginStop()
					qs.qr <- whatsmeow.QRChannelItem{Event: "timeout"}
				} else {
					qs.qr <- whatsmeow.QRChannelItem{Event: "timeout"}
					ot.o.BeginStop()
				}
				synctest.Wait()
				select {
				case <-ot.o.Done():
				default:
					t.Fatal("owner did not finish stopping")
				}
				if qs.retiredN.Load() != 1 {
					t.Errorf("retired %d times, want 1", qs.retiredN.Load())
				}
				if snap, _ := ot.o.PairingSnapshot(); snap.Code != "" || snap.Result == PairingActive {
					t.Errorf("window after stop: %+v", snap)
				}
			})
		})
	}
}

// A PairError cannot be turned into a success by a later PairSuccess from the same generation.
func TestRacePairErrorThenPairSuccessKeepsFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := openWindowWithCode(t, "only")
		qs.send(SessionPairError)
		synctest.Wait()
		qs.send(SessionPairSuccess)
		synctest.Wait()
		snap, _ := ot.o.PairingSnapshot()
		if snap.Result != PairingFailed || snap.Code != "" {
			t.Fatalf("failure was overwritten: %+v", snap)
		}
		stopInBubble(t, ot)
	})
}
