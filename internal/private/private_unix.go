//go:build unix

package private

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func checkDir(dir string) error {
	// O_NOFOLLOW refuses a symbolic link as the last component, and O_DIRECTORY anything but a directory.
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		switch {
		case errors.Is(err, unix.ELOOP):
			return errLink
		case errors.Is(err, unix.ENOTDIR):
			// Linux reports a symbolic link to a directory as ENOTDIR, macOS as ELOOP.
			if fi, lerr := os.Lstat(dir); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
				return errLink
			} else if lerr == nil && !fi.IsDir() {
				return errNotDir
			}
			// A parent is not a directory, so dir cannot exist.
			return fmt.Errorf("%w: a parent is not a directory", fs.ErrNotExist)
		}
		return err
	}
	f := os.NewFile(uintptr(fd), dir)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return unwrapPath(err)
	}
	if err := checkOwner(fi); err != nil {
		return err
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return &Error{
			Problem: fmt.Sprintf("other users can access it (mode %04o)", perm),
			Fix:     fmt.Sprintf("run: chmod 700 %s", shellQuote(dir)),
		}
	}
	if err := checkDirACL(dir); err != nil {
		return err
	}
	return checkParent(filepath.Dir(dir))
}

// checkParent refuses a parent that another user could use to rename or replace the directory: one owned by
// another user who is not root, or one writable by others without the sticky bit.
func checkParent(parent string) error {
	pi, err := os.Stat(parent)
	if err != nil {
		return unwrapPath(err)
	}
	if st, ok := pi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && int(st.Uid) != os.Geteuid() {
		return &Error{
			Problem: fmt.Sprintf("its parent %s is owned by uid %d, who could replace it", parent, st.Uid),
			Fix:     "use a parent owned by this user or by root",
		}
	}
	if perm := pi.Mode().Perm(); perm&0o022 != 0 && pi.Mode()&fs.ModeSticky == 0 {
		return &Error{
			Problem: fmt.Sprintf("its parent %s can be written by other users (mode %04o), who could replace it", parent, perm),
			Fix:     fmt.Sprintf("run: chmod go-w %s, or use another parent", shellQuote(parent)),
		}
	}
	return nil
}

func mkdir(dir string) error {
	return os.Mkdir(dir, 0o700)
}

func openFile(path string, flag int) (*os.File, error) {
	// O_NONBLOCK keeps a FIFO swapped in after the Lstat from blocking the open; checkFile then rejects it.
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, errLinkF
		}
		return nil, unwrapPath(err)
	}
	return f, nil
}

func checkFile(f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return unwrapPath(err)
	}
	if !fi.Mode().IsRegular() {
		return errIrreg
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && uint64(st.Nlink) > 1 {
		return errHardLnk
	}
	if err := checkOwner(fi); err != nil {
		return err
	}
	if perm := fi.Mode().Perm(); perm&0o022 != 0 {
		return &Error{
			Problem: fmt.Sprintf("other users can write it (mode %04o)", perm),
			Fix:     fmt.Sprintf("run: chmod 600 %s", shellQuote(f.Name())),
		}
	}
	return nil
}

func checkOwner(fi fs.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return &Error{Problem: "its owner cannot be read"}
	}
	if euid := os.Geteuid(); int(st.Uid) != euid {
		return &Error{
			Problem: fmt.Sprintf("is owned by uid %d, not by this user (uid %d)", st.Uid, euid),
			Fix:     "use one this user owns",
		}
	}
	return nil
}

func isReparse(fs.FileInfo) bool { return false }

// shellQuote quotes a path for a POSIX shell when it needs it.
func shellQuote(s string) string {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '/' || r == '.' || r == '_' || r == '-') {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

// checkFileStrict is the stricter rule for the database and its sidecars: no group or other permission bits,
// and on macOS no ACL entry.
func checkFileStrict(f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return unwrapPath(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return &Error{
			Problem: fmt.Sprintf("other users can read it (mode %04o)", perm),
			Fix:     fmt.Sprintf("run: chmod 600 %s", shellQuote(f.Name())),
		}
	}
	return checkACL(f)
}

func fileID(f *os.File) (FileID, error) {
	fi, err := f.Stat()
	if err != nil {
		return FileID{}, unwrapPath(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return FileID{}, &Error{Problem: "its identity cannot be read"}
	}
	return FileID{A: uint64(st.Dev), B: uint64(st.Ino)}, nil
}
