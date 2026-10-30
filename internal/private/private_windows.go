package private

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// trusted holds the SIDs that may have access: this user, SYSTEM and the Administrators group.
type trusted struct {
	user, system, admins *windows.SID
	creatorOwner         *windows.SID
	ownerRights          *windows.SID
}

var trustedOnce = sync.OnceValues(func() (*trusted, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("cannot read this user's SID: %w", err)
	}
	user, err := tu.User.Sid.Copy()
	if err != nil {
		return nil, err
	}
	t := &trusted{user: user}
	for _, w := range []struct {
		dst  **windows.SID
		kind windows.WELL_KNOWN_SID_TYPE
	}{
		{&t.system, windows.WinLocalSystemSid},
		{&t.admins, windows.WinBuiltinAdministratorsSid},
		{&t.creatorOwner, windows.WinCreatorOwnerSid},
		{&t.ownerRights, windows.WinCreatorOwnerRightsSid},
	} {
		if *w.dst, err = windows.CreateWellKnownSid(w.kind); err != nil {
			return nil, err
		}
	}
	return t, nil
})

func (t *trusted) allows(sid *windows.SID) bool {
	return sid.Equals(t.user) || sid.Equals(t.system) || sid.Equals(t.admins) || sid.Equals(t.ownerRights)
}

func checkDir(dir string) error {
	h, err := openHandle(dir, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	switch {
	case info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		return errLink
	case info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0:
		return errNotDir
	}
	return checkSecurity(h, dir, true)
}

func mkdir(dir string) error {
	t, err := trustedOnce()
	if err != nil {
		return err
	}
	// A protected access list: nothing is inherited from the parent, and everything created inside inherits
	// these three entries.
	sddl := fmt.Sprintf("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;%s)", t.user)
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	if err := windows.CreateDirectory(p, &sa); err != nil {
		return &fs.PathError{Op: "mkdir", Path: dir, Err: err}
	}
	return nil
}

func openFile(path string, flag int) (*os.File, error) {
	access := uint32(windows.GENERIC_READ)
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
		access |= windows.GENERIC_WRITE
	}
	disposition := uint32(windows.OPEN_EXISTING)
	if flag&os.O_CREATE != 0 {
		disposition = windows.OPEN_ALWAYS
	}
	h, err := openHandle(path, access, disposition, windows.FILE_ATTRIBUTE_NORMAL)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

// openHandle opens path itself, never the target of a symbolic link or junction.
func openHandle(path string, access, disposition, attrs uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	h, err := windows.CreateFile(p, access, share, nil, disposition, attrs|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	return h, nil
}

func checkFile(f *os.File) error {
	h := windows.Handle(f.Fd())
	if t, err := windows.GetFileType(h); err != nil || t != windows.FILE_TYPE_DISK {
		return errIrreg
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	switch {
	case info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		return errLinkF
	case info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0:
		return errIrreg
	case info.NumberOfLinks > 1:
		return errHardLnk
	}
	return checkSecurity(h, f.Name(), false)
}

// checkSecurity checks the owner and the access list of an open handle.
func checkSecurity(h windows.Handle, path string, dir bool) error {
	t, err := trustedOnce()
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("cannot read its access list: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("cannot read its owner: %w", err)
	}
	if !t.allows(owner) {
		return &Error{
			Problem: fmt.Sprintf("is owned by %s, not by this user", sidName(owner)),
			Fix:     "use one this user created, or take ownership with: takeown /f " + cmdQuote(path),
		}
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("cannot read its access list: %w", err)
	}
	if dacl == nil {
		return &Error{Problem: "has no access list, so everyone can access it", Fix: icaclsFix(path, dir, t, nil)}
	}
	var others []*windows.SID
	var unknown bool
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("cannot read its access list: %w", err)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE, aceDeniedObject, aceDeniedCallback, aceDeniedCallbackObject:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE, aceAllowedCallback:
		default:
			// Object entries have a different layout, and do not occur on local files.
			unknown = true
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if t.allows(sid) {
			continue
		}
		// CREATOR OWNER only shapes what children inherit: it becomes their creator, this user.
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 && sid.Equals(t.creatorOwner) {
			continue
		}
		if !containsSID(others, sid) {
			c, err := sid.Copy()
			if err != nil {
				return err
			}
			others = append(others, c)
		}
	}
	if len(others) == 0 && !unknown {
		return nil
	}
	names := make([]string, 0, len(others)+1)
	for _, sid := range others {
		names = append(names, sidName(sid))
	}
	if unknown {
		names = append(names, "an unsupported access entry")
	}
	return &Error{
		Problem: "other users can access it: " + strings.Join(names, ", "),
		Fix:     icaclsFix(path, dir, t, others),
	}
}

// Access entry types that x/sys/windows does not name.
const (
	aceDeniedObject         = 0x6
	aceAllowedCallback      = 0x9
	aceDeniedCallback       = 0xA
	aceDeniedCallbackObject = 0xC
)

func containsSID(list []*windows.SID, sid *windows.SID) bool {
	for _, s := range list {
		if s.Equals(sid) {
			return true
		}
	}
	return false
}

// icaclsFix is the icacls command that leaves only the trusted entries. It names SIDs rather than accounts,
// whose names depend on the Windows language.
func icaclsFix(path string, dir bool, t *trusted, others []*windows.SID) string {
	inherit := ""
	if dir {
		inherit = "(OI)(CI)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run: icacls %s /inheritance:r", cmdQuote(path))
	for _, sid := range []*windows.SID{t.user, t.system, t.admins} {
		fmt.Fprintf(&b, " /grant:r *%s:%sF", sid, inherit)
	}
	for _, sid := range others {
		fmt.Fprintf(&b, " /remove:g *%s", sid)
	}
	return b.String()
}

func sidName(sid *windows.SID) string {
	account, domain, _, err := sid.LookupAccount("")
	switch {
	case err != nil:
		return sid.String()
	case domain != "":
		return domain + `\` + account
	}
	return account
}

func cmdQuote(s string) string {
	if strings.ContainsAny(s, " &()[]{}^=;!'+,`~") {
		return `"` + s + `"`
	}
	return s
}

func isReparse(fi fs.FileInfo) bool {
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	return ok && d.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// checkFileStrict has nothing to add on Windows: the access-list rule is already checkFile's.
func checkFileStrict(*os.File) error { return nil }

func fileID(f *os.File) (FileID, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return FileID{}, err
	}
	return FileID{A: uint64(info.VolumeSerialNumber), B: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)}, nil
}
