//go:build unix

package store

import "golang.org/x/sys/unix"

// freeBytes returns the space available to this user in the directory's file system.
func freeBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
