package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
