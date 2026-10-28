package store

import (
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsPathFormsAreRefused(t *testing.T) {
	for _, p := range []string{`\server\share\x`, `\?\C:\x`, `\.\C:\x`, `C:\x:stream`, `//server/share/x`} {
		err := checkPathForm(p)
		var se *StorageError
		if !errors.Is(err, ErrUnsafeStorage) || !errors.As(err, &se) || se.Code != "unsupported_path" {
			t.Errorf("checkPathForm(%q) = %v, want unsupported_path", p, err)
		}
	}
	if err := checkPathForm(t.TempDir()); err != nil {
		t.Errorf("a local temp directory: %v", err)
	}
}

// A drive made with subst looks local by its name but resolves to a real directory, which is what is checked.
func TestWindowsSubstDriveResolves(t *testing.T) {
	dir := newDataDir(t)
	const drive = "Q:"
	if out, err := osexec.Command("subst", drive, dir).CombinedOutput(); err != nil {
		t.Skipf("cannot create a subst drive here: %v %s", err, out)
	}
	defer osexec.Command("subst", drive, "/d").Run()
	if err := checkPathForm(drive + `\`); err != nil {
		t.Errorf("a subst drive onto a local directory: %v, want accepted", err)
	}
	_ = filepath.Join
}

// A junction has no symbolic-link mode bit, so it is caught as a reparse point.
func TestWindowsJunctionArtifactsAreRefused(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		path := closedDB(t)
		if suffix == "" {
			os.Remove(path)
		}
		target := t.TempDir()
		out, err := osexec.Command("cmd", "/c", "mklink", "/J", path+suffix, target).CombinedOutput()
		if err != nil {
			t.Skipf("cannot create a junction here: %v %s", err, out)
		}
		_, err = open(context.Background(), filepath.Dir(path), nil, openOptions{})
		var se *StorageError
		if !errors.As(err, &se) || se.Code != "unsafe_artifact" || !strings.Contains(err.Error(), dbFileName+suffix) {
			t.Errorf("junction at %s: open = %v, want unsafe_artifact naming it", dbFileName+suffix, err)
		}
		if entries, _ := os.ReadDir(target); len(entries) != 0 {
			t.Errorf("something was created in the junction's target: %v", entries)
		}
	}
}
