package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

func wantAbsent(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s exists (%v)", path, err)
		}
	}
}

// stdioSession runs stdio with a synthetic owner, calls fn once the session is initialized and returns the exit
// code and stderr.
func stdioSession(t *testing.T, args []string, fn func(p *stdioPeer)) (int, string) {
	t.Helper()
	original := newOwner
	t.Cleanup(func() { newOwner = original })
	newOwner = func(ownerDeps) (*ownerHandle, error) { return &ownerHandle{owner: newScriptedOwner()}, nil }
	p := newStdioPeer(t)
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run(append([]string{"stdio"}, args...), p.stdin, p.stdout, &stderr, nil) }()
	init := p.initialize(t)
	if fn != nil {
		checkStdioDiscovery(t, p, init)
		fn(p)
	}
	_ = p.input.Close()
	code := awaitStdio(t, done, "stdio exit")
	p.close()
	return code, stderr.String()
}

// stdioRefused runs stdio expecting it to end before it reads anything.
func stdioRefused(t *testing.T, args ...string) (int, string) {
	t.Helper()
	original := newOwner
	t.Cleanup(func() { newOwner = original })
	owners := 0
	newOwner = func(ownerDeps) (*ownerHandle, error) { owners++; return &ownerHandle{owner: newScriptedOwner()}, nil }
	input := &countedStdioInput{}
	var out, stderr bytes.Buffer
	code := run(append([]string{"stdio"}, args...), input, &out, &stderr, nil)
	if out.Len() != 0 || input.calls.Load() != 0 || owners != 0 {
		t.Errorf("stdout %q, %d reads, %d owners", out.String(), input.calls.Load(), owners)
	}
	return code, stderr.String()
}

func TestSendWiringOutboxAndTools(t *testing.T) {
	dir := privateDir(t)
	outbox := filepath.Join(dir, "outbox")
	leftover := filepath.Join(dir, "send-work", "send-123")
	if err := private.MkdirAll(leftover); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "input"), []byte("left"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stderr := stdioSession(t, []string{"--data-dir", dir, "--send-allowed-recipients", "491511000001"}, func(p *stdioPeer) {
		if err := private.CheckDir(outbox); err != nil {
			t.Errorf("outbox: %v", err)
		}
		wantAbsent(t, leftover)
		// The send service is wired to the owner's port: this owner has none, so it is not connected.
		p.send(t, `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"send_message","arguments":{"recipient":"491511000001","message":"synthetic-secret"}}}`)
		var result struct {
			IsError           bool
			StructuredContent struct{ Error struct{ Code string } }
		}
		if err := json.Unmarshal(p.reply(t, 20), &result); err != nil || !result.IsError || result.StructuredContent.Error.Code != "not_connected" {
			t.Errorf("send = %+v, %v", result, err)
		}
	})
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "tool=send_message recipient_kind=phone outcome=not_sent reason=not_connected") || strings.Contains(stderr, "synthetic-secret") || strings.Contains(stderr, "491511000001") {
		t.Errorf("stderr:\n%s", stderr)
	}
	requireReleasedLock(t, dir)
}

func TestSendWiringOffTouchesNothing(t *testing.T) {
	dir := privateDir(t)
	missing := filepath.Join(t.TempDir(), "missing")
	for _, args := range [][]string{
		{"--data-dir", dir},
		{"--data-dir", dir, "--send-allowed-dirs", missing},
		{"--data-dir", dir, "--send-allowed-dirs", dir},
	} {
		code, stderr := stdioSession(t, args, func(p *stdioPeer) {
			p.send(t, `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"send_message","arguments":{"recipient":"491511000001","message":"x"}}}`)
			if result := p.reply(t, 20); !strings.Contains(string(result), `"not_permitted"`) {
				t.Errorf("send with sending off = %s", result)
			}
		})
		if code != exitOK {
			t.Fatalf("exit %d with %v: %s", code, args, stderr)
		}
		wantAbsent(t, filepath.Join(dir, "outbox"), filepath.Join(dir, "send-work"), missing)
	}
}

func TestSendWiringRefusesBadSettings(t *testing.T) {
	dir := privateDir(t)
	missing := filepath.Join(t.TempDir(), "missing")
	for name, tt := range map[string]struct {
		args    []string
		code    int
		setting string
	}{
		"missing directory": {[]string{"--send-allowed-recipients", "*", "--send-allowed-dirs", missing}, exitError, "send_allowed_dirs"},
		"data directory":    {[]string{"--send-allowed-recipients", "*", "--send-allowed-dirs", dir}, exitError, "send_allowed_dirs"},
		"relative":          {[]string{"--send-allowed-dirs", "relative"}, exitUsage, "send_allowed_dirs"},
		"recipient":         {[]string{"--send-allowed-recipients", "status@broadcast"}, exitUsage, "send_allowed_recipients"},
		"ffmpeg":            {[]string{"--ffmpeg", missing}, exitUsage, "ffmpeg"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stderr := stdioRefused(t, append([]string{"--data-dir", dir}, tt.args...)...)
			if code != tt.code || !strings.Contains(stderr, "setting="+tt.setting+" ") {
				t.Errorf("exit %d, stderr %q", code, stderr)
			}
			if strings.Contains(stderr, "status@broadcast") {
				t.Errorf("stderr repeats the value: %q", stderr)
			}
			requireReleasedLock(t, dir)
		})
	}
	writeConfig(t, dir, "send_allowed_recipients = \"491511000001\"\n")
	code, stderr := stdioRefused(t, "--data-dir", dir)
	if code != exitUsage || !strings.Contains(stderr, "setting=send_allowed_recipients line=1") || strings.Contains(stderr, "491511000001") {
		t.Errorf("file type error: exit %d, stderr %q", code, stderr)
	}
}

func TestSendWiringCreatesNothingWithoutTheLock(t *testing.T) {
	dir := privateDir(t)
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	code, _ := stdioRefused(t, "--data-dir", dir, "--send-allowed-recipients", "*")
	if code != exitError {
		t.Errorf("exit %d", code)
	}
	wantAbsent(t, filepath.Join(dir, "outbox"))
}

func TestSendWiringStatusCreatesNothing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "data")
	args := []string{"status", "--send-allowed-recipients", "*", "--data-dir"}
	runIn(t, nil, append(args, missing)...).expect(t, exitOK)
	wantAbsent(t, missing)
	dir := privateDir(t)
	runIn(t, nil, append(args, dir)...).expect(t, exitOK)
	wantAbsent(t, filepath.Join(dir, "outbox"), filepath.Join(dir, "send-work"))
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("status left %v, %v", entries, err)
	}
	// Directories are not looked at either.
	runIn(t, nil, append(args, dir, "--send-allowed-dirs", filepath.Join(dir, "nowhere"))...).expect(t, exitOK)
}
