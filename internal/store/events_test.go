package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSafePath(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain/path.db", "plain/path.db"},
		{"a\x1b[31mb", `a\u001b[31mb`},
		{"a\nb", `a\u000ab`},
		{"a\xffb", `a\xffb`},
		{`C:\data\whatsapp.db`, `C:\data\whatsapp.db`}, // Windows paths stay readable
	} {
		if got := safePath(c.in); got != c.want {
			t.Errorf("safePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := safePath(strings.Repeat("x", 2000))
	if len(long) > 520 {
		t.Errorf("safePath of a long path has %d bytes, want at most 512 plus an ellipsis", len(long))
	}
}

func TestOpID(t *testing.T) {
	a, b := newOpID(), newOpID()
	if len(a) != 16 || a == b {
		t.Errorf("op IDs %q %q: want 16 hex characters, distinct", a, b)
	}
}

func TestEventFields(t *testing.T) {
	logger, recs := newTestLogger()
	ev := newEvents(logger)
	ev.open("op1", "/data/whatsapp.db")
	ev.refused("op1", &SchemaError{Schema: "messages", Observed: 7, Supported: 5, sentinel: ErrSchemaTooNew}, "/data/whatsapp.db", "messages", 7, 5)
	ev.migrate("op1", 1, 2, "add-x", errors.New("SECRET"))
	for _, r := range recs.all() {
		for _, k := range []string{"event", "op_id", "outcome", "code"} {
			if r.attrs[k] == "" {
				t.Errorf("%s: missing %s", r.msg, k)
			}
		}
		for _, v := range r.attrs {
			if strings.Contains(v, "SECRET") {
				t.Errorf("%s leaks error text: %v", r.msg, r.attrs)
			}
		}
	}
	if got := recs.events("store.refused")[0].attrs["code"]; got != "schema_too_new" {
		t.Errorf("refused code = %q", got)
	}
	if got := recs.events("store.migrate")[0].attrs["outcome"]; got != "failed" {
		t.Errorf("migrate outcome = %q", got)
	}
}

func TestLimitEventIsRateLimited(t *testing.T) {
	logger, recs := newTestLogger()
	ev := newEvents(logger)
	now := time.Unix(1000, 0)
	ev.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		ev.limit("op", "busy", ErrBusy)
	}
	if got := len(recs.events("store.limit")); got != 1 {
		t.Fatalf("events = %d, want 1 within 10 s", got)
	}
	now = now.Add(11 * time.Second)
	ev.limit("op", "busy", ErrBusy)
	evs := recs.events("store.limit")
	if len(evs) != 2 || evs[1].attrs["suppressed"] != "4" {
		t.Errorf("after the interval: %d events, suppressed %q; want 2 and 4", len(evs), evs[len(evs)-1].attrs["suppressed"])
	}
	ev.limit("op", "memory", ErrResourceLimit)
	if got := len(recs.events("store.limit")); got != 3 {
		t.Errorf("a different limit shares the rate limit: %d events", got)
	}
}
