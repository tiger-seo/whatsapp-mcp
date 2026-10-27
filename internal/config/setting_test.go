package config

import (
	"errors"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setting(t *testing.T, name string) Setting {
	t.Helper()
	for _, s := range Settings() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no setting %q in the registry", name)
	return Setting{}
}

func TestRegistryRules(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Settings() {
		if seen[s.Name] {
			t.Errorf("setting %q appears twice", s.Name)
		}
		seen[s.Name] = true
		if want := "WHATSAPP_MCP_" + strings.ToUpper(s.Name); s.Env != want {
			t.Errorf("%s: Env = %q, want %q", s.Name, s.Env, want)
		}
		if s.FileKey != "" && s.FileKey != s.Name {
			t.Errorf("%s: FileKey = %q, want empty or the name", s.Name, s.FileKey)
		}
		if s.FileKey != "" && s.ParseTOML == nil {
			t.Errorf("%s: allowed in the file but has no ParseTOML", s.Name)
		}
		if s.Flag == "" || s.Default == nil || s.ParseText == nil || s.Apply == nil || s.Text == nil || s.Usage == "" {
			t.Errorf("%s: incomplete descriptor", s.Name)
		}
	}
}

func TestLogLevelText(t *testing.T) {
	s := setting(t, "log_level")
	for in, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "DEBUG": slog.LevelDebug,
		"info": slog.LevelInfo, "Info": slog.LevelInfo,
		"warn": slog.LevelWarn, "warning": slog.LevelWarn, "WARNING": slog.LevelWarn,
		"error": slog.LevelError,
	} {
		got, err := s.ParseText(in)
		if err != nil || got != want {
			t.Errorf("ParseText(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"verbose", ""} {
		_, err := s.ParseText(in)
		if err == nil || !strings.Contains(err.Error(), "debug, info, warn or error") {
			t.Errorf("ParseText(%q) error = %v; want one listing the accepted values", in, err)
		}
	}
}

func TestLogLevelTOML(t *testing.T) {
	s := setting(t, "log_level")
	if got, err := s.ParseTOML("warn"); err != nil || got != slog.LevelWarn {
		t.Errorf(`ParseTOML("warn") = %v, %v`, got, err)
	}
	for _, in := range []any{int64(5), true} {
		_, err := s.ParseTOML(in)
		var te *TypeError
		if !errors.As(err, &te) || te.Want != "a string" {
			t.Errorf("ParseTOML(%#v) error = %v; want a type error naming string", in, err)
		}
	}
	if _, err := s.ParseTOML(int64(5)); err.Error() != "must be a string, not an integer" {
		t.Errorf("type error text = %q", err)
	}
}

func TestDataDirText(t *testing.T) {
	s := setting(t, "data_dir")
	abs := filepath.Join(t.TempDir(), "x", "..", "data")
	got, err := s.ParseText(abs)
	if err != nil || got != filepath.Clean(abs) {
		t.Errorf("ParseText(%q) = %v, %v; want %q", abs, got, err, filepath.Clean(abs))
	}
	bad := []string{"data", "./data"}
	if runtime.GOOS == "windows" {
		bad = append(bad, `\foo`, `C:foo`)
	}
	for _, in := range bad {
		_, err := s.ParseText(in)
		if err == nil || err.Error() != "is not an absolute path" {
			t.Errorf("ParseText(%q) error = %v; want is not an absolute path", in, err)
		}
	}
}
