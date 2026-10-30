package store

import (
	"strings"
	"testing"
)

func TestWALogDropsArguments(t *testing.T) {
	logger, recs := newTestLogger()
	l := newWALog(logger)
	l.Infof("pairing %s with key %x", "SECRET-JID-7f3a", []byte("SECRET-KEY"))
	l.Sub("Database").Sub("Upgrade").Errorf("failed: %v", "SECRET-ERR")
	l.Debugf("debug")
	l.Warnf("warn")
	all := recs.all()
	if len(all) != 4 {
		t.Fatalf("records = %d, want 4", len(all))
	}
	for _, r := range all {
		if strings.Contains(r.msg, "SECRET") {
			t.Errorf("message leaks an argument: %q", r.msg)
		}
		for k, v := range r.attrs {
			if strings.Contains(v, "SECRET") {
				t.Errorf("attribute %s leaks an argument: %q", k, v)
			}
		}
		if r.attrs["event"] != "whatsmeow.log" {
			t.Errorf("event = %q", r.attrs["event"])
		}
	}
	if all[0].msg != "pairing %s with key %x" || all[0].level.String() != "INFO" {
		t.Errorf("first record = %q at %v", all[0].msg, all[0].level)
	}
	if got := all[1].attrs["module"]; got != "Database/Upgrade" {
		t.Errorf("module = %q, want Database/Upgrade", got)
	}
}
