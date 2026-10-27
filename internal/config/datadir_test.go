package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	user := t.TempDir()
	in := Inputs{UserConfigDir: func() (string, error) { return user, nil }}
	cfg, _, err := Load(in)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(user, "whatsapp-mcp")
	if cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	t.Chdir(t.TempDir())
	if cfg, _, _ := Load(in); cfg.DataDir != want {
		t.Errorf("after Chdir DataDir = %q, want %q", cfg.DataDir, want)
	}
}

func TestNoUserConfigDir(t *testing.T) {
	failing := func() (string, error) { return "", errors.New("$HOME is not defined") }
	_, _, err := Load(Inputs{UserConfigDir: failing})
	var ue *UserConfigDirError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--data-dir") || !strings.Contains(err.Error(), "WHATSAPP_MCP_DATA_DIR") {
		t.Errorf("error = %v, want one suggesting --data-dir and WHATSAPP_MCP_DATA_DIR", err)
	}
	dir := t.TempDir()
	if _, _, err := Load(Inputs{Flags: map[string]string{"data-dir": dir}, UserConfigDir: failing}); err != nil {
		t.Errorf("with --data-dir: %v", err)
	}
}

func TestRelativeDataDir(t *testing.T) {
	user := func() (string, error) { return t.TempDir(), nil }
	for _, in := range []Inputs{
		{Flags: map[string]string{"data-dir": "data"}, UserConfigDir: user},
		{Environ: []string{"WHATSAPP_MCP_DATA_DIR=./data"}, UserConfigDir: user},
	} {
		_, _, err := Load(in)
		if err == nil || !strings.HasSuffix(err.Error(), "is not an absolute path") {
			t.Errorf("error = %v, want is not an absolute path", err)
		}
	}
}

func TestDataDirCleaned(t *testing.T) {
	base := t.TempDir()
	raw := filepath.Join(base, "x") + string(filepath.Separator) + ".." + string(filepath.Separator) + "data" + string(filepath.Separator)
	cfg, _, err := Load(Inputs{Flags: map[string]string{"data-dir": raw}, UserConfigDir: os.UserConfigDir})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "data"); cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
}

func TestPrepareDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "nested")
	if err := PrepareDataDir(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700", fi.Mode().Perm())
	}
	if err := PrepareDataDir(dir); err != nil {
		t.Errorf("second call: %v", err)
	}
}

func TestPrepareDataDirKeepsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ignores the mode bits")
	}
	dir := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PrepareDataDir(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755 unchanged", fi.Mode().Perm())
	}
}

func TestPrepareDataDirOnFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := PrepareDataDir(path)
	if want := "data directory " + path + ": exists and is not a directory"; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
}
