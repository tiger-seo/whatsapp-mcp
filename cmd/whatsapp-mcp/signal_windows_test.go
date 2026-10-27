//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startForSignal puts the child in its own process group, so that a console event reaches it and not
// the test process.
func startForSignal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// sendStop sends Ctrl+Break whatever sig is: a new process group ignores Ctrl+C, and Windows has no
// SIGTERM to send. Go delivers Ctrl+Break as os.Interrupt.
func sendStop(p *os.Process, sig string) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(p.Pid))
}
