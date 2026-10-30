//go:build !windows

package store

import "io/fs"

// checkPathForm has nothing to check on Unix: a network file system has no portable signal, and OPERATIONS.md
// lists it as unsupported.
func checkPathForm(string) error { return nil }

func isReparsePoint(fs.FileInfo) bool { return false }
