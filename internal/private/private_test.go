package private

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// privateTemp returns a new private directory for one test.
func privateTemp(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "p")
	if err := MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return dir
}

func wantError(t *testing.T, err, want error) {
	t.Helper()
	if err != want {
		t.Errorf("error = %v, want %v", err, want)
	}
}

// sentinel writes a file outside the directory under test, for checking that nothing touched it.
func sentinel(t *testing.T) (path string, check func()) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "sentinel")
	const content = "do not touch\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, func() {
		t.Helper()
		if data, err := os.ReadFile(path); err != nil || string(data) != content {
			t.Errorf("sentinel = %q, %v; want it unchanged", data, err)
		}
	}
}

func TestMkdirAllNested(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "nested")
	if err := MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := CheckDir(dir); err != nil {
		t.Errorf("CheckDir after MkdirAll: %v", err)
	}
	if err := MkdirAll(dir); err != nil {
		t.Errorf("second MkdirAll: %v", err)
	}
}

func TestCheckDirMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	if err := CheckDir(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want one matching fs.ErrNotExist", err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CheckDir created %s", dir)
	}
	if err := CheckDir(filepath.Join(dir, "deeper")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing parent: error = %v, want one matching fs.ErrNotExist", err)
	}
}

func TestCheckDirOnFile(t *testing.T) {
	path := filepath.Join(privateTemp(t), "afile")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	wantError(t, CheckDir(path), errNotDir)
	wantError(t, MkdirAll(path), errNotDir)
	if err := CheckDir(filepath.Join(path, "below")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("below a file: error = %v, want one matching fs.ErrNotExist", err)
	}
}

func TestCheckDirSymlink(t *testing.T) {
	root := privateTemp(t)
	target := filepath.Join(root, "target")
	if err := MkdirAll(target); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	wantError(t, CheckDir(link), errLink)
	wantError(t, MkdirAll(link), errLink)
}

func TestOpenFileCreates(t *testing.T) {
	path := filepath.Join(privateTemp(t), "f")
	f, err := OpenFile(path, os.O_RDWR|os.O_CREATE)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("x"); err != nil {
		t.Error(err)
	}
	f.Close()
	f, err = OpenFile(path, os.O_RDONLY)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	f.Close()
}

func TestOpenFileMissing(t *testing.T) {
	path := filepath.Join(privateTemp(t), "f")
	if _, err := OpenFile(path, os.O_RDONLY); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want one matching fs.ErrNotExist", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenFile without O_CREATE created %s", path)
	}
}

func TestOpenFileSymlink(t *testing.T) {
	target, check := sentinel(t)
	link := filepath.Join(privateTemp(t), "f")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	for _, flag := range []int{os.O_RDONLY, os.O_RDWR | os.O_CREATE} {
		if f, err := OpenFile(link, flag); err != errLinkF {
			if f != nil {
				f.Close()
			}
			t.Errorf("flag %#x: error = %v, want %v", flag, err, errLinkF)
		}
	}
	check()
}

func TestOpenFileDanglingSymlink(t *testing.T) {
	root := privateTemp(t)
	target := filepath.Join(t.TempDir(), "would-be-created")
	link := filepath.Join(root, "f")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	if _, err := OpenFile(link, os.O_RDWR|os.O_CREATE); err != errLinkF {
		t.Errorf("error = %v, want %v", err, errLinkF)
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link target was created")
	}
}

func TestOpenFileHardLink(t *testing.T) {
	target, check := sentinel(t)
	link := filepath.Join(privateTemp(t), "f")
	if err := os.Link(target, link); err != nil {
		t.Skipf("cannot create a hard link here: %v", err)
	}
	if _, err := OpenFile(link, os.O_RDWR); err != errHardLnk {
		t.Errorf("error = %v, want %v", err, errHardLnk)
	}
	check()
}

func TestOpenFileDirectory(t *testing.T) {
	dir := filepath.Join(privateTemp(t), "d")
	if err := MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(dir, os.O_RDWR|os.O_CREATE); err != errIrreg {
		t.Errorf("error = %v, want %v", err, errIrreg)
	}
}

func TestErrorText(t *testing.T) {
	if got := (&Error{Problem: "p", Fix: "run: x"}).Error(); got != "p; run: x" {
		t.Errorf("got %q", got)
	}
	if got := (&Error{Problem: "p"}).Error(); got != "p" {
		t.Errorf("got %q", got)
	}
}

func TestCheckFileIdentity(t *testing.T) {
	dir := privateTemp(t)
	a := filepath.Join(dir, "a")
	if err := os.WriteFile(a, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	id1, err := CheckFile(a)
	if err != nil {
		t.Fatalf("CheckFile: %v", err)
	}
	id2, err := CheckFile(a)
	if err != nil || id1 != id2 {
		t.Errorf("second CheckFile = %v, %v; want %v", id2, err, id1)
	}
	f, err := os.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if id3, err := FileIDOf(f); err != nil || id3 != id1 {
		t.Errorf("FileIDOf = %v, %v; want %v", id3, err, id1)
	}
	// A different file renamed over it has another identity.
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(b, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Rename(b, a); err != nil {
		t.Fatal(err)
	}
	if id4, err := CheckFile(a); err != nil || id4 == id1 {
		t.Errorf("after rename CheckFile = %v, %v; want an identity different from %v", id4, err, id1)
	}
}

func TestCheckFileRefusals(t *testing.T) {
	dir := privateTemp(t)
	if _, err := CheckFile(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: %v, want ErrNotExist", err)
	}
	if _, err := CheckFile(dir); err != errIrreg {
		t.Errorf("directory: %v, want %v", err, errIrreg)
	}
	a := filepath.Join(dir, "a")
	if err := os.WriteFile(a, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, filepath.Join(dir, "hard")); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckFile(a); err != errHardLnk {
		t.Errorf("hard link: %v, want %v", err, errHardLnk)
	}
	link := filepath.Join(dir, "sym")
	if err := os.Symlink(a, link); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	if _, err := CheckFile(link); err != errLinkF {
		t.Errorf("symlink: %v, want %v", err, errLinkF)
	}
}
