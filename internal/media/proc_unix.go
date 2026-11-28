//go:build unix

package media

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// ErrNotExecutable refuses a converter path that is not a program that can be started directly.
var ErrNotExecutable = errors.New("is not an existing executable file")

// CheckExecutable checks that path names a regular file with an execute permission bit.
func CheckExecutable(path string) error {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return ErrNotExecutable
	}
	return nil
}

// converterEnv is the converter's environment, which is empty.
func converterEnv() []string { return []string{} }

// runTree runs cmd in a process group of its own, which holds it and everything it starts. Cancelling the command,
// and returning, kill the whole group, so a launcher that does not pass a kill on cannot leave the real program
// running.
func runTree(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		return err
	}
	group := cmd.Process.Pid
	err := cmd.Wait()
	// The group lives on while any member does. Kill the rest and wait, for a bounded time, until it is gone.
	for deadline := time.Now().Add(converterWait); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if syscall.Kill(-group, syscall.SIGKILL) != nil {
			break
		}
	}
	return err
}
