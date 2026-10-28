//go:build unix

package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestExistingDatabaseWithOpenModeIsRefused(t *testing.T) {
	path := closedDB(t)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	wantStorage(t, filepath.Dir(path), "not_private", dbFileName)
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want it left at 0644", fi.Mode().Perm())
	}
	_, err := open(context.Background(), filepath.Dir(path), nil, openOptions{})
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("error %v lacks the chmod fix", err)
	}
}

func TestFifoArtifactIsRefused(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		path := closedDB(t)
		if suffix == "" {
			os.Remove(path)
		}
		if err := syscall.Mkfifo(path+suffix, 0o600); err != nil {
			t.Fatal(err)
		}
		wantStorage(t, filepath.Dir(path), "unsafe_artifact", dbFileName+suffix)
	}
}

func TestNewDatabaseIsMode0600(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	mustStore(t, st, msg("491511234567@s.whatsapp.net", "A", "491511234567@s.whatsapp.net", at(0)))
	for _, s := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(filepath.Join(dir, dbFileName+s))
		if err != nil {
			continue
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", dbFileName+s, fi.Mode().Perm())
		}
	}
}
