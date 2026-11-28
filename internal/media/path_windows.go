package media

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// openNonblock has no counterpart on Windows, where opening a file never waits for another process.
const openNonblock = 0

// linkCount is the number of names the open file has.
func linkCount(f *os.File) (uint64, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return 0, err
	}
	return uint64(info.NumberOfLinks), nil
}

// cutRoot accepts only a path that starts with a drive letter and a separator, which excludes UNC, device and
// \\?\ paths, and refuses any further colon, which names an alternate data stream.
func cutRoot(p string) (string, bool) {
	if len(p) < 3 || p[1] != ':' || !isLetter(p[0]) || !isSeparator(p[2]) {
		return "", false
	}
	rest := p[3:]
	return rest, !strings.Contains(rest, ":")
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isSeparator(c byte) bool { return c == '\\' || c == '/' }

func splitComponents(rest string) []string {
	return strings.Split(strings.ReplaceAll(rest, "/", `\`), `\`)
}
