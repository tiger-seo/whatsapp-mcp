package store

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fileState struct {
	size    int64
	modTime time.Time
	sha256  string
}

// snapshotDir records every file under dir, so that two snapshots are equal only if no file was created,
// removed or changed in size, modification time or content.
func snapshotDir(t testing.TB, dir string) map[string]fileState {
	t.Helper()
	snap := map[string]fileState{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		snap[rel] = fileState{size: fi.Size(), modTime: fi.ModTime(), sha256: hex.EncodeToString(sum[:])}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}
