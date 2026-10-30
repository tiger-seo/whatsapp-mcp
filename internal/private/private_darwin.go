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

// noACL is the entry count of a security attribute that holds an owner and a group but no ACL.
const noACL = 0xffffffff

// hasACL reports whether path carries an extended ACL, which grants access that the mode bits do not show.
func hasACL(path string) (bool, error) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return false, err
	}
	return aclReply(syscall.SYS_GETATTRLIST, uintptr(unsafe.Pointer(p)), unix.FSOPT_NOFOLLOW)
}

// hasACLOpen is hasACL for an open file, asked of the handle so that a path swapped after the open cannot change
// the answer.
func hasACLOpen(f *os.File) (bool, error) {
	return aclReply(unix.SYS_FGETATTRLIST, f.Fd(), 0)
}

func aclReply(call, target, options uintptr) (bool, error) {
	al := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	buf := make([]byte, 4096)
	_, _, errno := syscall.Syscall6(call, target, uintptr(unsafe.Pointer(&al)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), options, 0)
	if errno != 0 {
		return false, errno
	}
	return aclInReply(buf), nil
}

// aclInReply reads a getattrlist reply for the extended security attribute: the length of the reply, then a
// reference (an offset from the reference itself, and a length) to a filesec of a magic number, an owner and a
// group GUID of 16 bytes each, and an ACL that starts with its entry count. A reply with no reference or an empty
// one has no ACL, and so has a filesec whose entry count is zero or marks "no ACL". A reply that cannot be read is
// taken to hold one, so that a layout this code does not know refuses and never allows.
func aclInReply(reply []byte) bool {
	const header, filesecACL = 4 + 8, 4 + 16 + 16
	if len(reply) < 4 || binary.NativeEndian.Uint32(reply[:4]) <= 4 {
		return false
	}
	if len(reply) < header {
		return true
	}
	offset, length := int(int32(binary.NativeEndian.Uint32(reply[4:8]))), int(binary.NativeEndian.Uint32(reply[8:12]))
	if length == 0 {
		return false
	}
	start := 4 + offset
	if offset < 8 || length < filesecACL+4 || start < 0 || start+length > len(reply) {
		return true
	}
	count := binary.NativeEndian.Uint32(reply[start+filesecACL : start+filesecACL+4])
	return count != 0 && count != noACL
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
	acl, err := hasACLOpen(f)
	return aclError(f.Name(), err, acl)
}

// checkDirACL applies the same rule to the data directory.
func checkDirACL(dir string) error {
	acl, err := hasACL(dir)
	return aclError(dir, err, acl)
}
