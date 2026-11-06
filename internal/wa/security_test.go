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

// The owner logs at every level, including debug. These tests drive hostile synthetic material
// through every owner path and require that none of it reaches a log line.

const (
	secretCode    = "2@SECRETQRPAYLOAD0123456789,KEYMATERIALabcdef==,ADVSECRET"
	secretAccount = "490000000099@s.whatsapp.net"
)

var secretNeedles = []string{"SECRETQRPAYLOAD", "KEYMATERIAL", "ADVSECRET", secretAccount, "490000000099"}

func assertNoSecrets(t *testing.T, log *memLog) {
	t.Helper()
	log.mu.Lock()
	defer log.mu.Unlock()
	for _, line := range log.lines {
		for _, needle := range secretNeedles {
			if strings.Contains(line, needle) {
				t.Errorf("log line carries %q: %s", needle, line)
			}
		}
		for _, r := range line {
			if r < 0x20 || r == 0x7f {
				t.Errorf("log line carries a control character %U: %q", r, line)
				break
			}
		}
	}
}

func TestOwnerLogsCarryNoPairingMaterialAtAnyLevel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := openWindowWithCode(t, secretCode)
		qs.code(secretCode+"-2", 20*time.Second) // replacement
		synctest.Wait()
		qs.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventError, Code: secretCode}
		synctest.Wait()
		stopInBubble(t, ot)
		assertNoSecrets(t, ot.log)
		for _, want := range []string{"pairing_requested", "pairing_ended"} {
			if !ot.log.contains(want) {
				t.Errorf("no %s event", want)
			}
		}
	})
}

func TestOwnerLogsCarryNoMaterialAcrossSuccessLogoutAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(qs *qrSession)
		want string
	}{
		{"timeout", func(qs *qrSession) { qs.qr <- whatsmeow.QRChannelItem{Event: "timeout", Code: secretCode} }, "timed_out"},
		{"success", func(qs *qrSession) { qs.send(SessionPairSuccess); qs.send(SessionConnected) }, "succeeded"},
		{"logout", func(qs *qrSession) { qs.send(SessionLoggedOut) }, "failed"},
		{"pair_error", func(qs *qrSession) { qs.send(SessionPairError) }, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ot, qs := openWindowWithCode(t, secretCode)
				tc.end(qs)
				synctest.Wait()
				stopInBubble(t, ot)
				assertNoSecrets(t, ot.log)
				if !ot.log.contains("outcome=" + tc.want) {
					t.Errorf("no pairing_ended outcome=%s event", tc.want)
				}
			})
		})
	}
}

func TestOwnerLogsCarryNoErrorTextFromSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, func(int64, context.Context) error {
			return errors.New("dial failed for " + secretAccount + " with " + secretCode)
		})
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(2 * connectWatchdog)
		synctest.Wait()
		stopInBubble(t, ot)
		assertNoSecrets(t, ot.log)
	})
}

// Runtime samples: a fixed one-minute cadence at debug level, no history, numbers and a fixed
// reason only, ended by stop.
func TestMonitoringSamplesOncePerMinuteAndStopsWithTheOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, false, nil)
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		count := func() int {
			ot.log.mu.Lock()
			defer ot.log.mu.Unlock()
			n := 0
			for _, l := range ot.log.lines {
				if strings.Contains(l, "event=runtime_sample") {
					n++
				}
			}
			return n
		}
		if n := count(); n != 1 {
			t.Fatalf("samples at start = %d, want 1 lifecycle sample", n)
		}
		time.Sleep(3*monitorInterval + time.Second)
		synctest.Wait()
		if n := count(); n != 4 {
			t.Fatalf("samples after three minutes = %d, want 4 (start plus one per minute)", n)
		}
		stopInBubble(t, ot)
		after := count()
		if after != 5 {
			t.Fatalf("samples after stop = %d, want 5 (one final lifecycle sample)", after)
		}
		time.Sleep(10 * monitorInterval)
		synctest.Wait()
		if n := count(); n != after {
			t.Fatalf("monitoring continued after stop: %d -> %d", after, n)
		}
		ot.log.mu.Lock()
		defer ot.log.mu.Unlock()
		for _, l := range ot.log.lines {
			if !strings.Contains(l, "event=runtime_sample") {
				continue
			}
			for _, key := range []string{"reason=", "heap_alloc_bytes=", "goroutines="} {
				if !strings.Contains(l, key) {
					t.Errorf("sample lacks %s: %s", key, l)
				}
			}
			if strings.Count(l, "=") != 4 { // event, reason, heap_alloc_bytes, goroutines
				t.Errorf("sample carries other fields: %s", l)
			}
		}
	})
}
