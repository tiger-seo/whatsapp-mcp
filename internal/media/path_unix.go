//go:build unix

package media

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// openNonblock keeps a FIFO swapped in for the file from blocking the open.
const openNonblock = syscall.O_NONBLOCK

// linkCount is the number of names the open file has.
func linkCount(f *os.File) (uint64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no link count")
	}
	return uint64(st.Nlink), nil
}

// cutRoot accepts only a path from the file system root.
func cutRoot(p string) (string, bool) {
	return strings.CutPrefix(p, "/")
}

func splitComponents(rest string) []string { return strings.Split(rest, "/") }
