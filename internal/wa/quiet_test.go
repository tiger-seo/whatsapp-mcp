package wa

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeClock advances only when the observer waits, so no test sleeps.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) wait(_ context.Context, d time.Duration) error {
	c.t = c.t.Add(d)
	return nil
}

// dumpOf builds a goroutine dump from blocks.
func dumpOf(blocks ...string) string { return strings.Join(blocks, "\n\n") + "\n" }

func goroutine(id int, frames ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "goroutine %d [select]:\n", id)
	for _, f := range frames {
		if strings.HasPrefix(f, "created by ") {
			b.WriteString(f + "\n\t/src/file.go:1 +0x1\n")
			continue
		}
		b.WriteString(f + "(0xc000012345, {0x1, 0x2})\n\t/src/file.go:10 +0x20\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// scripted returns a dump source that serves the given dumps in turn, repeating the last one.
func scripted(dumps ...string) func([]byte) (int, error) {
	i := 0
	return func(buf []byte) (int, error) {
		d := dumps[min(i, len(dumps)-1)]
		i++
		return copy(buf, d), nil
	}
}

func testObserver(clock *fakeClock, dump func([]byte) (int, error)) quietObserver {
	return quietObserver{dump: dump, self: "1", now: clock.now, wait: clock.wait}
}

func observe(o quietObserver, deadline time.Duration) (int, QuietReason) {
	return o.observeQuiet(context.Background(), deadline)
}

const (
	whatsmeowFrame = "go.mau.fi/whatsmeow.(*Client).keepAliveLoop"
	libsignalFrame = "go.mau.fi/libsignal/session.(*Cipher).Encrypt"
	websocketFrame = "github.com/coder/websocket.(*Conn).readLoop"
	utilFrame      = "go.mau.fi/util/exsync.(*Event).Wait"
)

func TestQuietPassesAfterTwoCleanObservationsApart(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	var observed []time.Time
	o := testObserver(clock, func(buf []byte) (int, error) {
		observed = append(observed, clock.t)
		return copy(buf, dumpOf(goroutine(1, "main.main"), goroutine(2, "runtime.gopark"))), nil
	})
	count, reason := observe(o, 5*time.Second)
	if reason != "" || count != 0 {
		t.Fatalf("observeQuiet = %d, %q; want 0, pass", count, reason)
	}
	if len(observed) < 2 || observed[len(observed)-1].Sub(observed[0]) < quietObservationGap {
		t.Fatalf("observations %v are not %v apart", observed, quietObservationGap)
	}
}

func TestQuietCleanThenDirtyFails(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	clean := dumpOf(goroutine(2, "main.main"))
	dirty := dumpOf(goroutine(2, "main.main"), goroutine(3, whatsmeowFrame))
	o := testObserver(clock, scripted(clean, dirty))
	count, reason := observe(o, 300*time.Millisecond)
	if reason != QuietWorkRunning || count != 1 {
		t.Fatalf("observeQuiet = %d, %q; want 1, work_running", count, reason)
	}
}

func TestQuietLibraryGoroutineAtDeadlineFails(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	o := testObserver(clock, scripted(dumpOf(goroutine(3, whatsmeowFrame))))
	count, reason := observe(o, time.Second)
	if reason != QuietWorkRunning || count != 1 {
		t.Fatalf("observeQuiet = %d, %q", count, reason)
	}
	if clock.t.Sub(time.Unix(1000, 0)) > time.Second+quietPollInterval {
		t.Fatalf("the observation ran past its deadline: %v", clock.t.Sub(time.Unix(1000, 0)))
	}
}

func TestQuietCountsEveryLibraryPackageAndCreatedBy(t *testing.T) {
	cases := map[string]string{
		"whatsmeow":    goroutine(3, whatsmeowFrame),
		"libsignal":    goroutine(3, libsignalFrame),
		"websocket":    goroutine(3, websocketFrame),
		"subpackage":   goroutine(3, "go.mau.fi/whatsmeow/appstate.(*Processor).Do"),
		"created by":   goroutine(3, "runtime.gopark", "created by go.mau.fi/whatsmeow.(*Client).Connect in goroutine 7"),
		"deep in call": goroutine(3, "runtime.gopark", "sync.(*Mutex).Lock", whatsmeowFrame),
	}
	for name, block := range cases {
		o := testObserver(&fakeClock{t: time.Unix(1, 0)}, scripted(dumpOf(goroutine(1, "main.main"), block)))
		if n, _, _ := o.observeOnce(make([]byte, 1<<20)); n != 1 {
			t.Errorf("%s: counted %d, want 1", name, n)
		}
	}
}

func TestQuietIgnoresNonLibraryAndLookalikePackages(t *testing.T) {
	cases := map[string]string{
		"util only":       goroutine(3, utilFrame, "created by go.mau.fi/util/exsync.NewEvent in goroutine 1"),
		"prefix lookup":   goroutine(3, "go.mau.fi/whatsmeowx.(*Client).run"),
		"other websocket": goroutine(3, "github.com/coder/websocketx.run"),
		"stdlib":          goroutine(3, "net/http.(*persistConn).readLoop"),
	}
	for name, block := range cases {
		o := testObserver(&fakeClock{t: time.Unix(1, 0)}, scripted(dumpOf(block)))
		if n, _, _ := o.observeOnce(make([]byte, 1<<20)); n != 0 {
			t.Errorf("%s: counted %d, want 0", name, n)
		}
	}
}

func TestQuietExcludesTheCheckingGoroutine(t *testing.T) {
	dump := dumpOf(goroutine(42, "go.mau.fi/whatsmeow.(*Client).Disconnect"), goroutine(2, "main.main"))
	o := testObserver(&fakeClock{t: time.Unix(1, 0)}, scripted(dump))
	o.self = "42"
	if n, _, _ := o.observeOnce(make([]byte, 1<<20)); n != 0 {
		t.Fatalf("the caller was counted: %d", n)
	}
	o.self = "43"
	if n, _, _ := o.observeOnce(make([]byte, 1<<20)); n != 1 {
		t.Fatalf("another goroutine was not counted: %d", n)
	}
}

func TestQuietAllowListIsExact(t *testing.T) {
	dump := dumpOf(goroutine(3, whatsmeowFrame))
	other := dumpOf(goroutine(3, "go.mau.fi/whatsmeow.(*Client).otherLoop"))
	o := testObserver(&fakeClock{t: time.Unix(1, 0)}, scripted(dump))
	o.allow = []quietAllowEntry{{Function: whatsmeowFrame, ReviewedFor: "v", Evidence: "e"}}
	if n, _, _ := o.observeOnce(make([]byte, 1<<20)); n != 0 {
		t.Fatalf("an allow-listed function was counted: %d", n)
	}
	o.dump = scripted(other)
	if n, _, _ := o.observeOnce(make([]byte, 1<<20)); n != 1 {
		t.Fatalf("a different function of the same package was ignored: %d", n)
	}
}

func TestQuietFullBufferIsIncomplete(t *testing.T) {
	var asked []int
	o := testObserver(&fakeClock{t: time.Unix(1, 0)}, func(buf []byte) (int, error) {
		asked = append(asked, len(buf))
		return len(buf), nil // always fills the buffer
	})
	count, reason := observe(o, time.Second)
	if reason != QuietObservationIncomplete {
		t.Fatalf("reason = %q, want observation_incomplete", reason)
	}
	if asked[0] != stackBufferStart || asked[len(asked)-1] != stackBufferMax {
		t.Fatalf("buffer sizes %v, want from %d up to %d", asked, stackBufferStart, stackBufferMax)
	}
	_ = count
}

func TestQuietBufferGrowsForALargeCompleteDump(t *testing.T) {
	big := dumpOf(goroutine(2, "main.main")) + strings.Repeat("x", 3<<20)
	o := testObserver(&fakeClock{t: time.Unix(1, 0)}, scripted(big))
	count, _, reason := o.observeOnce(make([]byte, stackBufferStart))
	if reason != "" || count != 0 {
		t.Fatalf("observeOnce = %d, %q; want a complete observation", count, reason)
	}
}

func TestQuietDumpErrorFails(t *testing.T) {
	secret := "SECRET-DUMP-ERROR-TEXT"
	o := testObserver(&fakeClock{t: time.Unix(1, 0)}, func([]byte) (int, error) { return 0, errors.New(secret) })
	count, reason := observe(o, time.Second)
	if reason != QuietObservationError {
		t.Fatalf("reason = %q, want observation_error", reason)
	}
	res := decideQuiet(quietInputs{Observation: reason, Goroutines: count})
	if res.Passed || strings.Contains(fmt.Sprintf("%+v %v", res, res.Reason), secret) {
		t.Fatalf("result %+v leaks or passes", res)
	}
}

func TestQuietObservationDeadlineIsRelativeToItsStart(t *testing.T) {
	clock := &fakeClock{t: time.Unix(5000, 0)}
	o := testObserver(clock, scripted(dumpOf(goroutine(3, whatsmeowFrame))))
	start := clock.t
	_, reason := observe(o, 250*time.Millisecond)
	if reason != QuietWorkRunning {
		t.Fatalf("reason = %q", reason)
	}
	if got := clock.t.Sub(start); got < 250*time.Millisecond || got > 250*time.Millisecond+quietPollInterval {
		t.Fatalf("observation lasted %v, want about 250ms", got)
	}
}

func TestQuietContextEndIsAnObservationError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := quietObserver{
		dump: scripted(dumpOf(goroutine(3, whatsmeowFrame))),
		now:  time.Now,
		wait: sleepContext,
	}
	if _, reason := o.observeQuiet(ctx, time.Hour); reason != QuietObservationError {
		t.Fatalf("reason = %q, want observation_error", reason)
	}
}

func TestDecidePrecedence(t *testing.T) {
	all := quietInputs{Observation: QuietObservationIncomplete, Goroutines: 2, Network: 1, Storage: 1, Calls: 1, Events: 1, BoundExceeded: true}
	steps := []struct {
		mutate func(*quietInputs)
		want   QuietReason
	}{
		{func(*quietInputs) {}, QuietObservationIncomplete},
		{func(in *quietInputs) { in.Observation = QuietObservationError }, QuietObservationError},
		{func(in *quietInputs) { in.Observation = QuietWorkRunning }, QuietStopBoundExceeded},
		{func(in *quietInputs) { in.BoundExceeded = false }, QuietStorageRefused},
		{func(in *quietInputs) { in.Storage = 0 }, QuietNetworkRefused},
		{func(in *quietInputs) { in.Network = 0 }, QuietCallRefused},
		{func(in *quietInputs) { in.Calls = 0 }, QuietEventDropped},
		{func(in *quietInputs) { in.Events = 0 }, QuietWorkRunning},
		{func(in *quietInputs) { in.Observation, in.Goroutines = "", 0 }, QuietPassed},
	}
	in := all
	for i, s := range steps {
		s.mutate(&in)
		res := decideQuiet(in)
		if res.Reason != s.want {
			t.Fatalf("step %d: reason = %q, want %q", i, res.Reason, s.want)
		}
		if res.Passed != (s.want == QuietPassed) {
			t.Fatalf("step %d: Passed = %v for %q", i, res.Passed, res.Reason)
		}
	}
	res := decideQuiet(all)
	if res.NetworkRefused != 1 || res.StorageRefused != 1 || res.CallsRefused != 1 || res.EventsDropped != 1 || res.LibraryGoroutines != 2 {
		t.Fatalf("the counts must report every cause: %+v", res)
	}
}

func TestDecideAfterTheStopBoundFailsWhateverTheObservationShows(t *testing.T) {
	res := decideQuiet(quietInputs{BoundExceeded: true})
	if res.Passed || res.Reason != QuietStopBoundExceeded {
		t.Fatalf("result = %+v, want a stop_bound_exceeded failure", res)
	}
}

func TestResultHoldsNoText(t *testing.T) {
	res := decideQuiet(quietInputs{Goroutines: 1})
	if got := fmt.Sprintf("%+v", res); strings.Contains(got, "goroutine") || strings.Contains(got, "whatsmeow") {
		t.Fatalf("result text %q mentions the dump", got)
	}
}

// TestProductionObserverSeesTheLibraryRunning proves the production dump source works end to end: this test
// goroutine is not library work, so a dump of the real process shows none from this package's own frames.
func TestProductionObserverUsesTheRealDump(t *testing.T) {
	o := productionObserver()
	if o.self == "" {
		t.Fatal("the production observer could not read its own goroutine id")
	}
	if _, _, reason := o.observeOnce(make([]byte, stackBufferStart)); reason != "" {
		t.Fatalf("a real dump was %q", reason)
	}
}

func TestQuietAllowListPin(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := pinnedWhatsmeow(mod)
	if err != nil {
		t.Fatal(err)
	}
	if len(quietAllowList) != 0 {
		t.Logf("allow-list has %d entries", len(quietAllowList))
	}
	for _, e := range quietAllowList {
		if e.Function == "" || strings.HasSuffix(e.Function, "*") || strings.HasSuffix(e.Function, "/") || strings.HasSuffix(e.Function, ".") {
			t.Errorf("allow-list entry %q is not one exact function", e.Function)
		}
		if !strings.Contains(e.Function, ".") {
			t.Errorf("allow-list entry %q names a package, not a function", e.Function)
		}
		if e.ReviewedFor != pinned {
			t.Errorf("allow-list entry %q was reviewed for %q but go.mod pins %q", e.Function, e.ReviewedFor, pinned)
		}
		if e.Evidence == "" {
			t.Errorf("allow-list entry %q has no evidence", e.Function)
		}
	}
}

func TestQuietAllowListStartsEmpty(t *testing.T) {
	if len(quietAllowList) != 0 {
		t.Fatalf("the allow-list must start empty, has %d entries", len(quietAllowList))
	}
}

// The allow-list is reviewed for the code that is built: when go.mod replaces whatsmeow, the replacement's
// version, not the required one.
func TestQuietAllowListPinParser(t *testing.T) {
	for _, c := range []struct{ name, mod, want string }{
		{"require only", "module x\n\nrequire (\n\tgo.mau.fi/whatsmeow v0.0.0-20261109152745-f1b798561919\n)\n", "v0.0.0-20261109152745-f1b798561919"},
		{"single-line require", "module x\nrequire go.mau.fi/whatsmeow v0.1.0\n", "v0.1.0"},
		{"replace with version", "module x\n\nreplace go.mau.fi/whatsmeow => github.com/tiger-seo/whatsmeow v0.0.0-20261113124132-bedcf0185245\n\nrequire (\n\tgo.mau.fi/whatsmeow v0.0.0-20261109152745-f1b798561919\n)\n", "v0.0.0-20261113124132-bedcf0185245"},
		{"replace block", "module x\nrequire go.mau.fi/whatsmeow v0.1.0\nreplace (\n\tgo.mau.fi/whatsmeow v0.1.0 => example.com/fork v0.2.0\n)\n", "v0.2.0"},
		{"replace of another module", "module x\nreplace go.mau.fi/util => example.com/util v9.0.0\nrequire go.mau.fi/whatsmeow v0.1.0\n", "v0.1.0"},
		{"local replace", "module x\nreplace go.mau.fi/whatsmeow => ../whatsmeow\nrequire go.mau.fi/whatsmeow v0.1.0\n", "../whatsmeow"},
	} {
		got, err := pinnedWhatsmeow([]byte(c.mod))
		if err != nil || got != c.want {
			t.Errorf("%s: pinnedWhatsmeow = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := pinnedWhatsmeow([]byte("module x\n")); err == nil {
		t.Error("no requirement: want an error")
	}
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pinnedWhatsmeow(mod); err != nil || !strings.HasSuffix(got, "-bedcf0185245") {
		t.Errorf("this repository's go.mod: %q, %v; want the bedcf01 fork version", got, err)
	}
}

// pinnedWhatsmeow returns the version of whatsmeow that is built: the target of a replace directive for
// go.mau.fi/whatsmeow when there is one (its version, or its path for a local directory), else the required
// version.
func pinnedWhatsmeow(mod []byte) (string, error) {
	const module = "go.mau.fi/whatsmeow"
	var required, replaced string
	block := ""
	for _, line := range strings.Split(string(mod), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if block != "" {
			if f[0] == ")" {
				block = ""
				continue
			}
			f = append([]string{block}, f...)
		} else if len(f) == 2 && f[1] == "(" {
			block = f[0]
			continue
		}
		switch {
		case f[0] == "require" && len(f) >= 3 && f[1] == module:
			required = f[2]
		case f[0] == "replace" && len(f) >= 4 && f[1] == module:
			arrow := slices.Index(f, "=>")
			if arrow < 0 || arrow+1 >= len(f) {
				return "", fmt.Errorf("malformed replace of %s", module)
			}
			target := f[arrow+1:]
			replaced = target[0]
			if len(target) >= 2 {
				replaced = target[1]
			}
		}
	}
	if replaced != "" {
		return replaced, nil
	}
	if required == "" {
		return "", fmt.Errorf("go.mod has no %s requirement", module)
	}
	return required, nil
}
