package wa

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

func openRetiredSession(t *testing.T) (*Session, context.Context) {
	t.Helper()
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s, err := OpenSession(newSessionTestConfig(st, lifetime))
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	if err := s.Retire(context.Background()); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	return s, lifetime
}

func TestSessionCountsOwnerRelevantEventsDroppedAfterRetirement(t *testing.T) {
	s, _ := openRetiredSession(t)
	s.handleEvent(&events.LoggedOut{})
	if got := s.droppedEvents.Load(); got != 1 {
		t.Fatalf("droppedEvents after a late LoggedOut = %d, want 1", got)
	}
	for _, ev := range []any{&events.Connected{}, &events.PairSuccess{}, &events.PairError{}, &events.ClientOutdated{}, &events.ManualLoginReconnect{}} {
		s.handleEvent(ev)
	}
	if got := s.droppedEvents.Load(); got != 6 {
		t.Fatalf("droppedEvents after six forwarded kinds = %d, want 6", got)
	}
}

func TestSessionDoesNotCountDisconnectsOrOrdinaryTrafficAsDropped(t *testing.T) {
	s, _ := openRetiredSession(t)
	s.handleEvent(&events.Disconnected{})
	s.handleEvent(&events.StreamReplaced{})
	s.handleEvent(&events.Receipt{})
	s.handleEvent(&events.Presence{})
	if got := s.droppedEvents.Load(); got != 0 {
		t.Fatalf("droppedEvents = %d, want 0 for a remote disconnect, a receipt and presence", got)
	}
}

func TestSessionCountsLateCallsAndStartsNoWork(t *testing.T) {
	s, ctx := openRetiredSession(t)
	if got := s.lateCalls.Load(); got != 0 {
		t.Fatalf("the retirement sequence's own Disconnect was counted: %d", got)
	}
	if err := s.Connect(ctx); !errors.Is(err, ErrSessionRetired) {
		t.Fatalf("late Connect: %v, want ErrSessionRetired", err)
	}
	if _, err := s.GetQRChannel(ctx); !errors.Is(err, ErrSessionRetired) {
		t.Fatalf("late GetQRChannel: %v, want ErrSessionRetired", err)
	}
	if err := s.RunRepairs(ctx); !errors.Is(err, ErrSessionRetired) {
		t.Fatalf("late RunRepairs: %v, want ErrSessionRetired", err)
	}
	s.Disconnect()
	if got := s.lateCalls.Load(); got != 4 {
		t.Fatalf("lateCalls = %d, want 4 (Connect, GetQRChannel, RunRepairs, Disconnect)", got)
	}
	if s.attemptedConnect() {
		t.Fatal("a refused late Connect must not count as an attempted Connect")
	}
}

func TestSessionConnectAbortedThroughTheRecordedAbortIsNotALateCall(t *testing.T) {
	s, _ := openRetiredSession(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errAttemptAborted)
	if err := s.Connect(ctx); !errors.Is(err, ErrSessionRetired) {
		t.Fatalf("Connect: %v, want ErrSessionRetired", err)
	}
	if got := s.lateCalls.Load(); got != 0 {
		t.Fatalf("an attempt aborted through the recorded abort was counted: %d", got)
	}
	// A Connect that was not aborted that way is counted, also when its context was cancelled by something else.
	other, cancelOther := context.WithCancelCause(context.Background())
	cancelOther(errors.New("some other cause"))
	_ = s.Connect(other)
	_ = s.Connect(context.Background())
	if got := s.lateCalls.Load(); got != 2 {
		t.Fatalf("lateCalls = %d, want 2", got)
	}
}

// cleanObserver sees no library work and never waits on the real clock.
func cleanObserver() quietObserver {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	return quietObserver{
		dump: func(buf []byte) (int, error) { return copy(buf, "goroutine 1 [running]:\nmain.main()\n"), nil },
		self: "1",
		now:  clock.now,
		wait: clock.wait,
	}
}

func connectedAttempt(t *testing.T) *Session {
	t.Helper()
	st := openWADir(t)
	lifetime, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := newSessionTestConfig(st, lifetime)
	cfg.PreLoginHTTP = countingClient(&countingTransport{})
	cfg.WebsocketHTTP = countingClient(&countingTransport{})
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.observer = cleanObserver
	_ = s.Connect(lifetime) // refused by the transport; the attempt is what counts
	return s
}

func TestQuietStopNeverAttemptedIsNotChecked(t *testing.T) {
	s, _ := openRetiredSession(t)
	s.observer = cleanObserver
	res := s.QuietStop(context.Background(), time.Second)
	if !res.Passed || res.Reason != QuietNotChecked {
		t.Fatalf("result = %+v, want passed and not_checked", res)
	}
}

func TestQuietStopPassesAfterACleanRetirement(t *testing.T) {
	s := connectedAttempt(t)
	if err := s.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	res := s.QuietStop(context.Background(), time.Second)
	if !res.Passed || res.Reason != QuietPassed {
		t.Fatalf("result = %+v, want a pass", res)
	}
}

func TestQuietStopEachSourceFailsWithItsReason(t *testing.T) {
	cases := []struct {
		name   string
		inject func(t *testing.T, s *Session)
		want   QuietReason
	}{
		{"late call", func(t *testing.T, s *Session) { s.Disconnect() }, QuietCallRefused},
		{"dropped event", func(t *testing.T, s *Session) { s.handleEvent(&events.LoggedOut{}) }, QuietEventDropped},
		{"refused dial", func(t *testing.T, s *Session) {
			req, _ := http.NewRequest(http.MethodGet, "https://127.0.0.1:1/", nil)
			if resp, err := s.mediaClient.Do(req); err == nil {
				resp.Body.Close()
				t.Fatal("a media request after the fence closed succeeded")
			}
		}, QuietNetworkRefused},
		{"refused storage", func(t *testing.T, s *Session) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = s.dev.Identities.PutIdentity(ctx, "491511234567:0", [32]byte{1})
		}, QuietStorageRefused},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := connectedAttempt(t)
			if err := s.Retire(context.Background()); err != nil {
				t.Fatal(err)
			}
			c.inject(t, s)
			res := s.QuietStop(context.Background(), time.Second)
			if res.Passed || res.Reason != c.want {
				t.Fatalf("result = %+v, want a %s failure", res, c.want)
			}
		})
	}
}

func TestQuietStopFailsWhenLibraryWorkIsStillRunning(t *testing.T) {
	s := connectedAttempt(t)
	if err := s.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.observer = func() quietObserver {
		o := cleanObserver()
		o.dump = func(buf []byte) (int, error) {
			return copy(buf, "goroutine 1 [running]:\nmain.main()\n\ngoroutine 9 [select]:\ngo.mau.fi/whatsmeow.(*Client).keepAliveLoop(0x1)\n"), nil
		}
		return o
	}
	res := s.QuietStop(context.Background(), 300*time.Millisecond)
	if res.Passed || res.Reason != QuietWorkRunning || res.LibraryGoroutines != 1 {
		t.Fatalf("result = %+v, want work_running with one goroutine", res)
	}
}

func TestQuietStopAfterTheStopBoundFails(t *testing.T) {
	s := connectedAttempt(t)
	if err := s.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	res := s.QuietStop(ctx, time.Second)
	if res.Passed || res.Reason != QuietStopBoundExceeded {
		t.Fatalf("result = %+v, want stop_bound_exceeded", res)
	}
}

func TestQuietStopRemovesTheHandlerOnlyWhenTheCheckEnds(t *testing.T) {
	retired := connectedAttempt(t)
	if err := retired.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !retired.client.RemoveEventHandler(retired.handlerID) {
		t.Fatal("the handler was already removed when Retire returned")
	}
	checked := connectedAttempt(t)
	if err := checked.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = checked.QuietStop(context.Background(), time.Second)
	if checked.client.RemoveEventHandler(checked.handlerID) {
		t.Fatal("the handler was still registered after the check ended")
	}
}

// attemptedConnect reports whether a production Connect was attempted on this client.
func (s *Session) attemptedConnect() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accountWork
}

// A write on a connection the fence already closed is refused and counted, but it cannot reach the network (the
// connection is closed) and the TLS layer makes one while it tears down, so it does not fail the check. A dial does.
func TestQuietStopIgnoresWritesOnConnectionsTheFenceClosed(t *testing.T) {
	s := connectedAttempt(t)
	l := newFenceListener(t)
	c, err := s.fence.DialContext(context.Background(), "tcp", l.addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := s.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, err := c.Write([]byte("late")); err == nil || n != 0 {
		t.Fatalf("late write = %d, %v; want an error and 0 bytes", n, err)
	}
	if s.fence.RefusedWrites() != 1 {
		t.Fatalf("RefusedWrites = %d, want 1", s.fence.RefusedWrites())
	}
	res := s.QuietStop(context.Background(), time.Second)
	if !res.Passed || res.NetworkRefused != 0 {
		t.Fatalf("result = %+v, want a pass with no refused network use", res)
	}
}

// The real source of such a write: the TLS layer's close alert, sent by the HTTP transport after the fence closed.
func TestFenceTLSTeardownAfterCloseIsNotADial(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	f := newFence()
	c := f.HTTPClient(srv.Client())
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	f.Close()
	c.CloseIdleConnections()
	deadline := time.Now().Add(2 * time.Second)
	for f.RefusedWrites() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.RefusedWrites() == 0 {
		t.Fatal("TLS teardown wrote nothing after the fence closed; the test no longer exercises a late write")
	}
	if f.RefusedDials() != 0 {
		t.Fatalf("TLS teardown counted %d refused dials", f.RefusedDials())
	}
}
