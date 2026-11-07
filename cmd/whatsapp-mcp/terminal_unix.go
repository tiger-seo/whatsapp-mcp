//go:build linux || darwin

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

func terminalStreams(s *streams) bool {
	in, ok := s.stdin.(*os.File)
	if !ok {
		return false
	}
	out, ok := s.stdout.(*os.File)
	if !ok {
		return false
	}
	return isTerminal(in) && isTerminal(out)
}

func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), terminalIoctl)
	return err == nil
}
