//go:build unix && !darwin

package private

import "os"

// Linux needs no separate ACL check: with a POSIX ACL the group bits of the mode are the mask, so a 0600 or 0700
// mode leaves every named entry without effective permissions.
func checkACL(*os.File) error { return nil }

func checkDirACL(string) error { return nil }
