package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
)

type result struct {
	code           int
	stdout, stderr string
}

// runIn calls run in process with empty stdin and the given environment.
func runIn(t *testing.T, environ []string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr, environ)
	return result{code, stdout.String(), stderr.String()}
}

func (r result) expect(t *testing.T, code int) {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, code, r.stdout, r.stderr)
	}
}

func TestVersion(t *testing.T) {
	dir := t.TempDir()
	for _, environ := range [][]string{
		nil,
		{"WHATSAPP_MCP_LOG_LEVEL=verbose", "WHATSAPP_MCP_DATA_DIR=relative"},
		{"WHATSAPP_MCP_DATA_DIR=" + filepath.Join(dir, "data")},
	} {
		r := runIn(t, environ, "version")
		r.expect(t, exitOK)
		if r.stderr != "" {
			t.Errorf("stderr = %q, want empty", r.stderr)
		}
		lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
		prefixes := []string{"whatsapp-mcp ", "commit:", "built:", "go:", "target:"}
		if len(lines) != len(prefixes) {
			t.Fatalf("version printed %d lines, want %d:\n%s", len(lines), len(prefixes), r.stdout)
		}
		for i, p := range prefixes {
			if !strings.HasPrefix(lines[i], p) {
				t.Errorf("line %d = %q, want prefix %q", i+1, lines[i], p)
			}
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("version created %d entries in the data directory", len(entries))
	}
}

func checkCommandList(t *testing.T, out string) {
	t.Helper()
	for _, cmd := range commands() {
		if !strings.Contains(out, cmd.name) || !strings.Contains(out, cmd.help) {
			t.Errorf("usage does not list %s (%s):\n%s", cmd.name, cmd.help, out)
		}
	}
}

func TestHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		r := runIn(t, nil, arg)
		r.expect(t, exitOK)
		checkCommandList(t, r.stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		args   []string
		prefix string
		usage  bool
	}{
		{nil, "whatsapp-mcp: missing command", true},
		{[]string{"frobnicate"}, `whatsapp-mcp: unknown command "frobnicate"`, true},
		{[]string{"--log-level", "debug", "stdio"}, `whatsapp-mcp: flags go after the command, e.g. "whatsapp-mcp stdio --log-level debug"`, false},
		{[]string{"stdio", "--foo"}, "whatsapp-mcp: stdio: flag provided but not defined: -foo", false},
		{[]string{"status", "x"}, `whatsapp-mcp: status: unexpected argument "x"`, false},
		{[]string{"version", "--log-level", "debug"}, "whatsapp-mcp: version: flag provided but not defined: -log-level", false},
	}
	for _, tt := range tests {
		r := runIn(t, nil, tt.args...)
		r.expect(t, exitUsage)
		if !strings.HasPrefix(r.stderr, tt.prefix) {
			t.Errorf("%q: stderr = %q, want prefix %q", tt.args, r.stderr, tt.prefix)
		}
		if tt.usage {
			checkCommandList(t, r.stderr)
		}
		if r.stdout != "" {
			t.Errorf("%q: stdout = %q, want empty", tt.args, r.stdout)
		}
	}
}

func TestCommandHelp(t *testing.T) {
	r := runIn(t, nil, "stdio", "--help")
	r.expect(t, exitOK)
	for _, want := range []string{"--data-dir", "WHATSAPP_MCP_DATA_DIR", "--log-level", "WHATSAPP_MCP_LOG_LEVEL", "default info"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdio --help does not contain %q:\n%s", want, r.stdout)
		}
	}
}

var logLine = regexp.MustCompile(`^time=\S+ level=(DEBUG|INFO|WARN|ERROR) msg=`)

func checkLogLines(t *testing.T, r result) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n") {
		if strings.HasPrefix(line, "time=") && !logLine.MatchString(line) {
			t.Errorf("malformed log line %q", line)
		}
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigLayers(t *testing.T) {
	dir := filepath.Clean(t.TempDir())
	writeConfig(t, dir, "log_level = \"warn\"\n")
	env := []string{"WHATSAPP_MCP_LOG_LEVEL=info"}

	r := runIn(t, env, "status", "--data-dir", dir, "--log-level", "debug")
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, "level=DEBUG msg=config setting=log_level value=debug source=flag origin=--log-level") {
		t.Errorf("no debug dump for log_level:\n%s", r.stderr)
	}
	if !strings.Contains(r.stderr, "msg=config setting=data_dir value="+dir+" source=flag origin=--data-dir") {
		t.Errorf("no debug dump for data_dir:\n%s", r.stderr)
	}
	checkLogLines(t, r)

	r = runIn(t, env, "status", "--data-dir", dir)
	r.expect(t, exitError)
	if strings.Contains(r.stderr, "level=DEBUG") {
		t.Errorf("debug lines at level info:\n%s", r.stderr)
	}
	checkLogLines(t, r)
}

func TestInvalidConfiguration(t *testing.T) {
	r := runIn(t, []string{"WHATSAPP_MCP_LOG_LEVEL=verbose"}, "status", "--data-dir", t.TempDir())
	r.expect(t, exitUsage)
	if !strings.HasPrefix(r.stderr, "whatsapp-mcp: invalid configuration:\n") {
		t.Errorf("stderr = %q", r.stderr)
	}
	for _, want := range []string{"log_level", `"verbose"`, "WHATSAPP_MCP_LOG_LEVEL", "debug, info, warn or error"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, r.stderr)
		}
	}
	checkLogLines(t, r)
}

func TestUnknownVariableWarning(t *testing.T) {
	r := runIn(t, []string{"WHATSAPP_MCP_LOGLEVEL=debug"}, "status", "--data-dir", t.TempDir())
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, `level=WARN msg="unknown environment variable" name=WHATSAPP_MCP_LOGLEVEL`) {
		t.Errorf("no warning:\n%s", r.stderr)
	}
	checkLogLines(t, r)
}

func TestStatusDataDir(t *testing.T) {
	nested := filepath.Join(t.TempDir(), "a", "b")
	r := runIn(t, nil, "status", "--data-dir", nested)
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, "whatsapp-mcp: status is not implemented yet") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if fi, err := os.Stat(nested); err != nil || !fi.IsDir() {
		t.Errorf("data directory not created: %v", err)
	}

	cwd := t.TempDir()
	t.Chdir(cwd)
	runIn(t, nil, "status", "--data-dir", "data").expect(t, exitUsage)
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Errorf("a relative --data-dir created %d entries in the working directory", len(entries))
	}

	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r = runIn(t, nil, "status", "--data-dir", file)
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, file) || !strings.Contains(r.stderr, "not a directory") {
		t.Errorf("stderr = %q", r.stderr)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	runIn(t, []string{"WHATSAPP_MCP_LOG_LEVEL=verbose"}, "status", "--data-dir", missing).expect(t, exitUsage)
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("invalid configuration created the data directory (stat: %v)", err)
	}
}

func TestLockHeld(t *testing.T) {
	dir := filepath.Clean(t.TempDir())
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("whatsapp-mcp: another whatsapp-mcp (PID %d) is using the data directory %s", os.Getpid(), dir)
	for _, name := range []string{"pair", "stdio", "serve"} {
		start := time.Now()
		r := runIn(t, nil, name, "--data-dir", dir)
		r.expect(t, exitError)
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("%s took %v with the lock held", name, elapsed)
		}
		if !strings.Contains(r.stderr, want) {
			t.Errorf("%s: stderr = %q, want %q", name, r.stderr, want)
		}
	}
	r := runIn(t, nil, "status", "--data-dir", dir)
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, "status is not implemented yet") {
		t.Errorf("status was blocked by the lock: %q", r.stderr)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}

	r = runIn(t, nil, "pair", "--data-dir", dir, "--log-level", "debug")
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, "whatsapp-mcp: pair is not implemented yet") || !strings.Contains(r.stderr, "msg=\"lock acquired\"") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if data, err := os.ReadFile(filepath.Join(dir, lock.FileName)); err != nil || len(data) != 0 {
		t.Errorf("lock file after pair: %q, %v; want it empty", data, err)
	}
}

func TestStdioStdinClosed(t *testing.T) {
	r := runIn(t, nil, "stdio", "--data-dir", t.TempDir())
	r.expect(t, exitOK)
	for _, want := range []string{"stdio: MCP server not implemented yet", `msg="shutting down" reason="stdin closed"`, "msg=stopped"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, r.stderr)
		}
	}
	checkLogLines(t, r)
}
