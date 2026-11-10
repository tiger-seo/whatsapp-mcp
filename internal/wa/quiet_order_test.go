package wa

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

// retirementOrder is the sequence the contract fixes for one generation, ending with the quiet-stop check.
var retirementOrder = []string{"work_cancelled", "disconnect", "fence_closed", "barrier_retired", "lib_cancelled", "observe", "handler_removed", "quiet_decided"}

// checkRetirementOrder reports the first violation of the contract's order in got, or nil. Steps may be
// missing (a generation that never connected has no disconnect to trace), but the ones present keep their order.
func checkRetirementOrder(got []string) error {
	last := -1
	for _, step := range got {
		i := slices.Index(retirementOrder, step)
		if i < 0 {
			return fmt.Errorf("unknown step %q", step)
		}
		if i < last {
			return fmt.Errorf("step %q ran after %q", step, retirementOrder[last])
		}
		last = i
	}
	return nil
}

func TestRetirementOrderCheckerRejectsEveryViolation(t *testing.T) {
	good := slices.Clone(retirementOrder)
	if err := checkRetirementOrder(good); err != nil {
		t.Fatalf("the contract order was rejected: %v", err)
	}
	swapped := func(a, b string) []string {
		out := slices.Clone(good)
		i, j := slices.Index(out, a), slices.Index(out, b)
		out[i], out[j] = out[j], out[i]
		return out
	}
	for name, seq := range map[string][]string{
		"fence and barrier swapped":       swapped("fence_closed", "barrier_retired"),
		"library cancel before barrier":   swapped("barrier_retired", "lib_cancelled"),
		"library cancel before the fence": swapped("fence_closed", "lib_cancelled"),
		"disconnect before work cancel":   swapped("work_cancelled", "disconnect"),
		"library cancel first":            append([]string{"lib_cancelled"}, good[:4]...),
	} {
		if err := checkRetirementOrder(seq); err == nil {
			t.Errorf("%s: the checker accepted %v", name, seq)
		}
	}
}

// stepLog records trace steps from several goroutines.
type stepLog struct {
	mu    sync.Mutex
	steps []string
}

func (l *stepLog) add(step string) {
	l.mu.Lock()
	l.steps = append(l.steps, step)
	l.mu.Unlock()
}

func (l *stepLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.steps)
}

// A connected run retires in the contract order against the real adapter, store and simulator: the program's own
// work first, then the library's disconnect, the fence, the store barrier, and the library context last.
func TestRetirementOrderOfAConnectedRun(t *testing.T) {
	trace := &stepLog{}
	r := newOwnerRigWith(t, func(cfg *OwnerConfig) { cfg.trace = trace.add })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	conn := r.pairUntilConfirmed(ctx, true)
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	conn2, err := r.srv.Accept(ctx)
	if err != nil {
		t.Fatalf("no reconnect: %v", err)
	}
	if err := conn2.SendSuccess(ctx, r.phone.LID); err != nil {
		t.Fatal(err)
	}
	r.waitConnected(ctx)
	r.owner.BeginStop()
	select {
	case <-r.owner.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("stop did not finish")
	}
	got := trace.get()
	// A fresh-client link retires two generations in turn: the pairing one, then the connected one on stop. Each
	// follows the whole order, and the second starts only after the first was decided.
	if len(got) != 2*len(retirementOrder) {
		t.Fatalf("steps = %v, want two generations of %v", got, retirementOrder)
	}
	for i := 0; i < 2; i++ {
		part := got[i*len(retirementOrder) : (i+1)*len(retirementOrder)]
		if err := checkRetirementOrder(part); err != nil {
			t.Fatalf("generation %d order %v: %v", i, part, err)
		}
		if !slices.Equal(part, retirementOrder) {
			t.Fatalf("generation %d steps = %v, want %v", i, part, retirementOrder)
		}
	}
}

// The library context of a connection that is up is not cancelled by the stop signal or by the work context; it is
// cancelled only when retirement has blocked the network and the store.
func TestStopSignalLeavesTheLibraryContextAlone(t *testing.T) {
	f := &fakeSession{identity: true, script: func(int64, context.Context) error { return nil }}
	o := NewOwner(OwnerConfig{
		Versions: bundledProvider(context.Background()),
		Sessions: func(cfg SessionConfig) (SessionHandle, error) { f.cfg = cfg; return f, nil },
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return f.connectN.Load() == 1 })
	o.mu.Lock()
	gen := o.gen
	o.mu.Unlock()
	f.send(SessionConnected)
	waitFor(t, func() bool { return o.Snapshot().Connected })
	o.BeginStop()
	waitFor(t, func() bool { return gen.work.Err() != nil })
	if err := gen.lib.Err(); err != nil {
		t.Fatalf("the stop signal reached the library context: %v", err)
	}
	if ctxp := f.attemptCtx.Load(); ctxp != nil && (*ctxp).Err() != nil {
		t.Fatalf("the stop signal cancelled the attempt context of a returned Connect: %v", (*ctxp).Err())
	}
	select {
	case <-o.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not finish")
	}
	if gen.lib.Err() == nil {
		t.Fatal("retirement left the library context running")
	}
}

// A startup attempt whose Connect returned and whose connection then ends before the connected event must not
// cancel the handlers' context.
func TestFailedStartupAttemptKeepsTheHandlerContext(t *testing.T) {
	f := &fakeSession{identity: true, script: func(int64, context.Context) error { return nil }}
	o := NewOwner(OwnerConfig{
		Versions:           bundledProvider(context.Background()),
		ShellStartupBudget: true,
		Sessions:           func(cfg SessionConfig) (SessionHandle, error) { f.cfg = cfg; return f, nil },
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return f.connectN.Load() == 1 })
	f.send(SessionDisconnected) // outcomeFailed
	waitFor(t, func() bool { return o.Snapshot().Connected == false && o.phaseNow() == PhaseDisconnected })
	ctxp := f.attemptCtx.Load()
	if ctxp == nil {
		t.Fatal("no attempt context")
	}
	if err := (*ctxp).Err(); err != nil {
		t.Fatalf("a failed attempt cancelled the handlers' context: %v", err)
	}
	o.BeginStop()
	<-o.Done()
}

// A stop that arrives while a Connect has not returned aborts it at once through the recorded abort, with the
// aborted cause, and the refusal count starts first.
func TestStopAbortsAnUnreturnedConnectSynchronously(t *testing.T) {
	entered := make(chan struct{})
	causes := make(chan error, 1)
	f := &fakeSession{identity: true, script: func(_ int64, ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		causes <- context.Cause(ctx)
		return ctx.Err()
	}}
	marks := &markingSession{fakeSession: f}
	o := NewOwner(OwnerConfig{
		Versions: bundledProvider(context.Background()),
		Sessions: func(cfg SessionConfig) (SessionHandle, error) { f.cfg = cfg; return marks, nil },
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-entered
	o.BeginStop()
	select {
	case cause := <-causes:
		if cause != errAttemptAborted {
			t.Fatalf("cause = %v, want the aborted sentinel", cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("BeginStop did not abort the unreturned Connect")
	}
	if marks.marked.Load() == 0 {
		t.Fatal("the refusal count did not start before the abort")
	}
	<-o.Done()
}

// No Connect begins after BeginStop returned.
func TestNoConnectBeginsAfterBeginStop(t *testing.T) {
	f := &fakeSession{identity: true}
	o := NewOwner(OwnerConfig{
		Versions: bundledProvider(context.Background()),
		Sessions: func(cfg SessionConfig) (SessionHandle, error) { f.cfg = cfg; return f, nil },
	})
	o.mu.Lock()
	gen := &genState{handle: f}
	gen.work, gen.workCancel = context.WithCancel(o.ctx)
	gen.lib, gen.libCancel = context.WithCancel(context.WithoutCancel(o.ctx))
	o.gen = gen
	o.mu.Unlock()
	o.BeginStop()
	if outcome := o.attempt(gen, attemptStartup); outcome != outcomeStopped {
		t.Fatalf("outcome = %v, want stopped", outcome)
	}
	if f.connectN.Load() != 0 {
		t.Fatal("a Connect began after BeginStop")
	}
	<-o.Done()
}

// markingSession is a fake session that counts MarkRetiring calls.
type markingSession struct {
	*fakeSession
	marked atomic.Int64
}

func (m *markingSession) MarkRetiring() { m.marked.Add(1) }

func (o *connectionOwner) phaseNow() Phase {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.phase
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached in time")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// The connect watchdog of an attempt whose Connect has already returned only disconnects: the handlers' context
// stays alive, so a handler that is writing in the gap completes.
func TestWatchdogAfterConnectReturnedLeavesTheHandlerContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, false, true, func(int64, context.Context) error { return nil })
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		synctest.Wait()
		ot.o.mu.Lock()
		gen := ot.o.gen
		ot.o.mu.Unlock()
		synctest.Sleep(connectWatchdog)
		synctest.Wait()
		if ot.sess.disconnectN.Load() != 1 {
			t.Fatalf("disconnects = %d, want the watchdog's one", ot.sess.disconnectN.Load())
		}
		if ot.o.phaseForTest() != PhaseDisconnected {
			t.Fatalf("phase = %v, want disconnected", ot.o.phaseForTest())
		}
		ctxp := ot.sess.attemptCtx.Load()
		if ctxp == nil || (*ctxp).Err() != nil {
			t.Fatalf("the watchdog cancelled the handlers' context after Connect returned")
		}
		if gen.lib.Err() != nil {
			t.Fatalf("the library context was cancelled by the watchdog")
		}
		stopInBubble(t, ot)
		if gen.lib.Err() == nil {
			t.Fatal("retirement left the library context running")
		}
	})
}

// Window (a): the stop calls the recorded abort in the instant after Connect returned and before the owner
// cleared it. The refusal count has started, and the generation records that its handlers' context was cancelled.
func TestStopInTheGapAfterConnectReturnedIsRecorded(t *testing.T) {
	f := &fakeSession{identity: true, script: func(int64, context.Context) error { return nil }}
	marks := &markingSession{fakeSession: f}
	var o *connectionOwner
	o = NewOwner(OwnerConfig{
		Versions:     bundledProvider(context.Background()),
		Sessions:     func(cfg SessionConfig) (SessionHandle, error) { f.cfg = cfg; return marks, nil },
		afterConnect: func() { o.BeginStop() },
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	o.mu.Lock()
	gen := o.gen
	o.mu.Unlock()
	<-o.Done()
	o.mu.Lock()
	late := gen.abortLate
	o.mu.Unlock()
	if !late {
		t.Fatal("the abort called after Connect returned was not recorded")
	}
	if marks.marked.Load() == 0 {
		t.Fatal("the refusal count did not start before the abort")
	}
}

// startSavedRun connects a run from a saved identity against the simulator and returns the owner, its
// generation and the real session.
func startSavedRun(t *testing.T, ctx context.Context) (*connectionOwner, *genState, *Session, *store.Store) {
	o, gen, sess, st, _, _ := startSavedRunWith(t, ctx, nil)
	return o, gen, sess, st
}

// startSavedRunWith also takes a retirement trace and returns the data directory and the simulator.
func startSavedRunWith(t *testing.T, ctx context.Context, trace func(string)) (*connectionOwner, *genState, *Session, *store.Store, string, *wasim.Server) {
	t.Helper()
	dir := waDataDir(t)
	st, err := store.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var first capture
	o1 := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: bundledProvider(ctx), Sessions: first.factory()})
	if err := o1.Start(ctx); err != nil {
		t.Fatal(err)
	}
	armAndSave(t, first.handle.(*Session))
	if err := o1.stopForTest(ctx); err != nil {
		t.Fatal(err)
	}
	st.Close(ctx)
	st, err = store.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	srv := wasim.Start(t)
	logger, mem := newMemLog()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("owner log: %v", mem.lines)
			buf := make([]byte, 4<<20)
			for _, block := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
				if all, _ := libraryCounts([]byte(block)); all > 0 {
					t.Logf("library goroutine left:\n%s", block)
				}
			}
		}
	})
	o := NewOwner(OwnerConfig{
		Log: logger, Store: st, Versions: bundledProvider(ctx), ShellStartupBudget: true, trace: trace,
		Sessions: func(cfg SessionConfig) (SessionHandle, error) {
			cfg.PreLoginHTTP = srv.Client()
			cfg.WebsocketHTTP = srv.Client()
			return OpenSession(cfg)
		},
	})
	if err := o.Start(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := srv.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SendSuccess(ctx, types.NewJID(testOwnPhone, types.HiddenUserServer)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return o.Snapshot().Connected })
	o.mu.Lock()
	gen := o.gen
	o.mu.Unlock()
	return o, gen, gen.handle.(*Session), st, dir, srv
}

// A pinned handler that has committed a decrypt transaction and is about to store an app-state key when the
// stop signal arrives finishes the write: the handler context is not cancelled, the key is stored, and the check
// passes.
func TestHandlerWritingWhenTheStopArrivesLosesNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	o, gen, sess, _ := startSavedRun(t, ctx)
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	id := []byte("keyid-lost-write")
	go func() {
		result <- sess.dev.EventBuffer.DoDecryptionTxn(gen.lib, func(tx context.Context) error {
			close(entered)
			<-release
			return sess.dev.AppStateKeys.PutAppStateSyncKey(tx, id, wastore.AppStateSyncKey{Data: []byte("data"), Timestamp: 1, Fingerprint: []byte("fp")})
		})
	}()
	<-entered
	o.BeginStop()
	waitFor(t, func() bool { return gen.work.Err() != nil })
	select {
	case <-o.Done():
		t.Fatal("stop finished while a pinned handler was still writing")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("the write failed: %v", err)
	}
	<-o.Done()
	if got := sess.barrier.Refused(); got != 0 {
		t.Fatalf("refused = %d, want 0", got)
	}
	sess.observer = cleanObserver
	if res := sess.QuietStop(ctx, time.Second); !res.Passed {
		t.Fatalf("result = %+v, want a pass", res)
	}
}

// Control: when the library context is cancelled first, the same write fails with context.Canceled, which is
// counted from the first cancellation and fails the check.
func TestWriteAfterLibraryCancelIsCountedAndFailsTheCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	o, gen, sess, _ := startSavedRun(t, ctx)
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- sess.dev.EventBuffer.DoDecryptionTxn(gen.lib, func(tx context.Context) error {
			close(entered)
			<-release
			return sess.dev.AppStateKeys.PutAppStateSyncKey(tx, []byte("k"), wastore.AppStateSyncKey{Data: []byte("d"), Timestamp: 1, Fingerprint: []byte("f")})
		})
	}()
	<-entered
	sess.MarkRetiring()
	gen.libCancel()
	close(release)
	if err := <-result; err == nil {
		t.Fatal("a write under a cancelled library context succeeded")
	}
	o.BeginStop()
	<-o.Done()
	sess.observer = cleanObserver
	res := sess.QuietStop(ctx, time.Second)
	if res.Passed || res.Reason != QuietStorageRefused {
		t.Fatalf("result = %+v, want storage_refused", res)
	}
}
