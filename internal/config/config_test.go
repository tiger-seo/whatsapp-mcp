package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

// fixture is a data directory with an optional config.toml, and a fake per-user config directory.
type fixture struct {
	dataDir string
	userDir string
}

func newFixture(t *testing.T, toml string) fixture {
	t.Helper()
	fx := fixture{dataDir: filepath.Join(t.TempDir(), "data"), userDir: t.TempDir()}
	if toml != "" {
		if err := private.MkdirAll(fx.dataDir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fx.configPath(), []byte(toml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return fx
}

// privateDir returns a new, existing data directory that only this user can reach. A bare t.TempDir() may
// inherit wider access, as on Windows when the temporary directory is outside the user's profile.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := private.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (fx fixture) configPath() string { return filepath.Join(fx.dataDir, FileName) }

// load runs Load with --data-dir pointing at the fixture, plus the given flags and environment.
func (fx fixture) load(flags map[string]string, environ ...string) (*Config, []Warning, error) {
	all := map[string]string{"data-dir": fx.dataDir}
	for k, v := range flags {
		all[k] = v
	}
	return Load(Inputs{
		Flags:         all,
		Environ:       environ,
		UserConfigDir: func() (string, error) { return fx.userDir, nil },
	})
}

func mustLoad(t *testing.T, fx fixture, flags map[string]string, environ ...string) (*Config, []Warning) {
	t.Helper()
	cfg, warnings, err := fx.load(flags, environ...)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg, warnings
}

func TestLogLevelPrecedence(t *testing.T) {
	const file = "# comment\nlog_level = \"error\"\n"
	flag := map[string]string{"log-level": "debug"}
	env := "WHATSAPP_MCP_LOG_LEVEL=warn"
	tests := []struct {
		name    string
		toml    string
		flags   map[string]string
		environ []string
		want    slog.Level
		source  Source
		detail  string
	}{
		{"flag>env", "", flag, []string{env}, slog.LevelDebug, Flag, "--log-level"},
		{"flag>file", file, flag, nil, slog.LevelDebug, Flag, "--log-level"},
		{"flag>default", "", flag, nil, slog.LevelDebug, Flag, "--log-level"},
		{"env>file", file, nil, []string{env}, slog.LevelWarn, Env, "WHATSAPP_MCP_LOG_LEVEL"},
		{"env>default", "", nil, []string{env}, slog.LevelWarn, Env, "WHATSAPP_MCP_LOG_LEVEL"},
		{"file>default", file, nil, nil, slog.LevelError, File, "line 2"},
		{"default", "", nil, nil, slog.LevelInfo, Default, "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newFixture(t, tt.toml)
			cfg, _ := mustLoad(t, fx, tt.flags, tt.environ...)
			if cfg.LogLevel != tt.want {
				t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, tt.want)
			}
			o := cfg.Origin("log_level")
			want := tt.detail
			if tt.source == File {
				want = fx.configPath() + " " + tt.detail
			}
			if o.Source != tt.source || o.Detail != want {
				t.Errorf("Origin = %+v, want {%v %s}", o, tt.source, want)
			}
		})
	}
}

func TestAcceptanceSet(t *testing.T) {
	const file = "log_level = \"warn\"\n"
	tests := []struct {
		name    string
		toml    string
		flags   map[string]string
		environ []string
		want    slog.Level
	}{
		{"all three", file, map[string]string{"log-level": "debug"}, []string{"WHATSAPP_MCP_LOG_LEVEL=info"}, slog.LevelDebug},
		{"file and env", file, nil, []string{"WHATSAPP_MCP_LOG_LEVEL=info"}, slog.LevelInfo},
		{"file only", file, nil, nil, slog.LevelWarn},
		{"nothing", "", nil, nil, slog.LevelInfo},
		{"empty env is unset", file, nil, []string{"WHATSAPP_MCP_LOG_LEVEL="}, slog.LevelWarn},
		{"old name ignored", "", nil, []string{"LOG_LEVEL=debug"}, slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, warnings := mustLoad(t, newFixture(t, tt.toml), tt.flags, tt.environ...)
			if cfg.LogLevel != tt.want {
				t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, tt.want)
			}
			if len(warnings) != 0 {
				t.Errorf("warnings = %v, want none", warnings)
			}
		})
	}
}

func TestUnknownEnvironmentVariable(t *testing.T) {
	_, warnings := mustLoad(t, newFixture(t, ""), nil,
		"WHATSAPP_MCP_LOGLEVEL=debug", "WHATSAPP_MCP_LOGLEVEL=debug", "WHATSAPP_MCP_EMPTY=", "OTHER=1")
	if len(warnings) != 1 || warnings[0].Name != "WHATSAPP_MCP_LOGLEVEL" {
		t.Errorf("warnings = %v, want one for WHATSAPP_MCP_LOGLEVEL", warnings)
	}
}

func TestEnvironmentNameCase(t *testing.T) {
	cfg, warnings := mustLoad(t, newFixture(t, ""), nil, "whatsapp_mcp_log_level=debug")
	if runtime.GOOS == "windows" {
		if cfg.LogLevel != slog.LevelDebug {
			t.Errorf("LogLevel = %v; Windows names are case-insensitive", cfg.LogLevel)
		}
	} else if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v; names are exact outside Windows", cfg.LogLevel)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

func TestDataDirPrecedence(t *testing.T) {
	user := t.TempDir()
	flagDir, envDir := filepath.Join(t.TempDir(), "flag"), filepath.Join(t.TempDir(), "env")
	in := func(flags map[string]string, environ ...string) Inputs {
		return Inputs{Flags: flags, Environ: environ, UserConfigDir: func() (string, error) { return user, nil }}
	}
	tests := []struct {
		name string
		in   Inputs
		want string
		src  Source
	}{
		{"flag>env", in(map[string]string{"data-dir": flagDir}, "WHATSAPP_MCP_DATA_DIR="+envDir), flagDir, Flag},
		{"env>default", in(nil, "WHATSAPP_MCP_DATA_DIR="+envDir), envDir, Env},
		{"default", in(nil), filepath.Join(user, "whatsapp-mcp"), Default},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, err := Load(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DataDir != tt.want || cfg.Origin("data_dir").Source != tt.src {
				t.Errorf("DataDir = %q from %v, want %q from %v", cfg.DataDir, cfg.Origin("data_dir").Source, tt.want, tt.src)
			}
		})
	}
}

func TestInvalidValuePerSource(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		flags   map[string]string
		environ []string
		want    string
	}{
		{"flag", "", map[string]string{"log-level": "verbose"}, nil,
			`log_level: "verbose" from --log-level is not valid; use debug, info, warn or error`},
		{"env", "", nil, []string{"WHATSAPP_MCP_LOG_LEVEL=verbose"},
			`log_level: "verbose" from WHATSAPP_MCP_LOG_LEVEL is not valid; use debug, info, warn or error`},
		{"file", "log_level = \"verbose\"\n", nil, nil,
			`log_level: "verbose" from %s line 1 is not valid; use debug, info, warn or error`},
		{"data dir flag", "", map[string]string{"data-dir": "data"}, nil,
			`data_dir: "data" from --data-dir is not an absolute path`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newFixture(t, tt.toml)
			_, _, err := fx.load(tt.flags, tt.environ...)
			want := tt.want
			if strings.Contains(want, "%s") {
				want = fmt.Sprintf(want, fx.configPath())
			}
			var se *SettingError
			if !errors.As(err, &se) || err.Error() != want {
				t.Errorf("error = %v\nwant    %s", err, want)
			}
		})
	}
	t.Run("data dir env", func(t *testing.T) {
		_, _, err := Load(Inputs{Environ: []string{"WHATSAPP_MCP_DATA_DIR=data"}, UserConfigDir: os.UserConfigDir})
		if err == nil || err.Error() != `data_dir: "data" from WHATSAPP_MCP_DATA_DIR is not an absolute path` {
			t.Errorf("error = %v", err)
		}
	})
}

func TestAllErrorsReported(t *testing.T) {
	_, _, err := Load(Inputs{
		Flags:         map[string]string{"data-dir": "data"},
		Environ:       []string{"WHATSAPP_MCP_LOG_LEVEL=verbose"},
		UserConfigDir: os.UserConfigDir,
	})
	errs := flatten(err)
	if len(errs) != 2 {
		t.Fatalf("got %d errors, want 2: %v", len(errs), err)
	}
	if !strings.HasPrefix(errs[0].Error(), "data_dir:") || !strings.HasPrefix(errs[1].Error(), "log_level:") {
		t.Errorf("errors = %v", errs)
	}
}

func TestLogValues(t *testing.T) {
	fx := newFixture(t, "")
	cfg, _ := mustLoad(t, fx, map[string]string{"log-level": "DEBUG"})
	lines := cfg.LogValues()
	if len(lines) != len(Settings()) {
		t.Fatalf("LogValues has %d lines, want %d", len(lines), len(Settings()))
	}
	got := fmt.Sprint(lines[1])
	want := "[setting=log_level value=debug source=flag origin=--log-level]"
	if got != want {
		t.Errorf("log_level line = %s, want %s", got, want)
	}
	if got := lines[0][1].Value.String(); got != fx.dataDir {
		t.Errorf("data_dir value = %s, want %s", got, fx.dataDir)
	}
}
