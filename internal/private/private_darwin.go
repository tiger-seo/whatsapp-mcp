//go:build darwin

package private

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// hasACL reports whether path carries an extended ACL, which grants access that the mode bits do not show.
// getattrlist returns the extended security attribute only when there is one, so a reply that is just the
// length word means there is none.
func hasACL(path string) (bool, error) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return false, err
	}
	al := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	buf := make([]byte, 4096)
	_, _, errno := syscall.Syscall6(syscall.SYS_GETATTRLIST, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&al)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unix.FSOPT_NOFOLLOW), 0)
	if errno != 0 {
		return false, errno
	}
	return binary.NativeEndian.Uint32(buf[:4]) > 4, nil
}

func aclError(path string, err error, acl bool) error {
	switch {
	case err != nil:
		return &Error{Problem: "its access control list cannot be read"}
	case acl:
		return &Error{
			Problem: "has an access control list entry that can grant other users access",
			Fix:     fmt.Sprintf("run: chmod -N %s", shellQuote(path)),
		}
	}
	return nil
}

func checkACL(f *os.File) error {
	acl, err := hasACL(f.Name())
	return aclError(f.Name(), err, acl)
}

// checkDirACL applies the same rule to the data directory.
func checkDirACL(dir string) error {
	acl, err := hasACL(dir)
	return aclError(dir, err, acl)
}
