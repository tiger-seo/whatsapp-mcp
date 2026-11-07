//go:build windows

package main

import (
	"golang.org/x/sys/windows"
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
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(in.Fd()), &mode) == nil && windows.GetConsoleMode(windows.Handle(out.Fd()), &mode) == nil
}
