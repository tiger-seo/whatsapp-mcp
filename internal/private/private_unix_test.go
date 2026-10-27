//go:build unix

package private

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMkdirAllMode(t *testing.T) {
	dir := privateTemp(t)
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, %v; want 0700", fi.Mode().Perm(), err)
	}
}

func TestCheckDirOpenMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o750, 0o701, 0o777} {
		dir := filepath.Join(privateTemp(t), "open")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		var pe *Error
		err := CheckDir(dir)
		if !errors.As(err, &pe) || !strings.Contains(err.Error(), "other users can access it") || pe.Fix != "run: chmod 700 "+dir {
			t.Errorf("mode %04o: error = %v, want other users can access it; run: chmod 700 %s", mode, err, dir)
		}
		// MkdirAll does not change an existing directory either.
		if err := MkdirAll(dir); err == nil {
			t.Errorf("mode %04o: MkdirAll accepted it", mode)
		}
		if fi, _ := os.Stat(dir); fi.Mode().Perm() != mode {
			t.Errorf("mode changed to %04o", fi.Mode().Perm())
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := CheckDir(dir); err != nil {
			t.Errorf("after the fix: %v", err)
		}
	}
}

func TestCheckDirWritableParent(t *testing.T) {
	parent := privateTemp(t)
	dir := filepath.Join(parent, "data")
	if err := MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := CheckDir(dir); err == nil || !strings.Contains(err.Error(), "can be written by other users") {
		t.Errorf("error = %v, want its parent can be written by other users", err)
	}
	// A sticky parent, like /tmp, lets nobody else rename or remove the directory.
	if err := os.Chmod(parent, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if err := CheckDir(dir); err != nil {
		t.Errorf("sticky parent: %v", err)
	}
}

func TestOpenFileOpenMode(t *testing.T) {
	path := filepath.Join(privateTemp(t), "f")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o620, 0o602, 0o666} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenFile(path, os.O_RDONLY); err == nil || !strings.Contains(err.Error(), "other users can write it") {
			t.Errorf("mode %04o: error = %v, want other users can write it", mode, err)
		}
	}
	// Reading is harmless: the private directory already keeps other users out.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := OpenFile(path, os.O_RDONLY)
	if err != nil {
		t.Fatalf("mode 0644: %v", err)
	}
	f.Close()
}

func TestOpenFileCreateMode(t *testing.T) {
	path := filepath.Join(privateTemp(t), "f")
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	f, err := OpenFile(path, os.O_RDWR|os.O_CREATE)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %04o, want 0600", fi.Mode().Perm())
	}
}

func TestOpenFileFIFO(t *testing.T) {
	path := filepath.Join(privateTemp(t), "f")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		f, err := OpenFile(path, os.O_RDONLY)
		if f != nil {
			f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != errIrreg {
			t.Errorf("error = %v, want %v", err, errIrreg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OpenFile blocked on a FIFO")
	}
}

func TestOpenFileDevice(t *testing.T) {
	link := filepath.Join(privateTemp(t), "f")
	if err := os.Symlink("/dev/zero", link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(link, os.O_RDONLY); err != errLinkF {
		t.Errorf("link to /dev/zero: error = %v, want %v", err, errLinkF)
	}
	if _, err := OpenFile("/dev/zero", os.O_RDONLY); err != errIrreg {
		t.Errorf("/dev/zero: error = %v, want %v", err, errIrreg)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/home/me/.config/whatsapp-mcp": "/home/me/.config/whatsapp-mcp",
		"/tmp/a b":                      "'/tmp/a b'",
		"/tmp/it's":                     `'/tmp/it'\''s'`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
