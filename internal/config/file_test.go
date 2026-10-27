package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileMissingOrEmpty(t *testing.T) {
	t.Run("missing data dir", func(t *testing.T) {
		fx := newFixture(t, "")
		cfg, _ := mustLoad(t, fx, nil)
		if cfg.LogLevel != slog.LevelInfo {
			t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
		}
		if _, err := os.Stat(fx.dataDir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Load created the data directory (stat: %v)", err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		fx := newFixture(t, "")
		if err := os.MkdirAll(fx.dataDir, 0o700); err != nil {
			t.Fatal(err)
		}
		mustLoad(t, fx, nil)
		if entries, _ := os.ReadDir(fx.dataDir); len(entries) != 0 {
			t.Errorf("Load created %d entries", len(entries))
		}
	})
	t.Run("empty file", func(t *testing.T) {
		fx := newFixture(t, "\n")
		if cfg, _ := mustLoad(t, fx, nil); cfg.LogLevel != slog.LevelInfo {
			t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
		}
	})
}

// fileErrors loads a fixture with the given config.toml and returns the errors found.
func fileErrors(t *testing.T, toml string, environ ...string) (fixture, []error) {
	t.Helper()
	fx := newFixture(t, toml)
	_, _, err := fx.load(nil, environ...)
	if err == nil {
		t.Fatalf("Load accepted config.toml %q", toml)
	}
	return fx, flatten(err)
}

func TestFileSyntaxError(t *testing.T) {
	fx, errs := fileErrors(t, `log_level = "info`, "WHATSAPP_MCP_LOG_LEVEL=verbose")
	if len(errs) != 2 {
		t.Fatalf("got %d errors, want the syntax error and the env error: %v", len(errs), errs)
	}
	var fe *FileError
	if !errors.As(errs[0], &fe) || fe.Line != 1 || fe.Column == 0 || fe.Path != fx.configPath() {
		t.Errorf("first error = %#v, want a FileError at line 1 with a column", errs[0])
	}
	if prefix := fx.configPath() + " line 1, column "; !strings.HasPrefix(errs[0].Error(), prefix) {
		t.Errorf("message = %q, want prefix %q", errs[0], prefix)
	}
	if !strings.Contains(errs[1].Error(), "WHATSAPP_MCP_LOG_LEVEL") {
		t.Errorf("second error = %v, want the env error", errs[1])
	}
}

func TestFileKeyErrors(t *testing.T) {
	tests := []struct {
		name, toml, want string
	}{
		{"unknown key", "# comment\n\nloglevel = \"info\"\n", `line 3: unknown key "loglevel"`},
		{"table", "[section]\nx = 1\n", `line 1: unknown key "section"`},
		{"wrong type", "log_level = 5\n", "line 1: log_level must be a string, not an integer"},
		{"wrong type bool", "log_level = true\n", "line 1: log_level must be a string, not a boolean"},
		{"data_dir", "\ndata_dir = \"/x\"\n", "line 2: data_dir cannot be set in config.toml, because the file is inside the data directory; use --data-dir or WHATSAPP_MCP_DATA_DIR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx, errs := fileErrors(t, tt.toml)
			if len(errs) != 1 {
				t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
			}
			if want := fx.configPath() + " " + tt.want; errs[0].Error() != want {
				t.Errorf("error = %q\nwant    %q", errs[0], want)
			}
		})
	}
}

func TestFileInvalidValue(t *testing.T) {
	fx, errs := fileErrors(t, "# level\nlog_level = \"verbose\"\n")
	want := `log_level: "verbose" from ` + fx.configPath() + ` line 2 is not valid; use debug, info, warn or error`
	if len(errs) != 1 || errs[0].Error() != want {
		t.Errorf("errors = %v\nwant     %s", errs, want)
	}
}

func TestFileUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions and a non-root user")
	}
	fx := newFixture(t, "log_level = \"info\"\n")
	if err := os.Chmod(fx.configPath(), 0); err != nil {
		t.Fatal(err)
	}
	_, _, err := fx.load(nil)
	if want := fx.configPath() + ": permission denied"; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
}

func TestFileDataDirIsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fx := fixture{dataDir: path, userDir: t.TempDir()}
	if _, _, err := fx.load(nil); err != nil {
		t.Errorf("Load: %v; a data directory that is a file is reported when it is prepared", err)
	}
}
