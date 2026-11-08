package wa

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// Nothing the check observes may leave quiet.go except counts and a category. The markers below are planted in
// the goroutine dump; none may appear in a log line at any level, in the result value or in a returned error.
var secrecyMarkers = []string{
	"SECRETFN_MARKER", "SECRETSTATE", "0xDEADBEEFCAFE", "SECRETFILE.go", "491511234567", "KEYBYTES_MARKER", "QRMATERIAL_MARKER",
}

func plantedDump() string {
	return "goroutine 1 [running]:\nmain.main()\n\n" +
		"goroutine 999 [chan send, SECRETSTATE]:\n" +
		"go.mau.fi/whatsmeow.(*Client).SECRETFN_MARKER(0xDEADBEEFCAFE, {0x1, 0x2}, \"491511234567\", \"KEYBYTES_MARKER\", \"QRMATERIAL_MARKER\")\n" +
		"\t/secret/path/SECRETFILE.go:10 +0x20\n"
}

func assertNoMarkers(t *testing.T, what, text string) {
	t.Helper()
	for _, m := range secrecyMarkers {
		if strings.Contains(text, m) {
			t.Errorf("%s contains %q:\n%s", what, m, text)
		}
	}
	if strings.Contains(text, "goroutine ") && strings.Contains(text, "[chan send") {
		t.Errorf("%s contains a goroutine header:\n%s", what, text)
	}
}

func TestQuietCheckLeaksNothingFromTheObservation(t *testing.T) {
	cases := []struct {
		name string
		dump func(buf []byte) (int, error)
		want QuietReason
	}{
		{"pass", func(buf []byte) (int, error) { return copy(buf, "goroutine 1 [running]:\nmain.main()\n"), nil }, QuietPassed},
		{"work running", func(buf []byte) (int, error) { return copy(buf, plantedDump()), nil }, QuietWorkRunning},
		{"over-large dump", func(buf []byte) (int, error) {
			for i := range buf {
				buf[i] = "SECRETFN_MARKER 0xDEADBEEFCAFE "[i%30]
			}
			return len(buf), nil
		}, QuietObservationIncomplete},
		{"dump error", func([]byte) (int, error) { return 0, fmt.Errorf("SECRETFN_MARKER 0xDEADBEEFCAFE") }, QuietObservationError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, err := store.Open(context.Background(), waDataDir(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close(context.Background()) })
			logger, mem := newMemLog()
			var session atomic.Pointer[recordingSession]
			o := NewOwner(OwnerConfig{
				Log: logger, Store: st, Versions: bundledProvider(context.Background()), ShellStartupBudget: true,
				Sessions: func(cfg SessionConfig) (SessionHandle, error) {
					cfg.PreLoginHTTP = countingClient(&countingTransport{})
					cfg.WebsocketHTTP = countingClient(&countingTransport{})
					s, err := OpenSession(cfg)
					if err != nil {
						return nil, err
					}
					clock := &fakeClock{t: time.Unix(1000, 0)}
					s.observer = func() quietObserver {
						return quietObserver{dump: c.dump, self: "1", now: clock.now, wait: clock.wait}
					}
					rec := &recordingSession{Session: s}
					session.Store(rec)
					return rec, nil
				},
			})
			if err := o.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			// The attempt is what makes the generation checkable; the transport refuses it.
			_ = session.Load().Connect(context.Background())
			o.BeginStop()
			select {
			case <-o.Done():
			case <-time.After(20 * time.Second):
				t.Fatal("stop did not finish")
			}
			res := *session.Load().res.Load()
			if res.Reason != c.want {
				t.Fatalf("reason = %q, want %q", res.Reason, c.want)
			}
			assertNoMarkers(t, "the result", fmt.Sprintf("%+v %#v", res, res))
			if err := o.StopError(); err != nil {
				assertNoMarkers(t, "the stop error", err.Error())
			}
			mem.mu.Lock()
			defer mem.mu.Unlock()
			assertNoMarkers(t, "the log", strings.Join(mem.lines, "\n"))
			if h := o.Health(); h.QuietReason != "" && h.QuietReason != string(c.want) {
				t.Errorf("health reason = %q", h.QuietReason)
			}
		})
	}
}

// recordingSession keeps the result of the real session's check so a test can inspect it.
type recordingSession struct {
	*Session
	res atomic.Pointer[QuietStopResult]
}

func (r *recordingSession) QuietStop(ctx context.Context, d time.Duration) QuietStopResult {
	res := r.Session.QuietStop(ctx, d)
	r.res.Store(&res)
	return res
}
