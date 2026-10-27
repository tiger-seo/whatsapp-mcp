package lock

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

// privateDir returns a new, existing data directory that only this user can reach. A bare t.TempDir() may
// inherit wider access, as on Windows when the temporary directory is outside the user's profile.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := private.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustAcquire(t *testing.T, dir string) *Lock {
	t.Helper()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	t.Cleanup(func() { l.Release() })
	return l
}

// readFrom reads the lock file from offset 1, which works while another handle holds the lock.
func readFrom(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, err := f.ReadAt(buf, 1)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	return string(buf[:n])
}

func heldError(t *testing.T, err error) *HeldError {
	t.Helper()
	var he *HeldError
	if !errors.As(err, &he) {
		t.Fatalf("error = %v, want a *HeldError", err)
	}
	return he
}

func TestAcquireWritesPID(t *testing.T) {
	dir := privateDir(t)
	l := mustAcquire(t, dir)
	if l.Path != filepath.Join(dir, FileName) {
		t.Errorf("Path = %q", l.Path)
	}
	if got, want := readFrom(t, l.Path), strconv.Itoa(os.Getpid())+"\n"; got != want {
		t.Errorf("content from offset 1 = %q, want %q", got, want)
	}
}

func TestSecondAcquireFails(t *testing.T) {
	dir := privateDir(t)
	l := mustAcquire(t, dir)
	start := time.Now()
	_, err := Acquire(dir)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("second Acquire took %v; it must not wait", elapsed)
	}
	he := heldError(t, err)
	if he.PID != os.Getpid() || he.DataDir != dir || he.Path != l.Path {
		t.Errorf("HeldError = %+v, want PID %d and dir %s", he, os.Getpid(), dir)
	}
}

func TestReleaseThenAcquire(t *testing.T) {
	dir := privateDir(t)
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Errorf("second Release: %v", err)
	}
	if data, err := os.ReadFile(l.Path); err != nil || len(data) != 0 {
		t.Errorf("after Release: %q, %v; want an empty file", data, err)
	}
	mustAcquire(t, dir)
}

func TestStaleContentIgnored(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("\x00junk that is longer than any pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := mustAcquire(t, dir)
	if got, want := readFrom(t, l.Path), strconv.Itoa(os.Getpid())+"\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestUnknownPID(t *testing.T) {
	dir := privateDir(t)
	fl := flock.New(filepath.Join(dir, FileName), flock.SetFlag(os.O_CREATE|os.O_RDWR))
	if ok, err := fl.TryLock(); !ok || err != nil {
		t.Fatalf("TryLock: %v, %v", ok, err)
	}
	defer fl.Close()
	if he := heldError(t, func() error { _, err := Acquire(dir); return err }()); he.PID != 0 {
		t.Errorf("PID = %d, want 0", he.PID)
	}
}

func TestHeldErrorMessage(t *testing.T) {
	if got := (&HeldError{DataDir: "/d", PID: 4242}).Error(); got != "another whatsapp-mcp (PID 4242) is using the data directory /d" {
		t.Errorf("got %q", got)
	}
	if got := (&HeldError{DataDir: "/d"}).Error(); got != "another whatsapp-mcp (PID unknown) is using the data directory /d" {
		t.Errorf("got %q", got)
	}
}

// TestHelperHolder is not a test: the cross-process test runs it in a child process to hold the lock.
func TestHelperHolder(t *testing.T) {
	dir := os.Getenv("LOCK_HELPER_DIR")
	if dir == "" {
		t.Skip("helper process only")
	}
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	os.Stdout.WriteString("ready\n")
	io.Copy(io.Discard, os.Stdin)
}

func TestOtherProcess(t *testing.T) {
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
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "ready\n" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("helper said %q, %v", line, err)
	}

	_, err = Acquire(dir)
	if he := heldError(t, err); he.PID != cmd.Process.Pid {
		t.Errorf("PID = %d, want the helper's %d", he.PID, cmd.Process.Pid)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	mustAcquire(t, dir)
}

// outside writes a file outside the data directory, and returns a check that it was left alone.
func outside(t *testing.T) (path string, unchanged func()) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "outside")
	const content = "precious\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, func() {
		t.Helper()
		if data, err := os.ReadFile(path); err != nil || string(data) != content {
			t.Errorf("file outside = %q, %v; want it unchanged", data, err)
		}
	}
}

func TestSymlinkRefused(t *testing.T) {
	target, unchanged := outside(t)
	dir := privateDir(t)
	if err := os.Symlink(target, filepath.Join(dir, FileName)); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	if _, err := Acquire(dir); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("error = %v, want a symbolic link refused", err)
	}
	unchanged()
}

func TestDanglingSymlinkRefused(t *testing.T) {
	dir := privateDir(t)
	target := filepath.Join(t.TempDir(), "created")
	if err := os.Symlink(target, filepath.Join(dir, FileName)); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	if _, err := Acquire(dir); err == nil {
		t.Error("Acquire followed a dangling link")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Error("the link target was created")
	}
}

func TestHardLinkRefused(t *testing.T) {
	target, unchanged := outside(t)
	dir := privateDir(t)
	if err := os.Link(target, filepath.Join(dir, FileName)); err != nil {
		t.Skipf("cannot create a hard link here: %v", err)
	}
	if _, err := Acquire(dir); err == nil || !strings.Contains(err.Error(), "more than one hard link") {
		t.Errorf("error = %v, want a hard link refused", err)
	}
	unchanged()
}

func TestDirectoryRefused(t *testing.T) {
	dir := privateDir(t)
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error = %v, want not a regular file", err)
	}
}

func TestSameFile(t *testing.T) {
	dir := privateDir(t)
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for path, want := range map[string]error{a: nil, b: errReplaced} {
		fl := flock.New(path, flock.SetFlag(os.O_RDWR))
		if ok, err := fl.TryLock(); !ok || err != nil {
			t.Fatalf("TryLock: %v, %v", ok, err)
		}
		if err := sameFile(f, fl); err != want {
			t.Errorf("%s: sameFile = %v, want %v", filepath.Base(path), err, want)
		}
		fl.Close()
	}
}
