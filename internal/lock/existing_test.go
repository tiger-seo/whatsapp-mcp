package lock

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestAcquireExistingAbsentCreatesNothing(t *testing.T) {
	dir := privateDir(t)
	if _, err := AcquireExisting(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want fs.ErrNotExist", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("directory = %v, %v; the absent lock must not be created", entries, err)
	}
	if _, err := AcquireExisting(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing directory error = %v, want fs.ErrNotExist", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the missing directory was created")
	}
}

func TestAcquireExistingFreeLockRecordsPID(t *testing.T) {
	dir := privateDir(t)
	mustAcquire(t, dir).Release() // leaves an empty lock file behind, as a finished process does
	l, err := AcquireExisting(dir)
	if err != nil {
		t.Fatalf("AcquireExisting: %v", err)
	}
	t.Cleanup(func() { l.Release() })
	if got, want := readFrom(t, l.Path), strconv.Itoa(os.Getpid())+"\n"; got != want {
		t.Errorf("content from offset 1 = %q, want %q", got, want)
	}
	// The lock is really held: a normal acquisition is refused while status inspects.
	if _, err := Acquire(dir); err == nil {
		t.Error("Acquire succeeded while AcquireExisting held the lock")
	} else if he := heldError(t, err); he.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", he.PID, os.Getpid())
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	mustAcquire(t, dir)
}

func TestAcquireExistingReportsHolderWithoutChangingIt(t *testing.T) {
	dir := privateDir(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHolder$")
	cmd.Env = append(os.Environ(), "LOCK_HELPER_DIR="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("helper said %q, %v", line, err)
	}
	path := filepath.Join(dir, FileName)
	before := readFrom(t, path)
	_, err = AcquireExisting(dir)
	if he := heldError(t, err); he.PID != cmd.Process.Pid {
		t.Errorf("PID = %d, want the holder's %d", he.PID, cmd.Process.Pid)
	}
	if after := readFrom(t, path); after != before {
		t.Errorf("lock file changed from %q to %q; a refused attempt must not touch the holder's record", before, after)
	}
}

func TestAcquireExistingRefusesUnsafeFiles(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		target, unchanged := outside(t)
		dir := privateDir(t)
		if err := os.Symlink(target, filepath.Join(dir, FileName)); err != nil {
			t.Skipf("cannot create a symbolic link here: %v", err)
		}
		if _, err := AcquireExisting(dir); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("error = %v, want a symbolic link refused", err)
		}
		unchanged()
	})
	t.Run("dangling symlink", func(t *testing.T) {
		dir := privateDir(t)
		target := filepath.Join(t.TempDir(), "created")
		if err := os.Symlink(target, filepath.Join(dir, FileName)); err != nil {
			t.Skipf("cannot create a symbolic link here: %v", err)
		}
		if _, err := AcquireExisting(dir); err == nil {
			t.Error("AcquireExisting followed a dangling link")
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Error("the link target was created")
		}
	})
	t.Run("hard link", func(t *testing.T) {
		target, unchanged := outside(t)
		dir := privateDir(t)
		if err := os.Link(target, filepath.Join(dir, FileName)); err != nil {
			t.Skipf("cannot create a hard link here: %v", err)
		}
		if _, err := AcquireExisting(dir); err == nil || !strings.Contains(err.Error(), "more than one hard link") {
			t.Errorf("error = %v, want a hard link refused", err)
		}
		unchanged()
	})
	t.Run("directory", func(t *testing.T) {
		dir := privateDir(t)
		if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := AcquireExisting(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("error = %v, want not a regular file", err)
		}
	})
	t.Run("existing permissions are not repaired", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("permission bits are a Unix concept")
		}
		dir := privateDir(t)
		path := filepath.Join(dir, FileName)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		// Whether the file is accepted is Acquire's rule; AcquireExisting must follow it and
		// leave the mode alone either way.
		if l, err := AcquireExisting(dir); err == nil {
			l.Release()
		}
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o644 {
			t.Errorf("mode = %v, %v; the permissions must not be repaired", fi.Mode().Perm(), err)
		}
	})
}
