//go:build windows

package wa

import (
	"syscall"
	"unsafe"
)

// processRSS returns the working set of this process in bytes, or -1 when it cannot be read.
func processRSS() int64 {
	type counters struct {
		cb                         uint32
		PageFaultCount             uint32
		PeakWorkingSetSize         uintptr
		WorkingSetSize             uintptr
		QuotaPeakPagedPoolUsage    uintptr
		QuotaPagedPoolUsage        uintptr
		QuotaPeakNonPagedPoolUsage uintptr
		QuotaNonPagedPoolUsage     uintptr
		PagefileUsage              uintptr
		PeakPagefileUsage          uintptr
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	getProcess := kernel.NewProc("GetCurrentProcess")
	info := kernel.NewProc("K32GetProcessMemoryInfo")
	handle, _, _ := getProcess.Call()
	var c counters
	c.cb = uint32(unsafe.Sizeof(c))
	if ok, _, _ := info.Call(handle, uintptr(unsafe.Pointer(&c)), uintptr(c.cb)); ok == 0 {
		return -1
	}
	return int64(c.WorkingSetSize)
}
