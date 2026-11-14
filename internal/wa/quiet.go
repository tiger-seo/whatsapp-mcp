package wa

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// QuietReason names why a quiet-stop check passed or failed. It is the only text the check lets out.
type QuietReason string

const (
	QuietPassed                QuietReason = "passed"
	QuietWorkRunning           QuietReason = "work_running"
	QuietNetworkRefused        QuietReason = "network_refused"
	QuietStorageRefused        QuietReason = "storage_refused"
	QuietCallRefused           QuietReason = "call_refused"
	QuietEventDropped          QuietReason = "event_dropped"
	QuietStopBoundExceeded     QuietReason = "stop_bound_exceeded"
	QuietObservationIncomplete QuietReason = "observation_incomplete"
	QuietObservationError      QuietReason = "observation_error"
	QuietNotChecked            QuietReason = "not_checked"
)

// QuietStopResult is the value one generation's check produces. It holds counts and a reason, never dump text,
// function names, argument values, keys or account identifiers.
type QuietStopResult struct {
	Passed            bool
	Reason            QuietReason
	LibraryGoroutines int
	NetworkRefused    uint64
	StorageRefused    uint64
	CallsRefused      uint64
	EventsDropped     uint64
	Duration          time.Duration
	// DroppedMessages counts message events left unacknowledged during or after retirement. They may be missing
	// from the store and are reported; they never make Passed false.
	DroppedMessages uint64
}

const (
	// quietObservationGap is the least time between the two clean observations.
	quietObservationGap = 100 * time.Millisecond
	// stackBufferMax is the largest goroutine dump the check accepts; a dump that fills it is incomplete.
	stackBufferMax = 8 << 20
	// stackBufferStart is the first buffer size; it doubles up to stackBufferMax.
	stackBufferStart = 1 << 20
	// quietStopDeadline is the approved observation time for an ordinary stop.
	quietStopDeadline = time.Second
	// postPairQuietDeadline is the approved observation time for the pairing generation before the fresh connection.
	postPairQuietDeadline = 2 * time.Second
	// quietPollInterval is the wait between observations while library work is still visible.
	quietPollInterval = 10 * time.Millisecond
)

// quietAllowEntry names harmless client-library work that may still be running after both blocks. Entries are
// exact function names. The list starts empty: an entry needs evidence and a review against the pinned version.
type quietAllowEntry struct {
	// Function is one exact function name as runtime.Stack prints it, without arguments.
	Function string
	// ReviewedFor is the go.mau.fi/whatsmeow version the entry was reviewed against; it must equal go.mod.
	ReviewedFor string
	// Evidence says why the work is harmless once the network and storage are blocked.
	Evidence string
}

var quietAllowList = []quietAllowEntry{}

// libraryPackages are the packages whose goroutines count as client-library work.
var libraryPackages = []string{"go.mau.fi/whatsmeow", "go.mau.fi/libsignal", "github.com/coder/websocket"}

// quietObserver is the injectable side of the observation: the dump source, the clock and the wait.
type quietObserver struct {
	// dump fills buf with a goroutine dump of all goroutines and returns the bytes written.
	dump func(buf []byte) (int, error)
	// self is the goroutine id of the caller, which is not library work of the generation.
	self string
	now  func() time.Time
	wait func(ctx context.Context, d time.Duration) error
	// allow is the allow-list in force.
	allow []quietAllowEntry
}

func productionObserver() quietObserver {
	return quietObserver{
		dump:  func(buf []byte) (int, error) { return runtime.Stack(buf, true), nil },
		self:  currentGoroutineID(),
		now:   time.Now,
		wait:  sleepContext,
		allow: quietAllowList,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// currentGoroutineID returns the caller's goroutine number as text, or "" when it cannot be read.
func currentGoroutineID() string {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	return goroutineHeaderID(buf[:n])
}

func goroutineHeaderID(b []byte) string {
	rest, ok := bytes.CutPrefix(b, []byte("goroutine "))
	if !ok {
		return ""
	}
	end := bytes.IndexByte(rest, ' ')
	if end <= 0 {
		return ""
	}
	if _, err := strconv.ParseUint(string(rest[:end]), 10, 64); err != nil {
		return ""
	}
	return string(rest[:end])
}

// observeOnce takes one bounded dump and returns how many library goroutines it shows. The reason is "" on a
// complete observation.
func (o quietObserver) observeOnce(buf []byte) (count int, grown []byte, reason QuietReason) {
	for {
		n, err := o.dump(buf)
		if err != nil || n < 0 || n > len(buf) {
			return 0, buf, QuietObservationError
		}
		if n < len(buf) {
			return o.countLibrary(buf[:n]), buf, ""
		}
		if len(buf) >= stackBufferMax {
			return 0, buf, QuietObservationIncomplete
		}
		next := len(buf) * 2
		if next > stackBufferMax {
			next = stackBufferMax
		}
		buf = make([]byte, next)
	}
}

// countLibrary counts the goroutines of the dump that run client-library code, after removing the caller and
// allow-listed work. The dump text stays in this function.
func (o quietObserver) countLibrary(dump []byte) int {
	count := 0
	for _, block := range bytes.Split(dump, []byte("\n\n")) {
		block = bytes.TrimLeft(block, "\n")
		if len(block) == 0 {
			continue
		}
		if id := goroutineHeaderID(block); id != "" && id == o.self {
			continue
		}
		library, allowed := o.classify(block)
		if library && !allowed {
			count++
		}
	}
	return count
}

// classify reports whether a goroutine block has a frame or creator in a client-library package, and whether
// one of its library functions is on the allow-list.
func (o quietObserver) classify(block []byte) (library, allowed bool) {
	for _, raw := range strings.Split(string(block), "\n") {
		if raw == "" || raw[0] == '\t' || raw[0] == ' ' || strings.HasPrefix(raw, "goroutine ") || strings.HasPrefix(raw, "...") {
			continue
		}
		name := raw
		if rest, ok := strings.CutPrefix(raw, "created by "); ok {
			name = rest
			if i := strings.Index(name, " in goroutine "); i >= 0 {
				name = name[:i]
			}
		} else {
			name = stripArguments(name)
		}
		if !inLibraryPackage(name) {
			continue
		}
		library = true
		for _, e := range o.allow {
			if e.Function == name {
				allowed = true
			}
		}
	}
	return library, allowed
}

// stripArguments removes the trailing "(args)" group that runtime.Stack prints after a function name.
func stripArguments(line string) string {
	if !strings.HasSuffix(line, ")") {
		return line
	}
	depth := 0
	for i := len(line) - 1; i >= 0; i-- {
		switch line[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return line[:i]
			}
		}
	}
	return line
}

// inLibraryPackage matches the package path followed by "." or "/", never a bare string prefix.
func inLibraryPackage(name string) bool {
	for _, pkg := range libraryPackages {
		if rest, ok := strings.CutPrefix(name, pkg); ok && rest != "" && (rest[0] == '.' || rest[0] == '/') {
			return true
		}
	}
	return false
}

// observeQuiet polls the goroutine dump until two consecutive observations at least quietObservationGap apart
// both show zero client-library goroutines, or until the deadline (counted from the start of observation). It
// returns "" when quiet was proven, and otherwise the reason it was not, with the last count.
func (o quietObserver) observeQuiet(ctx context.Context, deadline time.Duration) (count int, reason QuietReason) {
	start := o.now()
	limit := start.Add(deadline)
	buf := make([]byte, stackBufferStart)
	var firstZero time.Time
	haveZero := false
	for {
		var r QuietReason
		count, buf, r = o.observeOnce(buf)
		if r != "" {
			return count, r
		}
		now := o.now()
		if count == 0 {
			if !haveZero {
				haveZero, firstZero = true, now
			} else if now.Sub(firstZero) >= quietObservationGap {
				return 0, ""
			}
		} else {
			haveZero = false
		}
		remaining := limit.Sub(now)
		if remaining <= 0 {
			return count, QuietWorkRunning
		}
		pause := quietPollInterval
		if haveZero {
			pause = quietObservationGap - now.Sub(firstZero)
		}
		if pause > remaining {
			pause = remaining
		}
		if pause < 0 {
			pause = 0
		}
		if err := o.wait(ctx, pause); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				// The stop bound passed; the decision reports that, not a broken observation.
				return count, QuietWorkRunning
			}
			return count, QuietObservationError
		}
	}
}

// quietInputs are the facts the decision combines.
type quietInputs struct {
	Observation   QuietReason // "", observation_incomplete, observation_error or work_running
	Goroutines    int
	Network       uint64
	Storage       uint64
	Calls         uint64
	Events        uint64
	BoundExceeded bool
	Duration      time.Duration
}

// decideQuiet builds the result. Every failure or doubt gives Passed false, and Reason names the first cause in
// the fixed order while the counts report all of them.
func decideQuiet(in quietInputs) QuietStopResult {
	res := QuietStopResult{
		LibraryGoroutines: in.Goroutines,
		NetworkRefused:    in.Network,
		StorageRefused:    in.Storage,
		CallsRefused:      in.Calls,
		EventsDropped:     in.Events,
		Duration:          in.Duration,
	}
	switch {
	case in.Observation == QuietObservationIncomplete:
		res.Reason = QuietObservationIncomplete
	case in.Observation == QuietObservationError:
		res.Reason = QuietObservationError
	case in.BoundExceeded:
		res.Reason = QuietStopBoundExceeded
	case in.Storage > 0:
		res.Reason = QuietStorageRefused
	case in.Network > 0:
		res.Reason = QuietNetworkRefused
	case in.Calls > 0:
		res.Reason = QuietCallRefused
	case in.Events > 0:
		res.Reason = QuietEventDropped
	case in.Observation == QuietWorkRunning || in.Goroutines > 0:
		res.Reason = QuietWorkRunning
	default:
		res.Reason = QuietPassed
		res.Passed = true
	}
	return res
}
