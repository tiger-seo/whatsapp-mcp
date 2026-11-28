package media

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

// workDirName is the directory in the data directory that holds the per-send working directories. It is not
// inside the outbox, so nothing in it can be sent as a user's file.
const workDirName = "send-work"

// The names inside one working directory. The converter only ever sees these fixed names.
const (
	workCopy      = "input"
	workConverted = "output.ogg"
	workEncrypted = "upload.enc"
)

var errWorkFile = errors.New("a private working file could not be written")

// newWorkDir creates one private working directory for a send.
func newWorkDir(dataDir string) (string, error) {
	parent := filepath.Join(dataDir, workDirName)
	if err := private.MkdirAll(parent); err != nil {
		return "", errWorkFile
	}
	dir, err := os.MkdirTemp(parent, "send-")
	if err != nil {
		return "", errWorkFile
	}
	if err := private.CheckDir(dir); err != nil {
		_ = os.Remove(dir)
		return "", errWorkFile
	}
	return dir, nil
}

// createWorkFile creates a new private file in a working directory.
func createWorkFile(dir, name string) (*os.File, error) {
	f, err := private.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return nil, errWorkFile
	}
	return f, nil
}

// removeDir removes a working directory. A process that was just stopped can hold a file for a moment, so a failed
// removal is tried again for a short while.
func removeDir(dir string) error {
	var err error
	for attempt := 0; attempt < 60; attempt++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}

// SweepWorkDirs removes the working directories that an earlier run left behind: after a crash, or after a file
// read that never ended. Only the holder of the data-directory lock may call it. It creates nothing.
func SweepWorkDirs(dataDir string) error {
	parent := filepath.Join(dataDir, workDirName)
	if err := private.CheckDir(parent); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		// RemoveAll removes a link itself and never follows it.
		errs = append(errs, os.RemoveAll(filepath.Join(parent, entry.Name())))
	}
	return errors.Join(errs...)
}
