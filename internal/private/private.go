package private

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Error says why a path is not private, and how the operator can fix it. Its text does not repeat the path,
// which the caller names.
type Error struct {
	Problem string
	Fix     string
}

func (e *Error) Error() string {
	if e.Fix == "" {
		return e.Problem
	}
	return e.Problem + "; " + e.Fix
}

var (
	errNotDir  = &Error{Problem: "exists and is not a directory"}
	errLink    = &Error{Problem: "is a symbolic link or another reparse point", Fix: "use the directory it points to"}
	errLinkF   = &Error{Problem: "is a symbolic link or another reparse point", Fix: "replace it with a regular file"}
	errIrreg   = &Error{Problem: "is not a regular file"}
	errHardLnk = &Error{Problem: "has more than one hard link", Fix: "replace it with a copy"}
)

// CheckDir checks that dir is a private directory, without creating or changing anything. A missing dir is
// an error that matches fs.ErrNotExist.
func CheckDir(dir string) error {
	return checkDir(dir)
}

// MkdirAll creates dir if it is missing, private from the start, and then checks it as CheckDir does. Missing
// parents are created with mode 0700; on Windows they inherit their parent's access list.
func MkdirAll(dir string) error {
	err := checkDir(dir)
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if parent := filepath.Dir(dir); parent != dir {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return unwrapPath(err)
		}
	}
	if err := mkdir(dir); err != nil && !errors.Is(err, fs.ErrExist) {
		return unwrapPath(err)
	}
	return checkDir(dir)
}

// OpenFile opens a private file. It never follows a symbolic link or opens a device or FIFO, and checks the
// open handle, so a file swapped in after the check is not used. With os.O_CREATE a missing file is created
// with mode 0600 on Unix; on Windows it inherits the directory's access list. The file is checked even when
// it was just created.
func OpenFile(path string, flag int) (*os.File, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&fs.ModeSymlink != 0 || isReparse(fi) {
			return nil, errLinkF
		}
		if !fi.Mode().IsRegular() {
			return nil, errIrreg
		}
	} else if !errors.Is(err, fs.ErrNotExist) || flag&os.O_CREATE == 0 {
		return nil, unwrapPath(err)
	}
	f, err := openFile(path, flag)
	if err != nil {
		return nil, err
	}
	if err := checkFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// unwrapPath drops the operation and path from an *fs.PathError, which the caller names itself.
func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
