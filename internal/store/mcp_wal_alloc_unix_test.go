//go:build linux || darwin

package store

import "golang.org/x/sys/unix"

func walAllocatedBytes(path string) (int64, error) {
	var st unix.Stat_t
	err := unix.Stat(path, &st)
	return st.Blocks * 512, err
}
