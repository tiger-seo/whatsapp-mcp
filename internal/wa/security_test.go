package wa

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
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

// A hostile or buggy QR source can emit codes as fast as it likes. Only the current code is
// retained, no goroutine is added per code, and no code reaches a log line.
func TestQRCodeFloodKeepsOnlyTheCurrentCode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot, qs := openWindowWithCode(t, "flood-start")
		before := runtime.NumGoroutine()
		const n = 5000
		for i := 0; i < n; i++ {
			qs.code(fmt.Sprintf("flood-%05d", i), 20*time.Second)
		}
		synctest.Wait()
		snap, ok := ot.o.PairingSnapshot()
		if !ok || snap.Code != fmt.Sprintf("flood-%05d", n-1) {
			t.Fatalf("snapshot = %q, %v; want only the latest code", snap.Code, ok)
		}
		if after := runtime.NumGoroutine(); after > before {
			t.Errorf("goroutines grew from %d to %d during the flood", before, after)
		}
		ot.log.mu.Lock()
		for _, line := range ot.log.lines {
			if strings.Contains(line, "flood-") {
				t.Errorf("log line carries a code: %s", line)
			}
		}
		lines := len(ot.log.lines)
		ot.log.mu.Unlock()
		if lines > 50 {
			t.Errorf("%d log lines for %d codes; logging must not scale with the flood", lines, n)
		}
		stopInBubble(t, ot)
	})
}

// Ingest handles content other accounts wrote. Text and captions may appear in a log only at debug; media keys,
// hashes, URLs and direct paths never; names never above debug; and nothing reaches stdout, which is the protocol
// channel in stdio mode.

const (
	secretText    = "SECRET-TEXT-8c1f"
	secretCaption = "SECRET-CAPTION-3d7a"
	secretPush    = "Secret Pushy Name"
	secretSubject = "Secret Group Subject"
)

// captureStdout returns what fn wrote to the process's standard output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	func() {
		defer func() { os.Stdout = orig; w.Close() }()
		fn()
	}()
	return <-done
}

// logScenario runs one fixed set of hostile and ordinary events through ingest with a text logger at level and
// returns everything it logged and everything it wrote to stdout.
func logScenario(t *testing.T, level slog.Level) (logged, stdout string) {
	t.Helper()
	var buf bytes.Buffer
	var mu sync.Mutex
	w := writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return buf.Write(p) })
	f := &faultyStore{}
	r := newIngestRig(t, withFaultyStore(f), func(cfg *IngestConfig) {
		cfg.Log = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
	})
	r.client.group = func(context.Context, types.JID) (*types.GroupInfo, error) { return groupInfo(secretSubject), nil }
	stdout = captureStdout(t, func() {
		handle(t, r, nameEvent("S1", peerJID(), peerJID(), secretPush, 0))
		text := liveEvent("S2", &waE2E.Message{Conversation: proto.String(secretText)})
		handle(t, r, text)
		handle(t, r, liveEvent("S3", imageMessage(secretCaption)))
		handle(t, r, nameEvent("S4", groupJID(), peerJID(), secretPush, 1))
		waitFor(t, func() bool { n, _, _ := chatNameOf(t, r.st, groupJID().String()); return n == secretSubject })
		// A rejected message and a history notification that cannot be downloaded.
		handle(t, r, liveEvent("S5", &waE2E.Message{Conversation: proto.String(secretText + strings.Repeat("x", 256<<10))}))
		handle(t, r, historyNotice(1))
		waitFor(t, func() bool { return r.in.Counters().HistoryIncomplete == 1 })
		// A failed write ends in a warning.
		f.setErr(fmt.Errorf("synthetic: %w", store.ErrDiskLow))
		r.in.HandleMessage(liveEvent("S6", &waE2E.Message{Conversation: proto.String(secretText)}))
		r.in.Stop()
		r.in.logStopped(0)
	})
	mu.Lock()
	defer mu.Unlock()
	return buf.String(), stdout
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestIngestLogsKeepContentAtDebugAndNeverLogMediaMaterial(t *testing.T) {
	var never []string
	for _, b := range [][]byte{key32(1), key32(2), key32(3)} {
		never = append(never, string(b), hex.EncodeToString(b), base64.StdEncoding.EncodeToString(b),
			base64.URLEncoding.EncodeToString(b), base64.RawStdEncoding.EncodeToString(b), fmt.Sprint(b), fmt.Sprintf("%x", b))
	}
	never = append(never, "mmg.example", "https://", "/v/i")
	for _, c := range []struct {
		name  string
		level slog.Level
	}{{"debug", slog.LevelDebug}, {"info", slog.LevelInfo}} {
		t.Run(c.name, func(t *testing.T) {
			logged, stdout := logScenario(t, c.level)
			if stdout != "" {
				t.Errorf("ingest wrote %d bytes to stdout", len(stdout))
			}
			for _, needle := range never {
				if strings.Contains(logged, needle) {
					t.Errorf("a log line carries media material %q", needle)
				}
			}
			for _, needle := range []string{secretPush, secretSubject} {
				if c.level > slog.LevelDebug && strings.Contains(logged, needle) {
					t.Errorf("a name appears above debug: %q", needle)
				}
			}
			hasText := strings.Contains(logged, secretText) && strings.Contains(logged, secretCaption)
			if c.level == slog.LevelDebug && !hasText {
				t.Errorf("debug shows no text or caption: the check would prove nothing: %s", logged)
			}
			if c.level > slog.LevelDebug && (strings.Contains(logged, secretText) || strings.Contains(logged, secretCaption)) {
				t.Errorf("text or a caption appears above debug")
			}
			// The scenario must have reached the warnings it is meant to cover.
			for _, want := range []string{"event=history_incomplete", "event=ingest_failed", "event=ingest_stopped"} {
				if !strings.Contains(logged, want) {
					t.Errorf("no %s record: the scenario did not reach it", want)
				}
			}
		})
	}
}

// A JID or ID another account chose is written quoted, so it cannot add a field or end the record.
func TestIngestLogsEscapeAttackerChosenIdentifiers(t *testing.T) {
	f := &faultyStore{}
	r := newIngestRig(t, withFaultyStore(f))
	f.setErr(fmt.Errorf("synthetic: %w", store.ErrDiskLow))
	id := `a b" event=ingest_stopped x=`
	if r.in.HandleMessage(liveEvent(id, &waE2E.Message{Conversation: proto.String("hello")})) {
		t.Fatal("a failed write was acknowledged")
	}
	ev := r.logs.events("ingest_failed")
	if len(ev) != 1 {
		t.Fatalf("ingest_failed records = %v", ev)
	}
	if got, want := ev[0]["message_id"], strconv.QuoteToASCII(id); got != want {
		t.Errorf("message_id = %s, want the quoted form %s", got, want)
	}
	if got, want := ev[0]["chat"], strconv.QuoteToASCII(peerJID().String()); got != want {
		t.Errorf("chat = %s, want %s", got, want)
	}
	if n := len(r.logs.events("ingest_stopped")); n != 0 {
		t.Errorf("a forged ingest_stopped record appeared: %d", n)
	}
}
