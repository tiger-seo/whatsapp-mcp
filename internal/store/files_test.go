package store

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// closedDB returns the path of a valid database in a new directory, closed cleanly so it has no sidecars.
func closedDB(t *testing.T) string {
	t.Helper()
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	st.Close(context.Background())
	for _, s := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(filepath.Join(dir, dbFileName+s)); err == nil {
			t.Fatalf("a clean close left %s", s)
		}
	}
	return filepath.Join(dir, dbFileName)
}

// wantStorage asserts a refusal with the given code, that names every file, and that leaves dir unchanged.
func wantStorage(t *testing.T, dir string, code string, names ...string) {
	t.Helper()
	before := snapshotDir(t, dir)
	_, err := open(context.Background(), dir, nil, openOptions{})
	var se *StorageError
	if !errors.Is(err, ErrUnsafeStorage) || !errors.As(err, &se) || se.Code != code {
		t.Fatalf("open = %v, want an unsafe-storage refusal with code %s", err, code)
	}
	for _, n := range names {
		if !strings.Contains(err.Error(), n) {
			t.Errorf("error %q does not name %s", err, n)
		}
	}
	if !strings.Contains(err.Error(), "Recovering from a refused start") && se.Fix == "" {
		t.Errorf("error %q points to no recovery section or fix", err)
	}
	if after := snapshotDir(t, dir); !maps.Equal(before, after) {
		t.Errorf("the directory changed during a refused open:\n before %v\n after  %v", before, after)
	}
}

func TestOrphanSidecarsAreRefusedAndKept(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run("missing main "+suffix, func(t *testing.T) {
			dir := newDataDir(t)
			if err := os.WriteFile(filepath.Join(dir, dbFileName+suffix), []byte("keep me"), 0o600); err != nil {
				t.Fatal(err)
			}
			wantStorage(t, dir, "orphan_sidecar", dbFileName+suffix, "orphaned journal file")
		})
		t.Run("empty main "+suffix, func(t *testing.T) {
			dir := newDataDir(t)
			for name, data := range map[string]string{dbFileName: "", dbFileName + suffix: "keep me"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			wantStorage(t, dir, "orphan_sidecar", dbFileName+suffix)
		})
	}
}

func TestRollbackJournalIsRefusedAndKept(t *testing.T) {
	for name, content := range map[string][]byte{"empty": nil, "zeroed": make([]byte, 512), "data": []byte("pages")} {
		t.Run(name, func(t *testing.T) {
			path := closedDB(t)
			if err := os.WriteFile(path+"-journal", content, 0o600); err != nil {
				t.Fatal(err)
			}
			wantStorage(t, filepath.Dir(path), "rollback_journal", dbFileName+"-journal", "rollback journal")
		})
	}
}

func TestArtifactsThatAreNotPlainFilesAreRefused(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		t.Run("directory"+suffix, func(t *testing.T) {
			path := closedDB(t)
			if suffix == "" {
				os.Remove(path)
			}
			if err := os.Mkdir(path+suffix, 0o700); err != nil {
				t.Fatal(err)
			}
			wantStorage(t, filepath.Dir(path), "unsafe_artifact", dbFileName+suffix)
		})
		t.Run("hard link"+suffix, func(t *testing.T) {
			path := closedDB(t)
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			if suffix == "" {
				os.Remove(path)
			}
			if err := os.Link(target, path+suffix); err != nil {
				t.Skipf("cannot create a hard link here: %v", err)
			}
			code := "unsafe_artifact"
			if suffix == "-journal" {
				code = "rollback_journal" // a journal is refused as one before its type is looked at
			}
			wantStorage(t, filepath.Dir(path), code, dbFileName+suffix)
			assertFile(t, target, "outside")
		})
		t.Run("symlink"+suffix, func(t *testing.T) {
			path := closedDB(t)
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			if suffix == "" {
				os.Remove(path)
			}
			if err := os.Symlink(target, path+suffix); err != nil {
				t.Skipf("cannot create a symbolic link here: %v", err)
			}
			wantStorage(t, filepath.Dir(path), "unsafe_artifact", dbFileName+suffix)
			assertFile(t, target, "outside")
		})
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	if b, err := os.ReadFile(path); err != nil || string(b) != want {
		t.Errorf("%s = %q, %v; want it unchanged", path, b, err)
	}
}

func TestSwapBetweenCheckAndOpenIsRefused(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	st.Close(context.Background())
	other := closedDB(t)
	versionsBefore := readVersions2(t, other)

	hook := openOptions{hooks: testHooks{afterCheck: func() {
		if err := os.Rename(other, filepath.Join(dir, dbFileName)); err != nil {
			t.Errorf("swap: %v", err)
		}
	}}}
	_, err := open(context.Background(), dir, nil, hook)
	var se *StorageError
	if !errors.As(err, &se) || se.Code != "unsafe_artifact" {
		t.Fatalf("open = %v, want unsafe_artifact", err)
	}
	if got := readVersions2(t, filepath.Join(dir, dbFileName)); got != versionsBefore {
		t.Errorf("the swapped-in database changed: %s, was %s", got, versionsBefore)
	}
}

func TestCreatedFileSwappedIsRefused(t *testing.T) {
	dir := newDataDir(t)
	other := closedDB(t)
	hook := openOptions{hooks: testHooks{afterCreate: func() {
		if err := os.Rename(other, filepath.Join(dir, dbFileName)); err != nil {
			t.Errorf("swap: %v", err)
		}
	}}}
	_, err := open(context.Background(), dir, nil, hook)
	var se *StorageError
	if !errors.As(err, &se) || se.Code != "unsafe_artifact" {
		t.Fatalf("open = %v, want unsafe_artifact", err)
	}
}

// readVersions2 reads both version tables of a closed database file directly, without changing its schema.
func readVersions2(t *testing.T, path string) string {
	t.Helper()
	db := rawDB(t, filepath.Dir(path))
	defer db.Close() // a held file cannot be renamed over on Windows
	var d, m int
	if err := db.QueryRow("SELECT version FROM whatsmeow_version").Scan(&d); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT version FROM mcp_version").Scan(&m); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("device %d, messages %d", d, m)
}
