package store

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func walAllocatedBytes(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	// FILE_STANDARD_INFO: two LARGE_INTEGERs, one DWORD, two BOOLEANs.
	var info struct {
		allocationSize, endOfFile int64
		links                     uint32
		deletePending, directory  byte
	}
	err = windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileStandardInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return info.allocationSize, err
}
