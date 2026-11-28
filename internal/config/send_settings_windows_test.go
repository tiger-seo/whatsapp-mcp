package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

// junction creates a directory junction, which needs no privilege.
func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
}

func TestSendDirsJunction(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.MkdirAll(filepath.Join(target, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	junction(t, link, target)
	data := privateDir(t)
	for _, bad := range []string{link, filepath.Join(link, "inner")} {
		// With sending on, a junction in the entry stops startup.
		_, err := openDirs(t, loadSending(t, data, "*", bad))
		wantSendDirRefusal(t, err)
		// With sending off, nothing looks at it.
		if dirs, err := openDirs(t, loadSending(t, data, "", bad)); err != nil || len(dirs) != 0 {
			t.Errorf("sending off with %s: %v, %v", bad, dirs, err)
		}
	}
	// A data directory behind a junction: the default outbox's ancestors hold it.
	if err := PrepareDataDir(filepath.Join(target, "data")); err != nil {
		t.Fatal(err)
	}
	behind := filepath.Join(link, "data")
	if dirs, err := openDirs(t, loadSending(t, behind, "")); err != nil || len(dirs) != 0 {
		t.Errorf("sending off behind a junction: %v, %v", dirs, err)
	}
	_, err := openDirs(t, loadSending(t, behind, "*"))
	wantSendDirRefusal(t, err)
}

func TestSendDirsCaseAliasRequiresPrivateOutbox(t *testing.T) {
	data := privateDir(t)
	outbox := filepath.Join(data, "outbox")
	child := filepath.Join(outbox, "child")
	if err := private.MkdirAll(child); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(data, "OUTBOX")
	if _, err := os.Stat(alias); os.IsNotExist(err) {
		t.Skip("the volume is case-sensitive")
	} else if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{alias, filepath.Join(alias, "child")} {
		if _, err := openDirs(t, loadSending(t, data, "*", entry)); err != nil {
			t.Fatalf("private outbox alias refused: %v", err)
		}
	}
	if output, err := exec.Command("icacls", outbox, "/grant", "*S-1-5-32-545:(RX)").CombinedOutput(); err != nil {
		t.Fatalf("grant Users access to the test outbox: %v: %s", err, output)
	}
	for _, entry := range []string{outbox, alias, filepath.Join(alias, "child")} {
		_, err := openDirs(t, loadSending(t, data, "*", entry))
		wantSendDirRefusal(t, err)
		var privacyErr *private.Error
		if !errors.As(err, &privacyErr) {
			t.Errorf("expected a privacy error for %s, got %v", entry, err)
		}
	}
}
