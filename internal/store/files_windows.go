package store

import (
	"io/fs"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const networkFix = "use a local NTFS disk"

// checkPathForm rejects data directory paths that are not a plain local path, before any file is touched, and
// then asks Windows where the directory really is: a mapped network drive or a subst drive looks local by its
// name alone.
func checkPathForm(dir string) error {
	bad := func(why string) error {
		return unsafeStorage(dir, "unsupported_path", why, networkFix)
	}
	switch {
	case strings.HasPrefix(dir, `\`) || strings.HasPrefix(dir, "//"):
		return bad("is a network or device path")
	case len(dir) > 2 && strings.Contains(dir[2:], ":"):
		return bad("contains an alternate data stream")
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return bad("is not a valid path")
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		// A missing or unreadable directory is the caller's error to report, not a path-form refusal.
		return nil
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, 1024)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil || int(n) > len(buf) {
		return bad("cannot be resolved")
	}
	final := windows.UTF16ToString(buf[:n])
	if strings.HasPrefix(final, `\?\UNC\`) {
		return bad("is a network path")
	}
	root := final
	if strings.HasPrefix(root, `\?\`) {
		root = root[4:]
	}
	if len(root) >= 2 && root[1] == ':' {
		rp, err := windows.UTF16PtrFromString(root[:2] + `\`)
		if err == nil {
			switch windows.GetDriveType(rp) {
			case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE:
			default:
				return bad("is not on a local disk")
			}
		}
	}
	var fsName [64]uint16
	if err := windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &fsName[0], uint32(len(fsName))); err != nil {
		return bad("cannot be resolved")
	}
	if name := windows.UTF16ToString(fsName[:]); name != "NTFS" {
		return bad("is on a " + name + " file system, and only NTFS is supported")
	}
	return nil
}

// isReparsePoint reports a junction, symbolic link or any other reparse point.
func isReparsePoint(fi fs.FileInfo) bool {
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	return ok && d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
