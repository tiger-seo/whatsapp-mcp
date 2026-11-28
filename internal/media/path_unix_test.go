//go:build unix

package media

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// promptly fails a call that blocks, as an open of a FIFO without a writer would.
func promptly(t *testing.T, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the call blocked")
	}
}

func TestSendPathUnixFIFO(t *testing.T) {
	r := newPathRig(t)
	fifo := filepath.Join(r.outbox, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("not verified: cannot create a FIFO here: %v", err)
	}
	promptly(t, func() { r.refuse(t, fifo, errNotRegular) })
	file := filepath.Join(r.outbox, "file.txt")
	files := swapAfterResolve(t, func() {
		if err := os.Remove(file); err != nil {
			t.Error(err)
		}
		if err := syscall.Mkfifo(file, 0o600); err != nil {
			t.Error(err)
		}
	})
	promptly(t, func() { r.refuseWith(t, files, file, errNotRegular) })
}

func TestSendPathUnixSpecialFiles(t *testing.T) {
	r := newPathRig(t)
	symlink(t, "/dev/null", filepath.Join(r.outbox, "null"))
	r.refuse(t, filepath.Join(r.outbox, "null"), errOutsideDirs)
	socket := filepath.Join(r.outbox, "s")
	if fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0); err == nil {
		defer unix.Close(fd)
		if err := unix.Bind(fd, &unix.SockaddrUnix{Name: socket}); err == nil {
			r.refuse(t, socket, nil)
		} else {
			t.Logf("not verified: socket: %v", err)
		}
	}
}

func TestSendPathUnixDeviceNode(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("not verified: creating a device node needs root")
	}
	r := newPathRig(t)
	device := filepath.Join(r.outbox, "device")
	// The numbers of the null device.
	if err := unix.Mknod(device, unix.S_IFCHR|0o600, int(unix.Mkdev(1, 3))); err != nil {
		t.Skipf("not verified: cannot create a device node here: %v", err)
	}
	promptly(t, func() { r.refuse(t, device, nil) })
}
