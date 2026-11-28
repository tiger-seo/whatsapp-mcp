package media

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock moves only when a test advances it. Timers fire during Advance.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	ch      chan time.Time
	stopped bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		t.ch <- c.now
		t.stopped = true
	}
	c.timers = append(c.timers, t)
	return t.ch, func() {
		c.mu.Lock()
		t.stopped = true
		c.mu.Unlock()
	}
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, t := range c.timers {
		if !t.stopped && !t.at.After(c.now) {
			t.stopped = true
			t.ch <- c.now
		}
	}
}

// send takes the slot, records a hand-over and releases.
func send(t *testing.T, l *Limits) {
	t.Helper()
	slot, limit, busy, _ := l.Acquire()
	if limit != nil || busy {
		t.Fatalf("send refused: limit %+v, busy %v", limit, busy)
	}
	l.RecordSend()
	slot.Release()
}

func refusal(t *testing.T, l *Limits) RateLimit {
	t.Helper()
	slot, limit, _, _ := l.Acquire()
	if limit == nil {
		if slot != nil {
			slot.Release()
		}
		t.Fatal("send allowed, want a rate limit")
	}
	return *limit
}

func TestSendRateMinuteWindow(t *testing.T) {
	clock := newFakeClock()
	l := NewLimits(clock)
	for range sendsPerMinute {
		send(t, l)
		clock.Advance(time.Second)
	}
	// The first send is 10 s old, so it leaves the window in 50 s.
	got := refusal(t, l)
	if got.Window != LimitMinute || got.RetryAfter != 50*time.Second || got.Lookup {
		t.Fatalf("refusal = %+v", got)
	}
	clock.Advance(50*time.Second - time.Nanosecond)
	if got := refusal(t, l); got.RetryAfter != time.Nanosecond {
		t.Fatalf("refusal just before the window moves = %+v", got)
	}
	clock.Advance(time.Nanosecond)
	send(t, l)
	// Refused calls were not counted: one slot freed, one used.
	if got := refusal(t, l); got.RetryAfter != time.Second {
		t.Fatalf("refusal after the next send = %+v", got)
	}
}

func TestSendRateDayWindow(t *testing.T) {
	clock := newFakeClock()
	l := NewLimits(clock)
	start := clock.Now()
	for range sendsPerDay {
		send(t, l)
		clock.Advance(time.Minute)
	}
	got := refusal(t, l)
	want := start.Add(24 * time.Hour).Sub(clock.Now())
	if got.Window != LimitDay || got.RetryAfter != want {
		t.Fatalf("refusal = %+v, want the day window and %v", got, want)
	}
	clock.Advance(want)
	send(t, l)
	if n := len(l.sends.stamps); n > sendsPerDay {
		t.Errorf("kept %d timestamps, want at most %d", n, sendsPerDay)
	}
}

func TestSendRateReportsTheBindingWindow(t *testing.T) {
	clock := newFakeClock()
	l := NewLimits(clock)
	// 190 old sends, then 10 at once: both windows are full, and the day window stays full longer.
	for range sendsPerDay - sendsPerMinute {
		l.RecordSend()
	}
	clock.Advance(time.Hour)
	for range sendsPerMinute {
		l.RecordSend()
	}
	if got := refusal(t, l); got.Window != LimitDay || got.RetryAfter != 23*time.Hour {
		t.Fatalf("refusal = %+v", got)
	}
	// Near the end of the day window the minute window binds.
	clock = newFakeClock()
	l = NewLimits(clock)
	for range sendsPerDay - sendsPerMinute {
		l.RecordSend()
	}
	clock.Advance(24*time.Hour - 10*time.Second)
	for range sendsPerMinute {
		l.RecordSend()
	}
	if got := refusal(t, l); got.Window != LimitMinute || got.RetryAfter != time.Minute {
		t.Fatalf("refusal = %+v", got)
	}
}

func TestSendRateCapsTimestamps(t *testing.T) {
	l := NewLimits(newFakeClock())
	for range sendsPerDay + 50 {
		l.RecordSend()
	}
	if n := len(l.sends.stamps); n != sendsPerDay {
		t.Errorf("kept %d timestamps, want %d", n, sendsPerDay)
	}
}

func TestSendSlot(t *testing.T) {
	l := NewLimits(newFakeClock())
	slot, limit, busy, _ := l.Acquire()
	if slot == nil || limit != nil || busy {
		t.Fatalf("first acquire: %v %v %v", slot, limit, busy)
	}
	if s, _, busy, stuck := l.Acquire(); s != nil || !busy || stuck {
		t.Fatalf("second acquire: slot %v, busy %v, stuck %v", s, busy, stuck)
	}
	slot.Abandon()
	if _, _, busy, stuck := l.Acquire(); !busy || !stuck {
		t.Fatalf("after abandon: busy %v, stuck %v", busy, stuck)
	}
	slot.Release()
	next, _, busy, stuck := l.Acquire()
	if next == nil || busy || stuck {
		t.Fatalf("after release: slot %v, busy %v, stuck %v", next, busy, stuck)
	}
	// A second release of the old slot does not free the new holder's.
	slot.Release()
	if s, _, busy, _ := l.Acquire(); s != nil || !busy {
		t.Fatal("a stale release freed the slot")
	}
	next.Release()
}

func TestSendLimitLookupWindow(t *testing.T) {
	clock := newFakeClock()
	l := NewLimits(clock)
	for range lookupsPerMinute {
		if got := l.startLookup(); got != nil {
			t.Fatalf("lookup refused: %+v", got)
		}
	}
	got := l.startLookup()
	if got == nil || !got.Lookup || got.Window != LimitMinute || got.RetryAfter != time.Minute {
		t.Fatalf("refusal = %+v", got)
	}
	// The lookup window does not consume the send window.
	send(t, l)
	for sent := lookupsPerMinute; sent < lookupsPerDay; sent++ {
		if sent%lookupsPerMinute == 0 {
			clock.Advance(time.Minute)
		}
		if got := l.startLookup(); got != nil {
			t.Fatalf("lookup %d refused: %+v", sent, got)
		}
	}
	clock.Advance(time.Minute)
	if got := l.startLookup(); got == nil || got.Window != LimitDay {
		t.Fatalf("refusal after 100 = %+v", got)
	}
	if n := len(l.lookups.stamps); n > lookupsPerDay {
		t.Errorf("kept %d lookup timestamps", n)
	}
}

func TestSendLimitLookupCache(t *testing.T) {
	clock := newFakeClock()
	l := NewLimits(clock)
	if _, ok := l.cachedLookup("1"); ok {
		t.Fatal("empty cache hit")
	}
	l.storeLookup("1", true, "100@lid")
	l.storeLookup("2", false, "")
	if e, ok := l.cachedLookup("1"); !ok || !e.registered || e.lid != "100@lid" {
		t.Errorf("registered entry = %+v, %v", e, ok)
	}
	if e, ok := l.cachedLookup("2"); !ok || e.registered {
		t.Errorf("unregistered entry = %+v, %v", e, ok)
	}
	if n := len(l.lookups.stamps); n != 0 {
		t.Errorf("cache hits used %d of the window", n)
	}
	clock.Advance(lookupCacheAge - time.Nanosecond)
	if _, ok := l.cachedLookup("1"); !ok {
		t.Error("entry expired early")
	}
	clock.Advance(time.Nanosecond)
	if _, ok := l.cachedLookup("1"); ok {
		t.Error("entry used after 24 hours")
	}
}

func TestSendLimitLookupCacheEvictsOldest(t *testing.T) {
	clock := newFakeClock()
	l := NewLimits(clock)
	for i := range lookupCacheSize {
		l.storeLookup(fmt.Sprint(i), true, "")
	}
	// A refreshed result is the newest again, so the next one in age is dropped first.
	l.storeLookup("0", true, "")
	l.storeLookup("new", true, "")
	if len(l.cache) != lookupCacheSize || len(l.order) != lookupCacheSize {
		t.Fatalf("cache holds %d (%d ordered)", len(l.cache), len(l.order))
	}
	for phone, want := range map[string]bool{"0": true, "1": false, "2": true, "new": true} {
		if _, ok := l.cachedLookup(phone); ok != want {
			t.Errorf("cached(%s) = %v, want %v", phone, ok, want)
		}
	}
}
