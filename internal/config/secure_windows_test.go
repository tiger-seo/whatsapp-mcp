package config

import (
	"errors"
	"syscall"
)

func mkfifo(string) error { return errors.New("not supported") }

// windowsCmdLine passes line to the program as typed, without Go's argument quoting.
func windowsCmdLine(line string) *syscall.SysProcAttr { return &syscall.SysProcAttr{CmdLine: line} }
