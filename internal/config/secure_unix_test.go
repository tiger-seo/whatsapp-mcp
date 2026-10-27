//go:build unix

package config

import "syscall"

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o600) }

func windowsCmdLine(string) *syscall.SysProcAttr { return nil }
