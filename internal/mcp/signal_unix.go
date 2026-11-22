//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package mcp

import (
	"os/signal"
	"syscall"
)

// SetupDiagnosticSignals keeps broken pipes from terminating the process before cleanup.
func SetupDiagnosticSignals() { signal.Ignore(syscall.SIGPIPE) }
