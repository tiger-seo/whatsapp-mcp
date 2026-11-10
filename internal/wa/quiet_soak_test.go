package wa

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
)

// The soak run keeps a serve-shaped connection open on the simulator for hours while synthetic traffic arrives at
// a steady rate with a burst every ten minutes, samples resource use, then stops in order. It is a long-run
// monitor for the receive exception, not part of the normal suite: set WA_SOAK=1.
//
//	WA_SOAK=1 WA_SOAK_OUT=D:\tmp\soak.csv CGO_ENABLED=0 go test ./internal/wa -run TestQuietStopSoak -count=1 -v -timeout 5h
//
// WA_SOAK_DURATION (default 4h), WA_SOAK_SAMPLE (default 60s), WA_SOAK_RATE (events per second, default 10) and
// WA_SOAK_BURST_EVERY (default 10m) shorten it for a smoke run.

// soakGoroutineAllowance is how many goroutines above the pre-connect count the process may keep after the stop,
// once the simulator's own goroutines are taken out. The store and the test framework hold a few.
const soakGoroutineAllowance = 6

type soakSample struct {
	at         time.Duration
	heapInuse  uint64
	goroutines int
	db, wal    int64
	rss        int64
	sent       int64
}

func soakEnvDuration(name string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func soakFileSizes(dir string) (db, wal int64) {
	files, _ := filepath.Glob(filepath.Join(dir, "whatsapp.db*"))
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		if strings.HasSuffix(f, "-wal") {
			wal += info.Size()
		} else {
			db += info.Size()
		}
	}
	return db, wal
}

// slope is the least-squares growth per hour of ys over xs.
func slope(xs []time.Duration, ys []float64) float64 {
	n := float64(len(xs))
	if n < 2 {
		return 0
	}
	var sx, sy, sxx, sxy float64
	for i := range xs {
		x := xs[i].Hours()
		sx += x
		sy += ys[i]
		sxx += x * x
		sxy += x * ys[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / den
}

func TestQuietStopSoak(t *testing.T) {
	if os.Getenv("WA_SOAK") != "1" {
		t.Skip("set WA_SOAK=1 to run the soak test")
	}
	duration := soakEnvDuration("WA_SOAK_DURATION", 4*time.Hour)
	every := soakEnvDuration("WA_SOAK_SAMPLE", time.Minute)
	burstEvery := soakEnvDuration("WA_SOAK_BURST_EVERY", 10*time.Minute)
	rate := 10
	if v, err := strconv.Atoi(os.Getenv("WA_SOAK_RATE")); err == nil && v > 0 {
		rate = v
	}
	var out *os.File
	if p := os.Getenv("WA_SOAK_OUT"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		out = f
		fmt.Fprintln(out, "elapsed_s,heap_inuse_bytes,goroutines,db_bytes,wal_bytes,rss_bytes,events_sent")
	}

	baseline := runtime.NumGoroutine()
	setup, cancelSetup := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelSetup()
	dir := waDataDir(t)
	st, err := store.Open(setup, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var first capture
	o1 := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: bundledProvider(setup), Sessions: first.factory()})
	if err := o1.Start(setup); err != nil {
		t.Fatal(err)
	}
	armAndSave(t, first.handle.(*Session))
	if err := o1.stopForTest(setup); err != nil {
		t.Fatal(err)
	}
	st.Close(setup)
	st, err = store.Open(setup, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())

	srv := wasim.Start(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	o := NewOwner(OwnerConfig{
		Log: logger, Store: st, Versions: bundledProvider(setup), ShellStartupBudget: true,
		Sessions: func(cfg SessionConfig) (SessionHandle, error) {
			cfg.PreLoginHTTP = srv.Client()
			cfg.WebsocketHTTP = srv.Client()
			return OpenSession(cfg)
		},
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	conn, err := srv.Accept(setup)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SendSuccess(setup, types.NewJID(testOwnPhone, types.HiddenUserServer)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return o.Snapshot().Connected })

	// Drain what the client sends so the simulator's read loop never blocks.
	traffic, stopTraffic := context.WithCancel(context.Background())
	defer stopTraffic()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			if _, err := conn.Recv(traffic); err != nil {
				return
			}
		}
	}()

	var sent, seq atomic.Int64
	peer := types.NewJID("490000000002", types.DefaultUserServer)
	node := func() waBinary.Node {
		n := seq.Add(1)
		id := fmt.Sprintf("SOAK%d", n)
		now := strconv.FormatInt(time.Now().Unix(), 10)
		switch n % 4 {
		case 0:
			return waBinary.Node{Tag: "receipt", Attrs: waBinary.Attrs{"from": peer, "id": id, "t": now, "type": "read"}}
		case 1:
			return waBinary.Node{Tag: "presence", Attrs: waBinary.Attrs{"from": peer, "type": "available"}}
		case 2:
			return waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{"from": peer, "id": id, "t": now, "type": "text"}}
		default:
			return waBinary.Node{Tag: "chatstate", Attrs: waBinary.Attrs{"from": peer}, Content: []waBinary.Node{{Tag: "composing"}}}
		}
	}
	send := func(n waBinary.Node) {
		if err := conn.Send(traffic, n); err == nil {
			sent.Add(1)
		}
	}
	senderDone := make(chan struct{})
	go func() {
		defer close(senderDone)
		tick := time.NewTicker(time.Second / time.Duration(rate))
		defer tick.Stop()
		burst := time.NewTicker(burstEvery)
		defer burst.Stop()
		for {
			select {
			case <-traffic.Done():
				return
			case <-tick.C:
				send(node())
			case <-burst.C:
				for i := 0; i < 2000 && traffic.Err() == nil; i++ {
					send(node())
				}
				// An app-state notification: the client fetches the collection (the simulator answers empty).
				send(waBinary.Node{Tag: "notification", Attrs: waBinary.Attrs{
					"from": types.ServerJID, "type": "server_sync", "id": "SOAKSYNC" + strconv.FormatInt(seq.Add(1), 10),
					"t": strconv.FormatInt(time.Now().Unix(), 10),
				}, Content: []waBinary.Node{{Tag: "collection", Attrs: waBinary.Attrs{"name": "regular_low", "version": "1"}}}})
			}
		}
	}()

	var samples []soakSample
	started := time.Now()
	take := func() {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		db, wal := soakFileSizes(dir)
		s := soakSample{at: time.Since(started), heapInuse: m.HeapInuse, goroutines: runtime.NumGoroutine(), db: db, wal: wal, rss: processRSS(), sent: sent.Load()}
		samples = append(samples, s)
		line := fmt.Sprintf("%.0f,%d,%d,%d,%d,%d,%d", s.at.Seconds(), s.heapInuse, s.goroutines, s.db, s.wal, s.rss, s.sent)
		if out != nil {
			fmt.Fprintln(out, line)
			_ = out.Sync()
		}
		t.Logf("SOAK %s", line)
	}
	take()
	deadline := time.After(duration)
	ticker := time.NewTicker(every)
	defer ticker.Stop()
run:
	for {
		select {
		case <-ticker.C:
			take()
			if !o.Snapshot().Connected {
				stopTraffic()
				t.Fatalf("the connection was lost after %s", time.Since(started).Round(time.Second))
			}
		case <-deadline:
			break run
		}
	}
	take()

	stopTraffic()
	<-drained
	<-senderDone
	o.BeginStop()
	<-o.Done()
	d := ownerDecision(o)
	// The setup context may have expired hours ago; the marker is read with a fresh one.
	readCtx, cancelRead := context.WithTimeout(context.Background(), time.Minute)
	defer cancelRead()
	state, _, _ := st.RunGuard(readCtx)

	// The simulator's own server goroutines stay until the test ends; they are not the program's. Goroutines
	// that are winding down get up to ten seconds; what remains is grouped by its first function for the report.
	buf := make([]byte, 8<<20)
	var simulator, end int
	var remaining map[string]int
	for settle := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		dump := string(buf[:runtime.Stack(buf, true)])
		simulator, remaining = 0, map[string]int{}
		for _, block := range strings.Split(dump, "\n\n") {
			if strings.Contains(block, "internal/wasim") {
				simulator++
			}
			if lines := strings.Split(strings.TrimSpace(block), "\n"); len(lines) > 1 {
				remaining[strings.TrimSpace(lines[1])]++
			}
		}
		end = runtime.NumGoroutine()
		if end-simulator <= baseline+soakGoroutineAllowance || time.Now().After(settle) {
			break
		}
	}
	if end-simulator > baseline+soakGoroutineAllowance {
		for fn, n := range remaining {
			t.Logf("SOAK-GOROUTINES %3d %s", n, fn)
		}
	}

	xs := make([]time.Duration, 0, len(samples))
	for _, s := range samples {
		xs = append(xs, s.at)
	}
	rows := []struct {
		name string
		f    func(soakSample) float64
	}{
		{"heap_inuse_bytes", func(s soakSample) float64 { return float64(s.heapInuse) }},
		{"goroutines", func(s soakSample) float64 { return float64(s.goroutines) }},
		{"db_bytes", func(s soakSample) float64 { return float64(s.db) }},
		{"wal_bytes", func(s soakSample) float64 { return float64(s.wal) }},
		{"rss_bytes", func(s soakSample) float64 { return float64(s.rss) }},
	}
	last := samples[len(samples)-1]
	t.Logf("SOAK-SUMMARY duration=%s samples=%d events_sent=%d", last.at.Round(time.Second), len(samples), last.sent)
	half := len(samples) / 2
	for _, r := range rows {
		ys := make([]float64, 0, len(samples))
		peak := 0.0
		for _, s := range samples {
			ys = append(ys, r.f(s))
			peak = max(peak, r.f(s))
		}
		t.Logf("SOAK-SUMMARY %-17s start=%.0f peak=%.0f end=%.0f slope_all/h=%.0f slope_second_half/h=%.0f",
			r.name, ys[0], peak, ys[len(ys)-1], slope(xs, ys), slope(xs[half:], ys[half:]))
	}
	t.Logf("SOAK-SUMMARY stop passed=%v reason=%q marker=%s goroutines baseline=%d end=%d simulator_goroutines=%d",
		d.passed, d.reason, state, baseline, end, simulator)

	if !d.passed {
		t.Errorf("the stop failed its check: %q", d.reason)
	}
	if state != "clean" {
		t.Errorf("the marker is %q, want clean", state)
	}
	if end-simulator > baseline+soakGoroutineAllowance {
		t.Errorf("goroutines did not return to the baseline: baseline %d, end %d (simulator %d)", baseline, end, simulator)
	}
}
