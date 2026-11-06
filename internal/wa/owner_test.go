package wa

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.mau.fi/whatsmeow"
)

// memLog captures log messages for diagnostic assertions.
type memLog struct {
	mu    sync.Mutex
	lines []string
}

func newMemLog() (*slog.Logger, *memLog) {
	l := &memLog{}
	return slog.New(l), l
}

func (l *memLog) Enabled(context.Context, slog.Level) bool { return true }
func (l *memLog) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *memLog) WithGroup(string) slog.Handler            { return l }
func (l *memLog) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteByte('=')
		b.WriteString(a.Value.String())
		return true
	})
	l.mu.Lock()
	l.lines = append(l.lines, b.String())
	l.mu.Unlock()
	return nil
}

func (l *memLog) contains(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

// ownerTest bundles an owner with its scripted session and logger. Everything the owner's
// goroutines block on must be created in the same fake-time bubble as the owner, so the tests call
// newOwnerTest inside synctest.Test.
type ownerTest struct {
	o    *connectionOwner
	sess *fakeSession
	log  *memLog
}

func newOwnerTest(t testing.TB, shellBudget, identity bool, script func(n int64, ctx context.Context) error) *ownerTest {
	t.Helper()
	ot := &ownerTest{}
	logger, mem := newMemLog()
	ot.log = mem
	ot.sess = &fakeSession{identity: identity, script: script}
	vp := bundledProvider(context.Background())
	vp.selected.Store(&Version{2, 3000, 101})
	ot.o = NewOwner(OwnerConfig{
		Log:                logger,
		Versions:           vp,
		Sessions:           fakeOwnerFactory(func(cfg SessionConfig) *fakeSession { return ot.sess }),
		ShellStartupBudget: shellBudget,
	})
	return ot
}

// stopInBubble stops the owner inside a fake-time bubble so it closes without leftover goroutines.
func stopInBubble(t testing.TB, ot *ownerTest) {
	t.Helper()
	ot.o.BeginStop()
	synctest.Wait()
}

func TestOwnerStatusFourFieldsAndCopiedSnapshots(t *testing.T) {
	ot := newOwnerTest(t, false, true, errScript(map[int64]error{1: errors.New("refused")}))
	if err := ot.o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	ot.o.waitStartupDoneForTest(t)
	st := ot.o.Snapshot()
	if st.Connected {
		t.Error("Connected = true without a confirmed connection")
	}
	if !st.LoggedIn {
		t.Error("LoggedIn = false with a locally saved identity")
	}
	if st.PairingRequired {
		t.Error("PairingRequired = true for a saved session")
	}
	if st.WAVersion != "2.3000.101" {
		t.Errorf("WAVersion = %q, want the selected candidate", st.WAVersion)
	}
	// Snapshots are copied values: mutating one must not affect later snapshots.
	copied := st
	copied.Connected = true
	copied.LoggedIn = false
	copied.WAVersion = "tampered"
	if again := ot.o.Snapshot(); again.Connected || !again.LoggedIn || again.WAVersion != "2.3000.101" {
		t.Errorf("snapshot was aliased: %+v", again)
	}
	if pair, ok := ot.o.PairingSnapshot(); ok {
		t.Errorf("PairingSnapshot = %+v, %v; want no window", pair, ok)
	}
	if err := ot.o.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestOwnerSavedButUnverifiedInitialAndOrdinaryDisconnect(t *testing.T) {
	ot := newOwnerTest(t, false, true, func(n int64, _ context.Context) error { return nil })
	if err := ot.o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// The locally saved identity is shown before any server confirmation.
	st := ot.o.Snapshot()
	if !st.LoggedIn || st.Connected {
		t.Fatalf("initial saved state = %+v", st)
	}
	ot.sess.send(SessionConnected)
	st = ot.o.Snapshot()
	if !st.Connected || !st.LoggedIn {
		t.Fatalf("state after Connected = %+v", st)
	}
	// An ordinary disconnect preserves the identity and never triggers pairing.
	ot.sess.send(SessionDisconnected)
	st = ot.o.Snapshot()
	if st.Connected || !st.LoggedIn || st.PairingRequired {
		t.Fatalf("state after ordinary disconnect = %+v", st)
	}
	if err := ot.o.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestOwnerDeniesStaleEpoch(t *testing.T) {
	ot := newOwnerTest(t, false, false, nil)
	if err := ot.o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	ot.sess.sendStale(SessionPairSuccess)
	ot.sess.sendStale(SessionConnected)
	ot.sess.sendStale(SessionLoggedOut)
	if st := ot.o.Snapshot(); st.LoggedIn || st.Connected || st.PairingRequired {
		t.Fatalf("stale events changed the status: %+v", st)
	}
	// The current epoch is still admitted.
	ot.sess.send(SessionPairSuccess)
	if st := ot.o.Snapshot(); !st.LoggedIn {
		t.Fatal("current epoch PairSuccess was denied")
	}
	if err := ot.o.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestOwnerWatchdogCancelsStalledDialBeforeDisconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, false, true, stallScript())
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Fatalf("connects = %d, want 1", got)
		}
		if ot.o.trySlotForTest() {
			t.Error("the attempt slot was free while the dial was stalled")
		}
		synctest.Sleep(30 * time.Second)
		synctest.Wait()
		if ot.sess.connectN.Load() != 1 || ot.sess.disconnectN.Load() != 1 {
			t.Errorf("connects=%d disconnects=%d; want 1/1", ot.sess.connectN.Load(), ot.sess.disconnectN.Load())
		}
		if ot.o.phaseForTest() != PhaseDisconnected {
			t.Errorf("phase = %v, want disconnected", ot.o.phaseForTest())
		}
		if !ot.o.trySlotForTest() {
			t.Error("the attempt slot was not released after drain")
		}
		if !ot.sess.cancelledBeforeDisconnect.Load() {
			t.Error("Disconnect ran while the dial was still running")
		}
		if ot.sess.maxSeen.Load() > 1 {
			t.Errorf("attempts overlapped: %d", ot.sess.maxSeen.Load())
		}
		if st := ot.o.Snapshot(); !st.LoggedIn || st.Connected {
			t.Errorf("status after watchdog timeout = %+v", st)
		}
		stopInBubble(t, ot)
	})
}

func TestOwnerHealthyConnectionSurvivesWatchdog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, false, true, func(int64, context.Context) error { return nil })
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		ot.sess.send(SessionConnected)
		// Run well past the 30s watchdog deadline; the connection must survive it.
		synctest.Sleep(45 * time.Second)
		synctest.Wait()
		if ot.sess.disconnectN.Load() != 0 {
			t.Error("the watchdog disconnected a healthy connection")
		}
		if st := ot.o.Snapshot(); !st.Connected {
			t.Error("the confirmed connection did not survive the watchdog deadline")
		}
		stopInBubble(t, ot)
	})
}

func TestOwnerConnectedRetainsConnectContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		contexts := make(chan context.Context, 1)
		ot := newOwnerTest(t, false, true, func(_ int64, ctx context.Context) error {
			contexts <- ctx
			return nil
		})
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx := <-contexts
		ot.sess.send(SessionConnected)
		synctest.Wait()
		if ctx.Err() != nil {
			t.Error("confirmed connection context was cancelled")
		}
		stopInBubble(t, ot)
		if ctx.Err() == nil {
			t.Error("connection context survived retirement")
		}
	})
}

func TestOwnerStorageFaultRetiresConnectedGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, false, true, func(int64, context.Context) error { return nil })
		ot.sess.faultCh = make(chan struct{})
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		ot.sess.send(SessionConnected)
		synctest.Wait()
		close(ot.sess.faultCh)
		synctest.Wait()
		if ot.o.Snapshot().Connected {
			t.Error("storage fault retained connected status")
		}
		if ot.sess.retiredN.Load() != 1 {
			t.Error("storage fault did not retire generation")
		}
		stopInBubble(t, ot)
	})
}

type blockedDisconnectSession struct {
	*fakeSession
	entered chan struct{}
	release chan struct{}
}

func (s *blockedDisconnectSession) Disconnect() {
	s.fakeSession.Disconnect()
	close(s.entered)
	<-s.release
}

func TestOwnerWatchdogDrainsBeforeSlotRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, false, true, stallScript())
		s := &blockedDisconnectSession{fakeSession: ot.sess, entered: make(chan struct{}), release: make(chan struct{})}
		ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) { ot.sess.cfg = cfg; return s, nil }
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		synctest.Sleep(connectWatchdog)
		<-s.entered
		synctest.Wait()
		if ot.o.trySlotForTest() {
			t.Error("slot released while watchdog disconnect was running")
		}
		ot.sess.send(SessionConnected)
		if ot.o.Snapshot().Connected {
			t.Error("late confirmation accepted during timeout")
		}
		close(s.release)
		synctest.Wait()
		// Retirement can disconnect again, so restore the ordinary fake for shutdown.
		s.entered = make(chan struct{}, 1)
		s.release = make(chan struct{})
		close(s.release)
		stopInBubble(t, ot)
	})
}

var errRefused = errors.New("synthetic refusal")

func TestOwnerStartupCancellationDuringRetryWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, errScript(map[int64]error{1: errRefused}))
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		for ot.sess.connectN.Load() < 1 {
			synctest.Sleep(time.Second)
		}
		// Stop while the five-second retry wait is pending.
		ot.o.BeginStop()
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects = %d, want 1; the cancelled sequence must not start retries", got)
		}
		if ot.sess.retiredN.Load() != 1 {
			t.Errorf("retires = %d, want the generation retired", ot.sess.retiredN.Load())
		}
		if p := ot.o.phaseForTest(); p != PhaseStopped {
			t.Errorf("phase = %v, want stopped", p)
		}
		select {
		case <-ot.o.Done():
		default:
			t.Error("Done did not close after stop")
		}
	})
}

func TestOwnerLogoutCancelsSequenceAndRetiresGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, errScript(map[int64]error{1: errRefused}))
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		for ot.sess.connectN.Load() < 1 {
			synctest.Sleep(time.Second)
		}
		ot.sess.send(SessionLoggedOut)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects = %d, want 1; logout must cancel the startup sequence", got)
		}
		if ot.sess.retiredN.Load() != 1 {
			t.Errorf("retires = %d, want 1", ot.sess.retiredN.Load())
		}
		if st := ot.o.Snapshot(); st.LoggedIn || st.Connected {
			t.Fatalf("status after logout = %+v", st)
		}
		if p := ot.o.phaseForTest(); p != PhaseLoggedOut {
			t.Errorf("phase = %v, want logged_out", p)
		}
		stopInBubble(t, ot)
	})
}

func TestOwnerStorageFaultCancelsSequence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, errScript(map[int64]error{1: errRefused, 2: errRefused}))
		ot.sess.faultCh = make(chan struct{})
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		for ot.sess.connectN.Load() < 1 {
			synctest.Sleep(time.Second)
		}
		close(ot.sess.faultCh)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects = %d, want 1; a storage fault must cancel the sequence", got)
		}
		if h := ot.o.Health(); !h.StorageFault {
			t.Errorf("health = %+v, want the storage fault reported", h)
		}
		if p := ot.o.phaseForTest(); p != PhaseStorageFailed {
			t.Errorf("phase = %v, want storage_failed", p)
		}
		stopInBubble(t, ot)
	})
}

func TestOwnerClientOutdatedHandsOffStartupBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, errScript(map[int64]error{1: errRefused}))
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		for ot.sess.connectN.Load() < 1 {
			synctest.Sleep(time.Second)
		}
		ot.sess.send(SessionClientOutdated)
		synctest.Sleep(6 * time.Second)
		synctest.Wait()
		if got := ot.sess.connectN.Load(); got != 1 {
			t.Errorf("connects = %d, want 1; the 405 handoff must stop the startup budget", got)
		}
		if st := ot.o.Snapshot(); !st.LoggedIn || st.Connected {
			t.Fatalf("status after 405 handoff = %+v; the identity must be preserved", st)
		}
		if _, ok := ot.o.PairingSnapshot(); ok {
			t.Error("the 405 handoff opened a pairing window")
		}
		stopInBubble(t, ot)
	})
}

func TestOwnerStopWaitsForConnectReturnAndClearsStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cancelled, release := make(chan struct{}), make(chan struct{})
		ot := newOwnerTest(t, false, true, func(_ int64, ctx context.Context) error {
			<-ctx.Done()
			close(cancelled)
			<-release
			return ctx.Err()
		})
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		ot.sess.send(SessionConnected)
		ot.o.BeginStop()
		<-cancelled
		synctest.Wait()
		if ot.o.Snapshot().Connected {
			t.Error("stop retained connected status")
		}
		select {
		case <-ot.o.Done():
			t.Error("stop completed before Connect returned")
		default:
		}
		close(release)
		<-ot.o.Done()
	})
}

func TestOwnerStopWaitsForGenerationConstruction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		ot := newOwnerTest(t, false, false, nil)
		ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) {
			close(entered)
			<-release
			ot.sess.cfg = cfg
			return ot.sess, nil
		}
		startResult := make(chan error, 1)
		go func() { startResult <- ot.o.Start(context.Background()) }()
		<-entered
		ot.o.BeginStop()
		synctest.Wait()
		select {
		case <-ot.o.Done():
			t.Error("stop completed before generation construction returned")
		default:
		}
		close(release)
		if err := <-startResult; !errors.Is(err, ErrStopping) {
			t.Errorf("Start = %v, want ErrStopping", err)
		}
		<-ot.o.Done()
		if got := ot.sess.retiredN.Load(); got != 1 {
			t.Errorf("retirements = %d, want 1", got)
		}
	})
}

// qrSession is a fake session that can open a pairing window fed by the test.
type qrSession struct {
	fakeSession
	qr chan whatsmeow.QRChannelItem
}

func (q *qrSession) GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return q.qr, nil
}

func TestOwnerPairingSnapshotIsCopied(t *testing.T) {
	ot := newOwnerTest(t, false, false, func(int64, context.Context) error { return nil })
	qs := &qrSession{fakeSession: fakeSession{}, qr: make(chan whatsmeow.QRChannelItem, 8)}
	ot.o.cfg.Sessions = func(cfg SessionConfig) (SessionHandle, error) {
		qs.fakeSession.cfg = cfg
		return qs, nil
	}
	if err := ot.o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := ot.o.RequestPairing(context.Background()); err != nil {
		t.Fatalf("RequestPairing: %v", err)
	}
	qs.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "synthetic", Timeout: 20 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if snap, ok := ot.o.PairingSnapshot(); ok && snap.Code == "synthetic" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the code never reached the snapshot")
		}
		time.Sleep(time.Millisecond)
	}
	tampered, _ := ot.o.PairingSnapshot()
	tampered.Code = "tampered"
	tampered.Result = PairingCancelled
	if again, _ := ot.o.PairingSnapshot(); again.Code != "synthetic" || again.Result != PairingActive {
		t.Errorf("pairing snapshot was aliased: %+v", again)
	}
	if err := ot.o.stopForTest(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}
