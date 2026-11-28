package media

import (
	"slices"
	"sync"
	"time"
)

// The fixed limits. There is no setting for any of them.
const (
	sendsPerMinute   = 10
	sendsPerDay      = 200
	lookupsPerMinute = 10
	lookupsPerDay    = 100
	lookupCacheAge   = 24 * time.Hour
	lookupCacheSize  = 1000
)

// LimitWindow names the rolling window that refused a call.
type LimitWindow string

const (
	LimitMinute LimitWindow = "minute"
	LimitDay    LimitWindow = "day"
)

// RateLimit says which limit refused a call and when another one is possible.
type RateLimit struct {
	// Lookup is set for the registration lookup limit; otherwise the send limit applied.
	Lookup     bool
	Window     LimitWindow
	RetryAfter time.Duration
}

// window is a rolling per-minute and per-24-hour count held as timestamps, at most perDay of them.
type window struct {
	perMinute, perDay int
	stamps            []time.Time // oldest first
}

func (w *window) expire(now time.Time) {
	i := 0
	for i < len(w.stamps) && now.Sub(w.stamps[i]) >= 24*time.Hour {
		i++
	}
	w.stamps = slices.Delete(w.stamps, 0, i)
}

// check returns the limit that refuses another event now. When both windows are full, the one that stays full
// longer is reported, so the hint is the time at which a retry can succeed.
func (w *window) check(now time.Time) (RateLimit, bool) {
	w.expire(now)
	var out RateLimit
	refused := false
	if len(w.stamps) >= w.perDay {
		out = RateLimit{Window: LimitDay, RetryAfter: w.stamps[len(w.stamps)-w.perDay].Add(24 * time.Hour).Sub(now)}
		refused = true
	}
	if n := len(w.stamps); n >= w.perMinute {
		if wait := w.stamps[n-w.perMinute].Add(time.Minute).Sub(now); wait > 0 && wait > out.RetryAfter {
			out = RateLimit{Window: LimitMinute, RetryAfter: wait}
			refused = true
		}
	}
	return out, refused
}

func (w *window) record(now time.Time) {
	w.expire(now)
	w.stamps = append(w.stamps, now)
	if over := len(w.stamps) - w.perDay; over > 0 {
		w.stamps = slices.Delete(w.stamps, 0, over)
	}
}

// lookupEntry is one remembered registration lookup: registered with its LID, or unregistered.
type lookupEntry struct {
	registered bool
	lid        string
	at         time.Time
}

// Limits is the process-wide send state: the one send slot, the send and lookup windows and the lookup cache. One
// value serves every session of a process; it is kept in memory and starts empty again after a restart.
type Limits struct {
	clock Clock

	mu      sync.Mutex
	held    bool
	stuck   bool
	sends   window
	lookups window
	cache   map[string]lookupEntry
	order   []string // cached numbers, oldest result first
}

// NewLimits builds the state on a clock.
func NewLimits(clock Clock) *Limits {
	return &Limits{
		clock:   clock,
		sends:   window{perMinute: sendsPerMinute, perDay: sendsPerDay},
		lookups: window{perMinute: lookupsPerMinute, perDay: lookupsPerDay},
		cache:   map[string]lookupEntry{},
	}
}

// Slot is the held send slot. Release frees it once; later calls do nothing.
type Slot struct {
	l    *Limits
	once sync.Once
}

// Acquire checks the send window and takes the send slot. A refused call is not counted. A non-nil limit means
// the window refused; busy means another send holds the slot, and stuck that it holds it only because an earlier
// call's abandoned file read has not ended.
func (l *Limits) Acquire() (slot *Slot, limit *RateLimit, busy, stuck bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, refused := l.sends.check(l.clock.Now()); refused {
		return nil, &r, false, false
	}
	if l.held {
		return nil, nil, true, l.stuck
	}
	l.held, l.stuck = true, false
	return &Slot{l: l}, nil, false, false
}

// Abandon marks the slot as held only by a file stage whose caller has returned.
func (s *Slot) Abandon() {
	s.l.mu.Lock()
	defer s.l.mu.Unlock()
	s.l.stuck = true
}

// Release frees the slot.
func (s *Slot) Release() {
	s.once.Do(func() {
		s.l.mu.Lock()
		defer s.l.mu.Unlock()
		s.l.held, s.l.stuck = false, false
	})
}

// RecordSend counts a send at the moment it is handed to WhatsApp, whatever its outcome.
func (l *Limits) RecordSend() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sends.record(l.clock.Now())
}

// cachedLookup returns the remembered result for a phone number, if it is younger than a day.
func (l *Limits) cachedLookup(phone string) (lookupEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.cache[phone]
	if !ok || l.clock.Now().Sub(e.at) >= lookupCacheAge {
		return lookupEntry{}, false
	}
	return e, true
}

// startLookup counts a lookup that is about to be sent, or returns the limit that refuses it.
func (l *Limits) startLookup() *RateLimit {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if r, refused := l.lookups.check(now); refused {
		r.Lookup = true
		return &r
	}
	l.lookups.record(now)
	return nil
}

// storeLookup remembers a successful lookup. The oldest result is dropped beyond the cache size.
func (l *Limits) storeLookup(phone string, registered bool, lid string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.cache[phone]; ok {
		l.order = slices.DeleteFunc(l.order, func(p string) bool { return p == phone })
	}
	l.cache[phone] = lookupEntry{registered: registered, lid: lid, at: l.clock.Now()}
	l.order = append(l.order, phone)
	for len(l.order) > lookupCacheSize {
		delete(l.cache, l.order[0])
		l.order = slices.Delete(l.order, 0, 1)
	}
}
