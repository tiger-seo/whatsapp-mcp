package wa

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// quietSession is a fake session whose quiet-stop check returns a scripted result.
type quietSession struct {
	*fakeSession
	result QuietStopResult
	mu     sync.Mutex
	ctxs   []context.Context
	asked  []time.Duration
}

func (q *quietSession) QuietStop(ctx context.Context, deadline time.Duration) QuietStopResult {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ctxs = append(q.ctxs, ctx)
	q.asked = append(q.asked, deadline)
	return q.result
}

type quietRig struct {
	t    *testing.T
	st   *store.Store
	o    *connectionOwner
	sess *quietSession
	log  *memLog
}

func newQuietRig(t *testing.T, result QuietStopResult, adjust func(*OwnerConfig)) *quietRig {
	t.Helper()
	st, err := store.Open(context.Background(), waDataDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	logger, mem := newMemLog()
	r := &quietRig{t: t, st: st, log: mem}
	r.sess = &quietSession{fakeSession: &fakeSession{identity: true}, result: result}
	cfg := OwnerConfig{
		Log: logger, Store: st, Versions: bundledProvider(context.Background()), ShellStartupBudget: true,
		Sessions: func(c SessionConfig) (SessionHandle, error) { r.sess.cfg = c; return r.sess, nil },
	}
	if adjust != nil {
		adjust(&cfg)
	}
	r.o = NewOwner(cfg)
	if err := r.o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return r.sess.connectN.Load() == 1 })
	return r
}

func (r *quietRig) stop() {
	r.t.Helper()
	r.o.BeginStop()
	select {
	case <-r.o.Done():
	case <-time.After(10 * time.Second):
		r.t.Fatal("stop did not finish")
	}
}

func (r *quietRig) marker() string {
	r.t.Helper()
	state, _, err := r.st.RunGuard(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	return state
}

func passed() QuietStopResult { return QuietStopResult{Passed: true, Reason: QuietPassed} }

func TestStopClearsTheMarkerAfterAPassedCheck(t *testing.T) {
	r := newQuietRig(t, passed(), nil)
	r.stop()
	if got := r.marker(); got != "clean" {
		t.Fatalf("marker = %q, want clean", got)
	}
	if err := r.o.StopError(); err != nil {
		t.Fatalf("StopError = %v", err)
	}
	if !r.log.contains("quiet_stop_passed") {
		t.Error("no quiet_stop_passed event")
	}
	if r.log.contains("upstream_drain_unproven") {
		t.Error("upstream_drain_unproven is gone and must not be emitted")
	}
}

func TestStopKeepsTheMarkerUnsafeAfterAFailedCheck(t *testing.T) {
	for _, reason := range []QuietReason{QuietWorkRunning, QuietNetworkRefused, QuietStorageRefused, QuietCallRefused,
		QuietEventDropped, QuietStopBoundExceeded, QuietObservationIncomplete, QuietObservationError} {
		t.Run(string(reason), func(t *testing.T) {
			r := newQuietRig(t, QuietStopResult{Reason: reason, LibraryGoroutines: 2, StorageRefused: 3}, nil)
			r.stop()
			if got := r.marker(); got != "unsafe" {
				t.Fatalf("marker = %q, want unsafe", got)
			}
			if r.o.StopError() == nil {
				t.Error("a failed check reported a successful stop")
			}
			if !r.log.contains("quiet_stop_failed") || !r.log.contains(string(reason)) || !r.log.contains("run_guard_recovery_required") {
				t.Errorf("missing events: %v", r.log.lines)
			}
		})
	}
}

func TestStopWithoutAConnectAttemptIsNotChecked(t *testing.T) {
	r := newQuietRig(t, QuietStopResult{Passed: true, Reason: QuietNotChecked}, nil)
	r.stop()
	if got := r.marker(); got != "clean" {
		t.Fatalf("marker = %q, want clean", got)
	}
	if r.log.contains("quiet_stop_passed") || r.log.contains("quiet_stop_failed") {
		t.Error("an unchecked generation logged a decision")
	}
}

func TestPassingCheckDoesNotOverrideOtherUnsafeCauses(t *testing.T) {
	cases := map[string]func(*connectionOwner){
		"unresolved pairing":        func(o *connectionOwner) { o.mu.Lock(); o.unresolved = true; o.mu.Unlock() },
		"storage fault":             func(o *connectionOwner) { o.mu.Lock(); o.health.StorageFault = true; o.mu.Unlock() },
		"earlier failed generation": func(o *connectionOwner) { o.mu.Lock(); o.unsafeStop = true; o.mu.Unlock() },
	}
	for name, mark := range cases {
		t.Run(name, func(t *testing.T) {
			r := newQuietRig(t, passed(), nil)
			mark(r.o)
			r.stop()
			if got := r.marker(); got != "unsafe" {
				t.Fatalf("marker = %q, want unsafe", got)
			}
		})
	}
}

func TestCheckIsCappedByTheStopBound(t *testing.T) {
	r := newQuietRig(t, passed(), func(c *OwnerConfig) { c.StopBound = 3 * time.Second })
	before := time.Now()
	r.stop()
	r.sess.mu.Lock()
	defer r.sess.mu.Unlock()
	if len(r.sess.ctxs) != 1 {
		t.Fatalf("checks = %d, want 1", len(r.sess.ctxs))
	}
	deadline, ok := r.sess.ctxs[0].Deadline()
	if !ok {
		t.Fatal("the check had no stop-bound deadline")
	}
	if d := deadline.Sub(before); d < 2*time.Second || d > 4*time.Second {
		t.Fatalf("bound ends in %v, want about 3s", d)
	}
	if r.sess.asked[0] != quietStopDeadline {
		t.Fatalf("observation deadline = %v, want %v", r.sess.asked[0], quietStopDeadline)
	}
}

func TestTwoGenerationsKeepTheMarkerUnsafeIfEitherFailed(t *testing.T) {
	r := newQuietRig(t, passed(), nil)
	r.o.mu.Lock()
	gen := r.o.gen
	r.o.mu.Unlock()
	failing := &quietSession{fakeSession: &fakeSession{identity: true}, result: QuietStopResult{Reason: QuietWorkRunning}}
	r.o.checkQuiet(&genState{handle: failing, epoch: 9}, quietStopDeadline)
	r.o.checkQuiet(gen, quietStopDeadline)
	r.stop()
	if got := r.marker(); got != "unsafe" {
		t.Fatalf("marker = %q, want unsafe", got)
	}
}
