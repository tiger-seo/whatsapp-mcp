package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

const (
	dbFileName         = "whatsapp.db"
	recoverySectionFix = `see OPERATIONS.md "Recovering from a refused start"`
)

// artifactNames lists the database and the sidecars SQLite may create next to it.
func artifactNames(db string) []string {
	return []string{db, db + "-wal", db + "-shm", db + "-journal"}
}

// checkArtifacts examines the database and each sidecar without changing anything, and returns the identity of
// every file that exists. It refuses links and reparse points, anything but a regular file, hard links, a file
// that is not private, a rollback journal, and a sidecar next to a missing or empty database: SQLite would delete
// such a log on open, so the state is refused rather than treated as a fresh database. With live set, the
// database is already open, so sidecars next to a still-empty main file are expected and only links, file types,
// ownership and access are checked.
func checkArtifacts(dir string, live bool) (map[string]private.FileID, error) {
	db := filepath.Join(dir, dbFileName)
	infos := map[string]fs.FileInfo{}
	for _, p := range artifactNames(db) {
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, ioError("io")
		}
		if fi.Mode()&fs.ModeSymlink != 0 || isReparsePoint(fi) {
			return nil, unsafeStorage(p, "unsafe_artifact", "is a symbolic link or another reparse point, not a regular file", recoverySectionFix)
		}
		if !fi.Mode().IsRegular() {
			return nil, unsafeStorage(p, "unsafe_artifact", "is not a regular file", recoverySectionFix)
		}
		infos[p] = fi
	}

	var sidecars []string
	for _, p := range artifactNames(db)[1:] {
		if _, ok := infos[p]; ok {
			sidecars = append(sidecars, p)
		}
	}
	if mi, ok := infos[db]; !live && (!ok || mi.Size() == 0) && len(sidecars) > 0 {
		return nil, orphanError(sidecars, ok)
	}
	if _, ok := infos[db+"-journal"]; ok {
		return nil, unsafeStorage(db+"-journal", "rollback_journal",
			"is a rollback journal left behind by an interrupted write; it was left unchanged, "+recoverySectionFix, "")
	}

	ids := map[string]private.FileID{}
	for p := range infos {
		id, err := private.CheckFile(p)
		if err != nil {
			return nil, privateError(p, err)
		}
		ids[p] = id
	}
	return ids, nil
}

func orphanError(sidecars []string, emptyMain bool) error {
	names := make([]string, len(sidecars))
	for i, s := range sidecars {
		names[i] = safePath(s)
	}
	what := "exists without " + dbFileName + " (orphaned journal file)"
	if emptyMain {
		what = "exists next to an empty " + dbFileName + " (orphaned journal file)"
	}
	return &StorageError{
		Artifact: strings.Join(names, ", "), Code: "orphan_sidecar", sentinel: ErrUnsafeStorage,
		Reason: what + "; it was left unchanged, " + recoverySectionFix,
	}
}

func privateError(path string, err error) error {
	var pe *private.Error
	if errors.As(err, &pe) {
		code := "not_private"
		if strings.Contains(pe.Problem, "link") || strings.Contains(pe.Problem, "regular") {
			code = "unsafe_artifact"
		}
		return &StorageError{Artifact: safePath(path), Code: code, Reason: pe.Problem, Fix: pe.Fix, sentinel: ErrUnsafeStorage}
	}
	return ioError("io")
}

// sameArtifacts requires every file recorded in before to still be there with the same identity, which shows
// that no file was replaced between the check and SQLite's open.
func sameArtifacts(before, after map[string]private.FileID) error {
	for p, id := range before {
		if now, ok := after[p]; !ok || now != id {
			return unsafeStorage(p, "unsafe_artifact", "was replaced while the store was opening; startup stopped before any change", recoverySectionFix)
		}
	}
	return nil
}

// createDB creates the database file private from the start and returns its identity, taken from the handle that
// created it. SQLite then writes the schema into the existing file.
func createDB(path string) (private.FileID, error) {
	f, err := private.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return private.FileID{}, privateError(path, err)
	}
	defer f.Close()
	id, err := private.FileIDOf(f)
	if err != nil {
		return private.FileID{}, ioError("io")
	}
	return id, nil
}
