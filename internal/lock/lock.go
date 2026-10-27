package lock

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/gofrs/flock"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

// FileName is the lock file in the data directory.
const FileName = "whatsapp-mcp.lock"

// The holder's PID is stored from byte 1. On Windows the OS lock covers byte 0 and is mandatory, so no
// other process could read a PID stored there; byte 0 is never read or written.
const pidOffset = 1

// Lock is an OS lock on a data directory, held until Release or until the process dies.
type Lock struct {
	Path string

	fl   *flock.Flock
	f    *os.File
	once sync.Once
	err  error
}

// HeldError means another process holds the lock. PID is 0 when it could not be read.
type HeldError struct {
	Path    string
	DataDir string
	PID     int
}

func (e *HeldError) Error() string {
	pid := "unknown"
	if e.PID > 0 {
		pid = strconv.Itoa(e.PID)
	}
	return fmt.Sprintf("another whatsapp-mcp (PID %s) is using the data directory %s", pid, e.DataDir)
}

// Acquire takes the lock on dataDir without waiting, and records this process's PID in the lock file.
// If another process holds it, the error is a *HeldError.
//
// The lock file must be a private regular file, never a link, so that whatever is at its path cannot make
// Acquire or Release write to another file.
func Acquire(dataDir string) (*Lock, error) {
	path := filepath.Join(dataDir, FileName)
	// flock does not expose its handle, so the PID goes through a second one. That one is opened first and
	// checked; flock's must then be the same file, or a swap between the two opens could redirect the writes.
	f, err := private.OpenFile(path, os.O_RDWR|os.O_CREATE)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	fl := flock.New(path, flock.SetFlag(os.O_RDWR))
	ok, err := fl.TryLock()
	if err == nil {
		err = sameFile(f, fl)
	}
	if err != nil {
		fl.Close()
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if !ok {
		defer f.Close()
		return nil, &HeldError{Path: path, DataDir: dataDir, PID: readPID(f)}
	}
	if err = f.Truncate(pidOffset); err == nil {
		_, err = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), pidOffset)
	}
	if err != nil {
		f.Close()
		fl.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &Lock{Path: path, fl: fl, f: f}, nil
}

// errReplaced means the lock file was replaced between the two opens.
var errReplaced = errors.New("the lock file was replaced while it was being opened; check who else can write to the data directory")

func sameFile(f *os.File, fl *flock.Flock) error {
	a, err := f.Stat()
	if err != nil {
		return err
	}
	b, err := fl.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(a, b) {
		return errReplaced
	}
	return nil
}

// Release empties the lock file while still holding the lock, so a newer holder's PID is never lost,
// then unlocks it. The file itself stays. Calling Release again does nothing.
func (l *Lock) Release() error {
	l.once.Do(func() {
		l.err = errors.Join(l.f.Truncate(0), l.f.Close(), l.fl.Close())
	})
	return l.err
}

func readPID(f *os.File) int {
	buf := make([]byte, 32)
	n, err := f.ReadAt(buf, pidOffset)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0
	}
	pid, err := strconv.Atoi(string(bytes.Trim(buf[:n], "\x00 \t\r\n")))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}
