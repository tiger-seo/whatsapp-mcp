package wa

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

// sendingSession is a scripted generation that takes send calls. Each call runs hold with its context; Retire
// reports the retirement steps the real session reports.
type sendingSession struct {
	*fakeSession
	calls atomic.Int64
	hold  func(op string, ctx context.Context) error
}

func (s *sendingSession) run(op string, ctx context.Context) error {
	s.calls.Add(1)
	if s.hold != nil {
		return s.hold(op, ctx)
	}
	return nil
}

func (s *sendingSession) SendIdentity() SendIdentity {
	return SendIdentity{Phone: ownJID(), LID: types.NewJID(testOwnPhone, types.HiddenUserServer)}
}

func (s *sendingSession) LookupPhone(ctx context.Context, _ string) (Registration, error) {
	return Registration{Registered: true}, s.run("lookup", ctx)
}

func (s *sendingSession) UploadFile(ctx context.Context, _ io.Reader, _ io.ReadWriteSeeker, _ whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	return whatsmeow.UploadResponse{}, s.run("upload", ctx)
}

func (s *sendingSession) Deliver(ctx context.Context, _ types.JID, _ *waE2E.Message) (Delivery, error) {
	return Delivery{ID: "ID", Time: time.Now()}, s.run("deliver", ctx)
}

func (s *sendingSession) RecordSent(ctx context.Context, _ SentMessage) error {
	return s.run("record", ctx)
}

func (s *sendingSession) Retire(ctx context.Context) error {
	for _, step := range []string{"disconnect", "fence_closed", "barrier_retired"} {
		s.cfg.trace(step)
	}
	return s.fakeSession.Retire(ctx)
}

type sendOwnerRig struct {
	o     *connectionOwner
	sess  *sendingSession
	log   *memLog
	trace *stepLog
}

// newSendOwner returns an owner whose scripted generation is connected.
func newSendOwner(t *testing.T, adjust func(*OwnerConfig)) *sendOwnerRig {
	t.Helper()
	r := &sendOwnerRig{sess: &sendingSession{fakeSession: &fakeSession{identity: true}}, trace: &stepLog{}}
	logger, mem := newMemLog()
	r.log = mem
	versions := bundledProvider(context.Background())
	versions.selected.Store(&Version{2, 3000, 101})
	cfg := OwnerConfig{Log: logger, Versions: versions, trace: r.trace.add, ShellStartupBudget: true,
		Sessions: func(sc SessionConfig) (SessionHandle, error) {
			r.sess.cfg = sc
			return r.sess, nil
		}}
	if adjust != nil {
		adjust(&cfg)
	}
	r.o = NewOwner(cfg)
	if err := r.o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The event follows the startup attempt's Connect, which a connection confirmed earlier would skip.
	waitFor(t, func() bool { return r.sess.connectN.Load() == 1 })
	r.sess.send(SessionConnected)
	r.o.waitStartupDoneForTest(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.o.stopForTest(ctx); err != nil {
			t.Errorf("stop: %v", err)
		}
	})
	return r
}

// portCalls makes every port call once and returns each error.
func portCalls(ctx context.Context, port SendPort) map[string]error {
	out := map[string]error{"ready": port.Ready()}
	_, out["lookup"] = port.LookupPhone(ctx, "491511000001")
	_, out["upload"] = port.UploadFile(ctx, nil, nil, whatsmeow.MediaImage)
	_, out["deliver"] = port.Deliver(ctx, peerJID(), &waE2E.Message{}, nil)
	out["record"] = port.Record(ctx, SentMessage{})
	return out
}

func TestSendOwnerAdmitsOnlyAConnectedGeneration(t *testing.T) {
	r := newSendOwner(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for op, err := range portCalls(ctx, r.o) {
		if err != nil {
			t.Errorf("connected: %s = %v", op, err)
		}
	}
	if got := r.o.OwnIdentity(); got.Phone != ownJID() {
		t.Errorf("identity = %+v", got)
	}
	gen := r.o.gen
	refused := []struct {
		name string
		set  func()
	}{
		{"idle", func() { r.o.connected, r.o.phase = false, PhaseIdle }},
		{"connecting", func() { r.o.connected, r.o.phase = false, PhaseConnecting }},
		{"pairing", func() { r.o.connected, r.o.phase = false, PhasePairing }},
		{"pairing over a connected socket", func() { r.o.phase = PhasePairing }},
		{"disconnected", func() { r.o.connected, r.o.phase = false, PhaseDisconnected }},
		{"logged out", func() { r.o.connected, r.o.phase = false, PhaseLoggedOut }},
		{"retiring generation", func() { gen.retiring = true }},
		{"storage failed", func() { r.o.health.StorageFault = true }},
		{"owner stopping", func() { r.o.stopping = true }},
		{"no generation", func() { r.o.gen = nil }},
	}
	for _, tt := range refused {
		r.o.mu.Lock()
		tt.set()
		r.o.mu.Unlock()
		before := r.sess.calls.Load()
		for op, err := range portCalls(ctx, r.o) {
			if !errors.Is(err, ErrNotConnected) {
				t.Errorf("%s: %s = %v, want ErrNotConnected", tt.name, op, err)
			}
			if op == "record" && !errors.Is(err, ErrRecordMissing) {
				t.Errorf("%s: record = %v, want ErrRecordMissing too", tt.name, err)
			}
		}
		if got := r.o.OwnIdentity(); got != (SendIdentity{}) {
			t.Errorf("%s: identity = %+v", tt.name, got)
		}
		if r.sess.calls.Load() != before {
			t.Errorf("%s: a refused call reached the session", tt.name)
		}
		r.o.mu.Lock()
		r.o.gen, gen.retiring = gen, false
		r.o.connected, r.o.phase, r.o.stopping, r.o.health.StorageFault = true, PhaseConnected, false, false
		r.o.mu.Unlock()
	}
	// A refused call never connects anything.
	if got := r.sess.connectN.Load(); got != 1 {
		t.Errorf("connect attempts = %d, want the startup attempt only", got)
	}
	if !r.log.contains("send_record_missing") {
		t.Error("a refused record was not reported")
	}
}

func TestSendOwnerCallContexts(t *testing.T) {
	r := newSendOwner(t, nil)
	var mu sync.Mutex
	seen := map[string]context.Context{}
	r.sess.hold = func(op string, ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		seen[op] = ctx
		return nil
	}
	deadline := time.Now().Add(time.Minute)
	caller, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	for op, err := range portCalls(caller, r.o) {
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
	}
	for _, op := range []string{"lookup", "upload", "deliver"} {
		if got, ok := seen[op].Deadline(); !ok || !got.Equal(deadline) {
			t.Errorf("%s deadline = %v, %v; want the caller's", op, got, ok)
		}
	}
	if got, ok := seen["record"].Deadline(); !ok || time.Until(got) > recordLimit {
		t.Errorf("record deadline = %v, %v; want at most %v", got, ok, recordLimit)
	}
	// A shorter remaining budget bounds the record.
	short, cancelShort := context.WithTimeout(context.Background(), time.Second)
	defer cancelShort()
	if err := r.o.Record(short, SentMessage{}); err != nil {
		t.Fatal(err)
	}
	if got, ok := seen["record"].Deadline(); !ok || time.Until(got) > time.Second {
		t.Errorf("record deadline = %v, %v; want the remaining second", got, ok)
	}

	// A cancelled caller: lookup and upload end, deliver sends nothing, the record still runs.
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	before := r.sess.calls.Load()
	handed := false
	_, lookupErr := r.o.LookupPhone(cancelled, "1")
	_, uploadErr := r.o.UploadFile(cancelled, nil, nil, whatsmeow.MediaImage)
	_, deliverErr := r.o.Deliver(cancelled, peerJID(), &waE2E.Message{}, func() { handed = true })
	for _, err := range []error{lookupErr, uploadErr, deliverErr} {
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrOutcomeUnknown) {
			t.Errorf("cancelled caller: %v", err)
		}
	}
	if handed || r.sess.calls.Load() != before {
		t.Error("a cancelled caller reached the session")
	}
	if err := r.o.Record(cancelled, SentMessage{}); err != nil {
		t.Errorf("record after a cancel: %v", err)
	}
}

func TestSendOwnerCancellationReachesOnlyCallsBeforeTheHandOver(t *testing.T) {
	synctest.Test(t, testSendOwnerCancellation)
}

func testSendOwnerCancellation(t *testing.T) {
	r := newSendOwner(t, nil)
	for _, tt := range []struct {
		op       string
		followed bool
	}{{"lookup", true}, {"upload", true}, {"deliver", false}, {"record", false}} {
		entered, release := make(chan struct{}), make(chan struct{})
		r.sess.hold = func(_ string, ctx context.Context) error {
			close(entered)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
				return nil
			}
		}
		caller, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			var err error
			switch tt.op {
			case "lookup":
				_, err = r.o.LookupPhone(caller, "1")
			case "upload":
				_, err = r.o.UploadFile(caller, nil, nil, whatsmeow.MediaImage)
			case "deliver":
				_, err = r.o.Deliver(caller, peerJID(), &waE2E.Message{}, nil)
			case "record":
				err = r.o.Record(caller, SentMessage{})
			}
			done <- err
		}()
		<-entered
		cancel()
		if tt.followed {
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("%s: %v, want the cancellation", tt.op, err)
			}
			continue
		}
		synctest.Wait()
		select {
		case err := <-done:
			t.Errorf("%s ended on the caller's cancel: %v", tt.op, err)
			continue
		default:
		}
		close(release)
		if err := <-done; err != nil {
			t.Errorf("%s: %v", tt.op, err)
		}
	}
}

func TestSendOwnerRetirementWaitsForSends(t *testing.T) {
	for _, op := range []string{"lookup", "upload", "deliver", "record"} {
		t.Run(op, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { testSendOwnerRetirement(t, op) })
		})
	}
}

func testSendOwnerRetirement(t *testing.T, op string) {
	r := newSendOwner(t, nil)
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r.sess.hold = func(_ string, ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		r.trace.add("send_returned")
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- portCalls1(r.o, op) }()
	<-entered
	r.o.BeginStop()
	<-cancelled
	synctest.Wait()
	if steps := r.trace.get(); slices.Contains(steps, "disconnect") {
		t.Errorf("retirement disconnected before the send returned: %v", steps)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.o.stopForTest(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; op == "deliver" && !errors.Is(err, ErrOutcomeUnknown) {
		t.Errorf("deliver stopped by retirement = %v, want unknown", err)
	}
	steps := r.trace.get()
	last := -1
	for _, want := range []string{"work_cancelled", "send_returned", "sends_waited", "disconnect", "fence_closed", "barrier_retired"} {
		i := slices.Index(steps, want)
		if i <= last {
			t.Fatalf("step %q out of order in %v", want, steps)
		}
		last = i
	}
	if err := r.o.StopError(); err != nil {
		t.Errorf("StopError = %v", err)
	}
}

// portCalls1 makes one port call without a caller deadline.
func portCalls1(port SendPort, op string) error {
	ctx := context.Background()
	var err error
	switch op {
	case "lookup":
		_, err = port.LookupPhone(ctx, "1")
	case "upload":
		_, err = port.UploadFile(ctx, nil, nil, whatsmeow.MediaImage)
	case "deliver":
		_, err = port.Deliver(ctx, peerJID(), &waE2E.Message{}, nil)
	case "record":
		err = port.Record(ctx, SentMessage{})
	}
	return err
}

func TestSendOwnerStuckSendMakesTheStopUnsafe(t *testing.T) {
	synctest.Test(t, testSendOwnerStuckSend)
}

func testSendOwnerStuckSend(t *testing.T) {
	r := newSendOwner(t, func(cfg *OwnerConfig) { cfg.drainBound = 100 * time.Millisecond })
	entered, release := make(chan struct{}), make(chan struct{})
	r.sess.hold = func(string, context.Context) error {
		close(entered)
		<-release
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- portCalls1(r.o, "deliver") }()
	<-entered
	r.o.BeginStop()
	synctest.Wait()
	synctest.Sleep(100 * time.Millisecond)
	synctest.Wait()
	if !r.log.contains("owner_drain_failed") {
		t.Error("the stuck send was not reported")
	}
	// The generation is not released while the call still runs.
	select {
	case <-r.o.Done():
		t.Error("the owner finished while a send was running")
	default:
	}
	close(release)
	<-done
	select {
	case <-r.o.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the owner did not finish after the send returned")
	}
	r.o.mu.Lock()
	unsafe := r.o.unsafeStop
	r.o.mu.Unlock()
	if !unsafe {
		t.Error("a send that outlived the drain bound left the stop safe")
	}
	if steps := r.trace.get(); slices.Contains(steps, "sends_waited") {
		t.Errorf("steps = %v; the wait did not succeed", steps)
	}
}

func TestSendOwnerAdmitsNothingAfterTheStopBegan(t *testing.T) {
	r := newSendOwner(t, nil)
	r.o.BeginStop()
	if err := portCalls1(r.o, "deliver"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("deliver after BeginStop = %v", err)
	}
}
