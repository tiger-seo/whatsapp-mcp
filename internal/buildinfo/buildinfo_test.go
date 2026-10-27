package buildinfo

import (
	"runtime"
	"runtime/debug"
	"testing"
)

func TestGetDefaults(t *testing.T) {
	info := Get()
	if info.Version != "dev" {
		t.Errorf("Version = %q, want dev", info.Version)
	}
	if info.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; info.Target != want {
		t.Errorf("Target = %q, want %q", info.Target, want)
	}
	if info.Commit == "" || info.Date == "" {
		t.Errorf("Commit = %q, Date = %q; want a value or unknown", info.Commit, info.Date)
	}
}

func TestGetInjected(t *testing.T) {
	oldCommit, oldDate := Commit, Date
	t.Cleanup(func() { Commit, Date = oldCommit, oldDate })
	Commit, Date = "4f2c1d0", "2026-10-27T09:14:03Z"
	info := Get()
	if info.Commit != Commit || info.Date != Date {
		t.Errorf("Get() = %+v; want the injected commit and date", info)
	}
}

func TestFromSettings(t *testing.T) {
	tests := []struct {
		name         string
		settings     []debug.BuildSetting
		commit, date string
	}{
		{"dirty", []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "true"}, {Key: "vcs.time", Value: "2026-10-27T09:14:03Z"}}, "abc-dirty", "2026-10-27T09:14:03Z"},
		{"clean", []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "false"}}, "abc", "unknown"},
		{"none", nil, "unknown", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commit, date := fromSettings(tt.settings)
			if commit != tt.commit || date != tt.date {
				t.Errorf("fromSettings = %q, %q; want %q, %q", commit, date, tt.commit, tt.date)
			}
		})
	}
}
