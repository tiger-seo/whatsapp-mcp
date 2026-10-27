package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Set at build time with -ldflags "-X github.com/tiger-seo/whatsapp-mcp-next/internal/buildinfo.Version=…".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

const unknown = "unknown"

// Info is the build information printed by the version command.
type Info struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
	Target    string
}

// Get returns the build information. A commit or date not injected at build time falls back to the VCS
// stamp of the Go toolchain, then to "unknown". The module version from debug.BuildInfo is ignored on
// purpose: it is a pseudo-version, and a local build must report Version.
func Get() Info {
	var settings []debug.BuildSetting
	if bi, ok := debug.ReadBuildInfo(); ok {
		settings = bi.Settings
	}
	commit, date := fromSettings(settings)
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Target:    runtime.GOOS + "/" + runtime.GOARCH,
	}
	if info.Commit == "" {
		info.Commit = commit
	}
	if info.Date == "" {
		info.Date = date
	}
	return info
}

func fromSettings(settings []debug.BuildSetting) (commit, date string) {
	commit, date = unknown, unknown
	modified := false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			if s.Value != "" {
				commit = s.Value
			}
		case "vcs.time":
			if s.Value != "" {
				date = s.Value
			}
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if modified && commit != unknown {
		commit += "-dirty"
	}
	return commit, date
}
