//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func startForSignal(cmd *exec.Cmd) {}

// sendStop sends SIGINT for "interrupt" and SIGTERM for "terminate".
func sendStop(p *os.Process, sig string) error {
	if sig == "terminate" {
		return p.Signal(syscall.SIGTERM)
	}
	return p.Signal(syscall.SIGINT)
}
