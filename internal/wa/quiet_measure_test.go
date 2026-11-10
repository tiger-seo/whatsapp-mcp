package wa

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
)

// The measurement runs the production owner, adapter and store against the simulator and reports how long
// client-library goroutines stay visible after retirement, for the owner to approve the two deadlines. It is
// not part of the normal suite: set WA_MEASURE=1 (and optionally WA_MEASURE_RUNS, default 50).
//
//	CGO_ENABLED=0 go test ./internal/wa -run TestQuietStopMeasurement -count=1 -v -timeout 30m

// measureSample is one orderly stop.
type measureSample struct {
	// latency is the time from the start of the observation (the library context was cancelled, which is when
	// QuietStop starts) to the first poll that showed no library goroutine; -1 when none was seen in time.
	latency time.Duration
	// latencyNoSim is the same, ignoring goroutines that have an internal/wasim frame (the simulator's own
	// server, which runs in this process).
	latencyNoSim time.Duration
	// simAtEnd is the number of library goroutines with a simulator frame at the end of the poll.
	simAtEnd int
	// result is what QuietStop decided on the real retired session, with a 5 s deadline.
	result QuietStopResult
	// windowA records that the recorded abort was called after the Connect returned.
	windowA bool
	// stopTime is the whole stop, from BeginStop (or the retirement call) to its end.
	stopTime time.Duration
}

// libraryCounts splits the library goroutines of a dump into all and those with a simulator frame.
func libraryCounts(dump []byte) (all, sim int) {
	o := quietObserver{allow: quietAllowList}
	for _, block := range strings.Split(string(dump), "\n\n") {
		block = strings.TrimLeft(block, "\n")
		if block == "" {
			continue
		}
		if library, allowed := o.classify([]byte(block)); library && !allowed {
			all++
			if strings.Contains(block, "internal/wasim") {
				sim++
			}
		}
	}
	return all, sim
}

// pollLatency polls the real goroutine dump until stop is closed, starting at t0 (set by the retirement trace).
func pollLatency(t0 *atomic.Int64, stop <-chan struct{}) (latency, latencyNoSim time.Duration, simAtEnd int) {
	latency, latencyNoSim = -1, -1
	buf := make([]byte, 8<<20)
	for {
		select {
		case <-stop:
			return latency, latencyNoSim, simAtEnd
		default:
		}
		start := t0.Load()
		if start != 0 {
			n := runtime.Stack(buf, true)
			all, sim := libraryCounts(buf[:n])
			elapsed := time.Duration(time.Now().UnixNano() - start)
			simAtEnd = sim
			if all == 0 && latency < 0 {
				latency = elapsed
			}
			if all-sim == 0 && latencyNoSim < 0 {
				latencyNoSim = elapsed
			}
		}
		time.Sleep(time.Millisecond)
	}
}

// measureSavedRun measures one orderly stop of a connected run that started from a saved identity (the shape of
// stdio and serve). traffic lets the run stay connected a little longer.
func measureSavedRun(t *testing.T, hold time.Duration) measureSample {
	t.Helper()
	dir := waDataDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Save an identity the way a pairing does, then stop cleanly.
	st, err := store.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var first capture
	o := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: bundledProvider(ctx), Sessions: first.factory()})
	if err := o.Start(ctx); err != nil {
		t.Fatal(err)
	}
	armAndSave(t, first.handle.(*Session))
	if err := o.stopForTest(ctx); err != nil {
		t.Fatal(err)
	}
	st.Close(ctx)

	st, err = store.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	srv := wasim.Start(t)
	var t0 atomic.Int64
	owner := NewOwner(OwnerConfig{
		Log:      slog.New(slog.DiscardHandler),
		Store:    st,
		Versions: bundledProvider(ctx),
		Sessions: func(cfg SessionConfig) (SessionHandle, error) {
			cfg.PreLoginHTTP = srv.Client()
			cfg.WebsocketHTTP = srv.Client()
			return OpenSession(cfg)
		},
		ShellStartupBudget: true,
		trace: func(step string) {
			if step == "lib_cancelled" {
				t0.Store(time.Now().UnixNano())
			}
		},
	})
	if err := owner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := srv.Accept(ctx)
	if err != nil {
		t.Fatalf("no connection: %v", err)
	}
	if err := conn.SendSuccess(ctx, types.NewJID(testOwnPhone, types.HiddenUserServer)); err != nil {
		t.Fatal(err)
	}
	for !owner.Snapshot().Connected {
		select {
		case <-ctx.Done():
			t.Fatal("never connected")
		case <-time.After(2 * time.Millisecond):
		}
	}
	time.Sleep(hold)
	// The measurement is vacuous unless the dump shows the connected client's goroutines before the stop.
	buf := make([]byte, 8<<20)
	if all, _ := libraryCounts(buf[:runtime.Stack(buf, true)]); all == 0 {
		t.Fatal("the goroutine dump shows no client-library goroutine while connected; the observation would prove nothing")
	}

	stopPoll := make(chan struct{})
	type polled struct {
		latency, noSim time.Duration
		sim            int
	}
	pollDone := make(chan polled, 1)
	go func() {
		l, n, s := pollLatency(&t0, stopPoll)
		pollDone <- polled{l, n, s}
	}()
	owner.mu.Lock()
	gen := owner.gen
	owner.mu.Unlock()
	began := time.Now()
	owner.BeginStop()
	<-owner.Done()
	stopTime := time.Since(began)
	// Let the poller see the settled state before it is stopped.
	time.Sleep(150 * time.Millisecond)
	close(stopPoll)
	p := <-pollDone
	res := gen.handle.(*Session).QuietStop(ctx, 5*time.Second)
	return measureSample{latency: p.latency, latencyNoSim: p.noSim, simAtEnd: p.sim, result: res, stopTime: stopTime, windowA: abortLate(owner, gen)}
}

func abortLate(o *connectionOwner, gen *genState) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return gen != nil && gen.abortLate
}

// measurePairRun measures the retirement of the pairing generation after the 515 that follows PairSuccess,
// through the production retirement path.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return -1
	}
	i := int(float64(len(sorted)-1) * p)
	return sorted[i]
}

func reportKind(t *testing.T, kind string, samples []measureSample) {
	t.Helper()
	var withSim, noSim []time.Duration
	missing, missingNoSim := 0, 0
	windowA := 0
	reasons := map[QuietReason]int{}
	var stops []time.Duration
	simRuns := 0
	for _, s := range samples {
		if s.latency >= 0 {
			withSim = append(withSim, s.latency)
		} else {
			missing++
		}
		if s.latencyNoSim >= 0 {
			noSim = append(noSim, s.latencyNoSim)
		} else {
			missingNoSim++
		}
		if s.windowA {
			windowA++
		}
		if s.simAtEnd > 0 {
			simRuns++
		}
		reasons[s.result.Reason]++
		stops = append(stops, s.stopTime)
	}
	slices.Sort(withSim)
	slices.Sort(noSim)
	slices.Sort(stops)
	var b strings.Builder
	fmt.Fprintf(&b, "\n== %s: %d stops ==\n", kind, len(samples))
	fmt.Fprintf(&b, "time to first poll with no library goroutine (p50 / p95 / max), runs never quiet in 5 s\n")
	fmt.Fprintf(&b, "  including the simulator's goroutines: %v / %v / %v, never quiet %d\n", percentile(withSim, .5), percentile(withSim, .95), percentile(withSim, 1), missing)
	fmt.Fprintf(&b, "  excluding goroutines with an internal/wasim frame: %v / %v / %v, never quiet %d\n", percentile(noSim, .5), percentile(noSim, .95), percentile(noSim, 1), missingNoSim)
	fmt.Fprintf(&b, "runs with simulator-server goroutines visible at the end: %d of %d\n", simRuns, len(samples))
	fmt.Fprintf(&b, "stop duration (p50 / p95 / max): %v / %v / %v\n", percentile(stops, .5), percentile(stops, .95), percentile(stops, 1))
	fmt.Fprintf(&b, "stops that landed in window (a): %d of %d\n", windowA, len(samples))
	fmt.Fprintf(&b, "QuietStop(5s) decisions:")
	for _, reason := range []QuietReason{QuietPassed, QuietWorkRunning, QuietNetworkRefused, QuietStorageRefused, QuietCallRefused, QuietEventDropped, QuietStopBoundExceeded, QuietObservationIncomplete, QuietObservationError, QuietNotChecked} {
		if reasons[reason] > 0 {
			fmt.Fprintf(&b, " %s=%d", reason, reasons[reason])
		}
	}
	fmt.Fprintf(&b, "\npass rate under a candidate deadline (the first quiet poll plus the %v gap must fit):\n", quietObservationGap)
	fmt.Fprintf(&b, "  deadline   with simulator goroutines   without them\n")
	for _, d := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second} {
		fmt.Fprintf(&b, "  %-9v  %5.1f%%                      %5.1f%%\n", d, passRate(withSim, len(samples), d), passRate(noSim, len(samples), d))
	}
	t.Log(b.String())
}

func passRate(latencies []time.Duration, total int, deadline time.Duration) float64 {
	pass := 0
	for _, l := range latencies {
		if l+quietObservationGap <= deadline {
			pass++
		}
	}
	return 100 * float64(pass) / float64(total)
}

func TestQuietStopMeasurement(t *testing.T) {
	if os.Getenv("WA_MEASURE") != "1" {
		t.Skip("set WA_MEASURE=1 to run the quiet-stop measurement")
	}
	runs := 50
	if v, err := strconv.Atoi(os.Getenv("WA_MEASURE_RUNS")); err == nil && v > 0 {
		runs = v
	}
	kinds := []struct {
		name string
		one  func(*testing.T, int) measureSample
	}{
		{"stdio (saved identity, connected, short hold)", func(t *testing.T, _ int) measureSample { return measureSavedRun(t, 20*time.Millisecond) }},
		{"serve (saved identity, connected, longer hold)", func(t *testing.T, _ int) measureSample { return measureSavedRun(t, 400*time.Millisecond) }},
	}
	for _, kind := range kinds {
		var samples []measureSample
		for i := 0; i < runs; i++ {
			t.Run(fmt.Sprintf("%s/%d", strings.Fields(kind.name)[0], i), func(t *testing.T) {
				samples = append(samples, kind.one(t, i))
			})
		}
		reportKind(t, kind.name, samples)
	}
}

// The pass-rate recorder runs the production owner end to end and counts what its own checks decided: the
// pairing generation's check before the fresh connection (pair), and the stop check (pair, stdio, serve). Set
// WA_MEASURE=1; WA_MEASURE_RUNS defaults to 50.
//
//	CGO_ENABLED=0 go test ./internal/wa -run TestQuietStopPassRate -count=1 -v -timeout 60m
type decision struct {
	passed bool
	reason string
}

func ownerDecision(o *connectionOwner) decision {
	h := o.Health()
	return decision{passed: h.QuietPassed && h.QuietReason == "", reason: h.QuietReason}
}

func recordPairRun(t *testing.T) (pairing, stop decision, marker string) {
	t.Helper()
	r := newOwnerRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn := r.pairUntilConfirmed(ctx, true)
	if err := conn.SendStreamError(ctx, "515"); err != nil {
		t.Fatal(err)
	}
	conn2, err := r.srv.Accept(ctx)
	if err != nil {
		// The pairing generation's check failed, so no fresh connection was made.
		return ownerDecision(r.owner), decision{}, ""
	}
	if err := conn2.SendSuccess(ctx, r.phone.LID); err != nil {
		t.Fatal(err)
	}
	r.waitConnected(ctx)
	pairing = ownerDecision(r.owner)
	r.owner.BeginStop()
	<-r.owner.Done()
	stop = ownerDecision(r.owner)
	state, _, _ := r.st.RunGuard(ctx)
	return pairing, stop, state
}

func recordSavedRun(t *testing.T, hold time.Duration) (stop decision, marker string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	o, _, _, st, _, _ := startSavedRunWith(t, ctx, nil)
	time.Sleep(hold)
	o.BeginStop()
	<-o.Done()
	state, _, _ := st.RunGuard(ctx)
	return ownerDecision(o), state
}

func TestQuietStopPassRate(t *testing.T) {
	if os.Getenv("WA_MEASURE") != "1" {
		t.Skip("set WA_MEASURE=1 to run the pass-rate recorder")
	}
	runs := 50
	if v, err := strconv.Atoi(os.Getenv("WA_MEASURE_RUNS")); err == nil && v > 0 {
		runs = v
	}
	type tally struct {
		n, pass int
		reasons map[string]int
		unclean int
	}
	count := func(tl *tally, d decision, marker string) {
		tl.n++
		if d.passed {
			tl.pass++
		} else {
			tl.reasons[d.reason]++
		}
		if d.passed && marker != "clean" {
			tl.unclean++
		}
	}
	pairing, pairStop := &tally{reasons: map[string]int{}}, &tally{reasons: map[string]int{}}
	stdio, serve := &tally{reasons: map[string]int{}}, &tally{reasons: map[string]int{}}
	for i := 0; i < runs; i++ {
		t.Run(fmt.Sprintf("pair/%d", i), func(t *testing.T) {
			p, s, m := recordPairRun(t)
			count(pairing, p, "clean")
			if s != (decision{}) || m != "" {
				count(pairStop, s, m)
			}
		})
	}
	for i := 0; i < runs; i++ {
		t.Run(fmt.Sprintf("stdio/%d", i), func(t *testing.T) { s, m := recordSavedRun(t, 20*time.Millisecond); count(stdio, s, m) })
	}
	for i := 0; i < runs; i++ {
		t.Run(fmt.Sprintf("serve/%d", i), func(t *testing.T) { s, m := recordSavedRun(t, 400*time.Millisecond); count(serve, s, m) })
	}
	for _, row := range []struct {
		name string
		tl   *tally
	}{{"pair: pairing generation's check", pairing}, {"pair: stop check", pairStop}, {"stdio: stop check", stdio}, {"serve: stop check", serve}} {
		rate := 100 * float64(row.tl.pass) / float64(max(row.tl.n, 1))
		t.Logf("PASSRATE %-34s runs=%d passed=%d rate=%.1f%% failures=%v passed-but-marker-not-clean=%d",
			row.name, row.tl.n, row.tl.pass, rate, row.tl.reasons, row.tl.unclean)
	}
}
