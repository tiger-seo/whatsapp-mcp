package wa

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

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

// Stops with ingest busy. Each case stops the production owner while the ingest component has work
// in flight and records what the stop's own quiet check decided and the marker it left. Set WA_MEASURE=1;
// WA_MEASURE_RUNS defaults to 50.
//
//	WA_MEASURE=1 CGO_ENABLED=0 go test ./internal/wa -run TestQuietStopWithIngestWorkers -count=1 -v -timeout 60m

// busyStore delays the store calls ingest makes, so work is in flight when the stop begins.
type busyStore struct {
	ingestStore
	lidDelay, writeDelay, mergeDelay time.Duration
	mapLID, mapPhone                 string
	lidCalls                         atomic.Int64
	mergeEntered                     chan struct{}
	mergeOnce                        sync.Once
}

func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

func (b *busyStore) PhoneForLID(ctx context.Context, lid string) (string, bool, error) {
	b.lidCalls.Add(1)
	sleepCtx(ctx, b.lidDelay)
	if lid == b.mapLID {
		return b.mapPhone, true, nil
	}
	return b.ingestStore.PhoneForLID(ctx, lid)
}

func (b *busyStore) StoreMessage(ctx context.Context, m store.Message) error {
	sleepCtx(ctx, b.writeDelay)
	return b.ingestStore.StoreMessage(ctx, m)
}

func (b *busyStore) StoreMessages(ctx context.Context, m []store.Message) error {
	sleepCtx(ctx, b.writeDelay)
	return b.ingestStore.StoreMessages(ctx, m)
}

func (b *busyStore) MergeLID(ctx context.Context, lid, phone string) error {
	if lid != b.mapLID {
		return b.ingestStore.MergeLID(ctx, lid, phone)
	}
	b.mergeOnce.Do(func() { close(b.mergeEntered) })
	sleepCtx(ctx, b.mergeDelay)
	return nil
}

// busyOutcome is one stop.
type busyOutcome struct {
	passed  bool
	reason  string
	marker  string
	stopFor time.Duration
	// inFlight is false when the work the case wanted to catch had already finished: the run proves less.
	inFlight bool
}

func finishBusyStop(t *testing.T, r *ownerRig, inFlight bool) busyOutcome {
	t.Helper()
	began := time.Now()
	r.owner.BeginStop()
	select {
	case <-r.owner.Done():
	case <-time.After(60 * time.Second):
		t.Fatal("the owner did not finish stopping")
	}
	d := ownerDecision(r.owner)
	return busyOutcome{passed: d.passed, reason: d.reason, marker: r.runGuardState(), stopFor: time.Since(began), inFlight: inFlight}
}

func jitter(i int, upTo time.Duration) time.Duration {
	return time.Duration(rand.New(rand.NewPCG(uint64(i)+1, 7)).Int64N(int64(upTo) + 1))
}

// busyHistoryRun stops while the simulator is delivering history chunks: the first download is held until the
// stop begins on a third of the runs, and on the others it is released and the stop comes after a random delay.
func busyHistoryRun(t *testing.T, i int) busyOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	r := newIngestSimRig(t, ctx, nil)
	phone := r.historyPhone(t)
	hit := make(chan struct{}, 64)
	release := make(chan struct{})
	holdFirst := i%3 == 0
	var once sync.Once
	r.srv.HoldMedia(func(string) {
		hit <- struct{}{}
		if holdFirst {
			once.Do(func() { <-release })
		}
	})
	defer r.srv.HoldMedia(nil)
	for c := 0; c < 6; c++ {
		r.sendHistory(ctx, phone, fmt.Sprintf("HIST-%d", c), simHistory(fmt.Sprintf("c%d", c), 10, 100, time.Now().Add(-time.Hour)), uint32(c+1))
	}
	select {
	case <-hit:
	case <-ctx.Done():
		t.Fatal("no history download started")
	}
	if !holdFirst {
		time.Sleep(jitter(i, 120*time.Millisecond))
	}
	busy := r.session().ingest.Counters().Stored < 6000
	go func() { time.Sleep(50 * time.Millisecond); close(release) }()
	return finishBusyStop(t, r.ownerRig, busy)
}

// busyLookupMergeRun stops while a group-name lookup (a real library call to the simulator, held) and a run-time
// merge (held in the store) are in progress.
func busyLookupMergeRun(t *testing.T, i int) busyOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	bs := &busyStore{mapLID: "700000000009@lid", mapPhone: "491519999999@s.whatsapp.net", mergeDelay: 400 * time.Millisecond,
		mergeEntered: make(chan struct{})}
	r := newIngestSimRig(t, ctx, func(s ingestStore) ingestStore { bs.ingestStore = s; return bs })
	groupAsked := make(chan struct{}, 4)
	releaseGroup := make(chan struct{})
	r.conn.SetIQHandler(func(n *waBinary.Node) *waBinary.Node {
		if n.AttrGetter().OptionalString("xmlns") == "w:g2" {
			groupAsked <- struct{}{}
			<-releaseGroup
		}
		return nil
	})
	defer close(releaseGroup)
	in := r.session().ingest
	if !in.HandleMessage(nameEvent("BG1", groupJID(), peerJID(), "", 0)) {
		t.Fatal("the group message was refused")
	}
	in.NoteMapping(bs.mapLID, "", bs.mapPhone)
	inFlight := true
	for _, ch := range []chan struct{}{groupAsked, bs.mergeEntered} {
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			inFlight = false
		}
	}
	time.Sleep(jitter(i, 30*time.Millisecond))
	return finishBusyStop(t, r.ownerRig, inFlight)
}

// busyLiveRun stops while a live message sent by a peer is inside the handler: its sender's LID is looked up twice
// slowly and the write is slow.
func busyLiveRun(t *testing.T, i int) busyOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	bs := &busyStore{lidDelay: 250 * time.Millisecond, writeDelay: 400 * time.Millisecond, mapLID: "none", mergeEntered: make(chan struct{})}
	r := newIngestSimRig(t, ctx, func(s ingestStore) ingestStore { bs.ingestStore = s; return bs })
	lidPeer, err := wasim.NewPeer(types.NewJID("700000000001", types.HiddenUserServer))
	if err != nil {
		t.Fatal(err)
	}
	n, err := lidPeer.MessageNode(ctx, r.keys, "LIVE-1", time.Now(), &waE2E.Message{Conversation: proto.String("slow")})
	if err != nil {
		t.Fatal(err)
	}
	r.send(ctx, n)
	inFlight := true
	deadline := time.Now().Add(10 * time.Second)
	for bs.lidCalls.Load() == 0 {
		if time.Now().After(deadline) {
			inFlight = false
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(jitter(i, 400*time.Millisecond))
	return finishBusyStop(t, r.ownerRig, inFlight)
}

// busyPairRun is the pair-shaped stop: after the 515 and the fresh connection, while the first history
// notification is being downloaded.
func busyPairRun(t *testing.T, i int) busyOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	r, sessions := newRecordedRig(t, nil)
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
	rig := &ingestSimRig{ownerRig: r, sessions: sessions, conn: conn2}
	rig.keys = wasim.DeviceKeysOf(rig.session().dev)
	hit := make(chan struct{}, 8)
	release := make(chan struct{})
	var once sync.Once
	r.srv.HoldMedia(func(string) {
		hit <- struct{}{}
		once.Do(func() { <-release })
	})
	defer r.srv.HoldMedia(nil)
	rig.sendHistory(ctx, rig.historyPhone(t), "HIST-P", simHistory("p", 10, 100, time.Now().Add(-time.Hour)), 1)
	inFlight := true
	select {
	case <-hit:
	case <-time.After(10 * time.Second):
		inFlight = false
	}
	time.Sleep(jitter(i, 20*time.Millisecond))
	go func() { time.Sleep(50 * time.Millisecond); close(release) }()
	return finishBusyStop(t, r, inFlight)
}

func TestQuietStopWithIngestWorkers(t *testing.T) {
	if os.Getenv("WA_MEASURE") != "1" {
		t.Skip("set WA_MEASURE=1 to run the stops-with-ingest-busy measurement")
	}
	runs := 50
	if v, err := strconv.Atoi(os.Getenv("WA_MEASURE_RUNS")); err == nil && v > 0 {
		runs = v
	}
	cases := []struct {
		name string
		run  func(*testing.T, int) busyOutcome
	}{
		{"history chunks", busyHistoryRun},
		{"group lookup and merge", busyLookupMergeRun},
		{"live message in the handler", busyLiveRun},
		{"pair-shaped, first history download", busyPairRun},
	}
	for _, c := range cases {
		var outs []busyOutcome
		for i := 0; i < runs; i++ {
			t.Run(fmt.Sprintf("%s/%d", strings.Fields(c.name)[0], i), func(t *testing.T) { outs = append(outs, c.run(t, i)) })
		}
		pass, caught, unsafe := 0, 0, 0
		reasons := map[string]int{}
		var stops []time.Duration
		for _, o := range outs {
			if o.passed {
				pass++
			} else {
				reasons[o.reason]++
			}
			if o.inFlight {
				caught++
			}
			if o.marker != "clean" {
				unsafe++
			}
			stops = append(stops, o.stopFor)
		}
		slices.Sort(stops)
		t.Logf("BUSYSTOP %-38s runs=%d passed=%d rate=%.1f%% work-in-flight-at-stop=%d marker-not-clean=%d stop_bound_exceeded=%d failures=%v stop p50/max=%v/%v",
			c.name, len(outs), pass, 100*float64(pass)/float64(max(len(outs), 1)), caught, unsafe,
			reasons[string(QuietStopBoundExceeded)], reasons, percentile(stops, .5), percentile(stops, 1))
	}
}

// Handler latency under history. On the simulator an initial history of 20 chunks arrives while two
// other accounts send a burst of live messages: one by phone number, one by LID with a phone SenderAlt (so the
// library repeats mapping upserts), with view-once placeholders mixed in. The measurement records how long the
// handler waited for the ingest lock, how many times the library reported receive_overload and what was stored.
//
//	WA_MEASURE=1 CGO_ENABLED=0 go test ./internal/wa -run TestIngestHandlerLatency -count=1 -v -timeout 60m
const (
	latencyLive         = 2000
	latencyPlaceholders = 100
	latencyChunks       = 20
	latencyChunkChats   = 5
	latencyChunkPerChat = 50
)

type latencyRun struct {
	waits      []time.Duration
	overloads  int
	stored     uint64
	expected   uint64
	unavail    uint64
	unavailExp uint64
	completed  bool
	elapsed    time.Duration
}

func handlerLatencyRun(t *testing.T) latencyRun {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	r := newIngestSimRig(t, ctx, nil)
	in := r.session().ingest
	var mu sync.Mutex
	var out latencyRun
	in.liveWaited = func(d time.Duration) { mu.Lock(); out.waits = append(out.waits, d); mu.Unlock() }

	pn, err := wasim.NewPeer(peerJID())
	if err != nil {
		t.Fatal(err)
	}
	lidJID := types.NewJID("700000000001", types.HiddenUserServer)
	lid, err := wasim.NewPeer(lidJID)
	if err != nil {
		t.Fatal(err)
	}
	altPhone := types.NewJID("491517000001", types.DefaultUserServer)
	phone := r.historyPhone(t)
	chunkEvery := latencyLive / latencyChunks
	placeholderEvery := latencyLive / latencyPlaceholders
	started := time.Now()
	chunk := 0
	for i := 0; i < latencyLive; i++ {
		id := fmt.Sprintf("L%d", i)
		switch {
		case i%placeholderEvery == placeholderEvery-1:
			r.send(ctx, pn.UnavailableNode(id, time.Now(), "view_once"))
			out.unavailExp++
			continue
		case i%2 == 1:
			n, err := lid.MessageNode(ctx, r.keys, id, time.Now(), &waE2E.Message{Conversation: proto.String("lid " + id)})
			if err != nil {
				t.Fatal(err)
			}
			n.Attrs["sender_pn"] = altPhone
			r.send(ctx, n)
		default:
			r.sendMessage2(ctx, pn, id)
		}
		out.expected++
		if i%chunkEvery == 0 && chunk < latencyChunks {
			r.sendHistory(ctx, phone, fmt.Sprintf("HC%d", chunk), simHistory(fmt.Sprintf("hc%d", chunk), latencyChunkChats, latencyChunkPerChat, time.Now().Add(-24*time.Hour)), uint32(chunk+1))
			out.expected += latencyChunkChats * latencyChunkPerChat
			chunk++
		}
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		c := in.Counters()
		if c.Stored >= out.expected && c.Unavailable >= out.unavailExp {
			out.completed = true
			break
		}
		if r.owner.Health().ReceiveOverload != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	out.elapsed = time.Since(started)
	c := in.Counters()
	out.stored, out.unavail = c.Stored, c.Unavailable
	for _, l := range r.log.lines {
		if strings.Contains(l, "event=receive_overload") {
			out.overloads++
		}
	}
	mu.Lock()
	defer mu.Unlock()
	return out
}

// sendMessage2 sends a text from a given peer.
func (r *ingestSimRig) sendMessage2(ctx context.Context, p *wasim.Peer, id string) {
	r.t.Helper()
	n, err := p.MessageNode(ctx, r.keys, id, time.Now(), &waE2E.Message{Conversation: proto.String("pn " + id)})
	if err != nil {
		r.t.Fatal(err)
	}
	r.send(ctx, n)
}

func TestIngestHandlerLatency(t *testing.T) {
	if os.Getenv("WA_MEASURE") != "1" {
		t.Skip("set WA_MEASURE=1 to run the handler-latency measurement")
	}
	runs := 20
	if v, err := strconv.Atoi(os.Getenv("WA_MEASURE_RUNS")); err == nil && v > 0 {
		runs = v
	}
	var all []time.Duration
	var maxes []time.Duration
	overloads, incomplete := 0, 0
	var stored, expected uint64
	var elapsed []time.Duration
	for i := 0; i < runs; i++ {
		t.Run(fmt.Sprintf("run/%d", i), func(t *testing.T) {
			o := handlerLatencyRun(t)
			all = append(all, o.waits...)
			if len(o.waits) > 0 {
				w := slices.Clone(o.waits)
				slices.Sort(w)
				maxes = append(maxes, w[len(w)-1])
			}
			overloads += o.overloads
			if !o.completed {
				incomplete++
			}
			stored += o.stored
			expected += o.expected
			elapsed = append(elapsed, o.elapsed)
		})
	}
	slices.Sort(all)
	slices.Sort(maxes)
	slices.Sort(elapsed)
	t.Logf("LATENCY runs=%d live_waits=%d wait p50=%v p99=%v max=%v per-run max p50=%v max=%v receive_overload=%d runs_incomplete=%d stored=%d of %d run time p50/max=%v/%v",
		runs, len(all), percentile(all, .5), percentile(all, .99), percentile(all, 1), percentile(maxes, .5), percentile(maxes, 1),
		overloads, incomplete, stored, expected, percentile(elapsed, .5), percentile(elapsed, 1))
}
