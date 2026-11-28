package media

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrNotExecutable refuses a converter path that is not a program that can be started directly.
var ErrNotExecutable = errors.New("is not an existing executable file")

// CheckExecutable checks that path names a program that starts without a shell: a regular .exe or .com file. A
// batch file would run through the command interpreter.
func CheckExecutable(path string) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".com":
	default:
		return ErrNotExecutable
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return ErrNotExecutable
	}
	return nil
}

// converterEnv is the converter's environment: only what a Windows program needs to start.
func converterEnv() []string { return []string{"SystemRoot=" + os.Getenv("SystemRoot")} }

// runTree runs cmd inside a job object that holds it and everything it starts. The program starts suspended, so it
// cannot start a child before it is in the job. Cancelling the command, and returning, end every process of the
// job: a launcher that does not pass a kill on cannot leave the real program running.
func runTree(cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	// Closing the last handle ends whatever is still in the job.
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED, HideWindow: true}
	cmd.Cancel = func() error {
		if err := windows.TerminateJobObject(job, 1); err != nil {
			return err
		}
		return cmd.Process.Kill()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := startInJob(job, uint32(cmd.Process.Pid)); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	err = cmd.Wait()
	_ = windows.TerminateJobObject(job, 1)
	waitJobEmpty(job)
	return err
}

// startInJob puts the suspended process into the job and then lets it run.
func startInJob(job windows.Handle, pid uint32) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return err
	}
	return resume(pid)
}

// resume resumes the threads of a process that was created suspended.
func resume(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := 0
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if err != nil {
			return err
		}
		resumed++
	}
	if resumed == 0 {
		return errors.New("the converter could not be resumed")
	}
	return nil
}

// jobAccounting is the part of the job's basic accounting that counts its live processes.
type jobAccounting struct {
	TotalUserTime, TotalKernelTime, PeriodUserTime, PeriodKernelTime int64
	PageFaults, TotalProcesses, ActiveProcesses, Terminated          uint32
}

// waitJobEmpty waits, for a bounded time, until the terminated processes are gone, so their files can be removed.
func waitJobEmpty(job windows.Handle) {
	for deadline := time.Now().Add(converterWait); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var info jobAccounting
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
		if err != nil || info.ActiveProcesses == 0 {
			return
		}
	}
}
