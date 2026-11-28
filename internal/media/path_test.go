package media

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pathRig is two allowed directories beside things that must never be reached.
type pathRig struct {
	base, outbox, second, outside string
	dirs                          []AllowedDir
}

const secretContent = "outside the allowed directories"

func newPathRig(t *testing.T) *pathRig {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &pathRig{base: base, outbox: filepath.Join(base, "outbox"), second: filepath.Join(base, "second"), outside: filepath.Join(base, "outside")}
	for _, dir := range []string{r.outbox, filepath.Join(r.outbox, "sub"), r.second, r.outside, r.outbox + "-evil"} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r.write(t, filepath.Join(r.outbox, "file.txt"), "inside")
	r.write(t, filepath.Join(r.outbox, "sub", "nested.txt"), "nested")
	r.write(t, filepath.Join(r.second, "other.txt"), "second")
	r.write(t, filepath.Join(r.outside, "secret.txt"), secretContent)
	r.write(t, filepath.Join(r.outbox+"-evil", "secret.txt"), secretContent)
	r.dirs = []AllowedDir{openAllowed(t, r.outbox), openAllowed(t, r.second)}
	return r
}

func (r *pathRig) write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// accept opens a path that must be allowed and returns its content.
func (r *pathRig) accept(t *testing.T, path, want string) {
	t.Helper()
	source, err := openSource(systemFiles(), r.dirs, path)
	if err != nil {
		t.Errorf("%q refused: %v", path, err)
		return
	}
	defer source.file.Close()
	got, err := io.ReadAll(source.file)
	if err != nil || string(got) != want || source.size != int64(len(want)) {
		t.Errorf("%q = %q (%d), %v", path, got, source.size, err)
	}
}

// refuse checks that a path is refused and that nothing was opened.
func (r *pathRig) refuse(t *testing.T, path string, want error) {
	t.Helper()
	r.refuseWith(t, systemFiles(), path, want)
}

func (r *pathRig) refuseWith(t *testing.T, files fileAccess, path string, want error) {
	t.Helper()
	source, err := openSource(files, r.dirs, path)
	if err == nil {
		content := make([]byte, 64)
		n, _ := source.file.Read(content)
		source.file.Close()
		t.Errorf("%q accepted; read %q", path, content[:n])
		return
	}
	if want != nil && !errors.Is(err, want) {
		t.Errorf("%q refused with %v, want %v", path, err, want)
	}
	if source.file != nil {
		t.Errorf("%q: a handle was returned with the refusal", path)
	}
	if strings.Contains(err.Error(), r.base) {
		t.Errorf("the refusal shows a path: %v", err)
	}
}

// symlink creates a link, or skips the test where that needs a privilege this run does not have.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("not verified: cannot create a symbolic link here: %v", err)
	}
}

func TestSendPathAccepted(t *testing.T) {
	r := newPathRig(t)
	r.accept(t, filepath.Join(r.outbox, "file.txt"), "inside")
	r.accept(t, filepath.Join(r.outbox, "sub", "nested.txt"), "nested")
	r.accept(t, filepath.Join(r.second, "other.txt"), "second")
	r.accept(t, r.outbox+string(filepath.Separator)+"sub"+string(filepath.Separator)+".."+string(filepath.Separator)+"file.txt", "inside")
}

func TestSendPathRefused(t *testing.T) {
	r := newPathRig(t)
	sep := string(filepath.Separator)
	big := filepath.Join(r.outbox, "big.bin")
	r.write(t, big, "")
	r.refuse(t, big, errEmptyFile)
	if err := os.Truncate(big, maxFileSize); err != nil {
		t.Fatal(err)
	}
	if source, err := openSource(systemFiles(), r.dirs, big); err != nil {
		t.Errorf("a file of exactly 100 MiB: %v", err)
	} else {
		source.file.Close()
	}
	if err := os.Truncate(big, maxFileSize+1); err != nil {
		t.Fatal(err)
	}
	r.refuse(t, big, errTooLarge)
	for path, want := range map[string]error{
		"":                   errPathRelative,
		"file.txt":           errPathRelative,
		"." + sep + "outbox": errPathRelative,
		r.outbox + sep + ".." + sep + "outside" + sep + "secret.txt": errOutsideDirs,
		r.outbox + "-evil" + sep + "secret.txt":                      errOutsideDirs,
		filepath.Join(r.outside, "secret.txt"):                       errOutsideDirs,
		filepath.Join(r.outbox, "missing.txt"):                       errUnresolved,
		filepath.Join(r.outbox, "sub"):                               errNotRegular,
		r.outbox:                                                     errOutsideDirs,
		filepath.Join(r.outbox, "file.txt") + sep:                    errPathRelative,
		r.outbox + sep + sep + "file.txt":                            errPathRelative,
		filepath.Join(r.outbox, "file\x00.txt"):                      errPathRelative,
		filepath.Join(r.outbox, strings.Repeat("a", 5000)):           errPathRelative,
	} {
		r.refuse(t, path, want)
	}
}

func TestSendPathHardLink(t *testing.T) {
	r := newPathRig(t)
	link := filepath.Join(r.outbox, "hard.txt")
	if err := os.Link(filepath.Join(r.outside, "secret.txt"), link); err != nil {
		t.Skipf("not verified: cannot create a hard link here: %v", err)
	}
	r.refuse(t, link, errHardLinked)
}

func TestSendPathSymlinks(t *testing.T) {
	r := newPathRig(t)
	symlink(t, filepath.Join(r.outside, "secret.txt"), filepath.Join(r.outbox, "escape.txt"))
	symlink(t, r.outside, filepath.Join(r.outbox, "escapedir"))
	symlink(t, filepath.Join(r.outbox, "file.txt"), filepath.Join(r.outbox, "absolute.txt"))
	symlink(t, "file.txt", filepath.Join(r.outbox, "relative.txt"))
	symlink(t, filepath.Join(r.second, "other.txt"), filepath.Join(r.outbox, "crossing.txt"))
	symlink(t, r.outbox, filepath.Join(r.base, "alias"))
	symlink(t, filepath.Join(r.outbox, "file.txt"), filepath.Join(r.outside, "inward.txt"))
	r.refuse(t, filepath.Join(r.outbox, "escape.txt"), errOutsideDirs)
	r.refuse(t, filepath.Join(r.outbox, "escapedir", "secret.txt"), errOutsideDirs)
	r.accept(t, filepath.Join(r.outbox, "absolute.txt"), "inside")
	r.accept(t, filepath.Join(r.outbox, "relative.txt"), "inside")
	// A link from one allowed directory into another is accepted, and so is a path through a link to a directory.
	r.accept(t, filepath.Join(r.outbox, "crossing.txt"), "second")
	r.accept(t, filepath.Join(r.base, "alias", "file.txt"), "inside")
	r.accept(t, filepath.Join(r.outside, "inward.txt"), "inside")
}

// swapAfterResolve changes the file system between the resolution and the confined open.
func swapAfterResolve(t *testing.T, swap func()) fileAccess {
	files := systemFiles()
	resolve := files.resolve
	files.resolve = func(path string) (string, error) {
		resolved, err := resolve(path)
		swap()
		return resolved, err
	}
	return files
}

func TestSendPathDirectorySwappedForALink(t *testing.T) {
	r := newPathRig(t)
	symlink(t, r.outside, filepath.Join(r.base, "probe"))
	r.write(t, filepath.Join(r.outbox, "sub", "secret.txt"), "inside")
	files := swapAfterResolve(t, func() {
		if err := os.Rename(filepath.Join(r.outbox, "sub"), filepath.Join(r.base, "moved")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(r.outside, filepath.Join(r.outbox, "sub")); err != nil {
			t.Fatal(err)
		}
	})
	r.refuseWith(t, files, filepath.Join(r.outbox, "sub", "secret.txt"), errNotOpened)
}

// A root swapped after startup does not redirect an open, because the handle opened at startup is used. The test
// records whether the platform lets the directory be renamed at all while it is open.
func TestSendPathKeptRoot(t *testing.T) {
	r := newPathRig(t)
	moved := filepath.Join(r.base, "moved-outbox")
	if err := os.Rename(r.outbox, moved); err != nil {
		t.Logf("an allowed directory cannot be renamed while its root is open: %v", err)
		return
	}
	t.Log("an allowed directory can be renamed while its root is open")
	if err := os.Mkdir(r.outbox, 0o700); err != nil {
		t.Fatal(err)
	}
	r.write(t, filepath.Join(r.outbox, "file.txt"), "replacement")
	r.write(t, filepath.Join(r.outbox, "only-new.txt"), "replacement")
	// The path names the new directory, but the open goes through the kept handle of the old one.
	r.accept(t, filepath.Join(r.outbox, "file.txt"), "inside")
	r.refuse(t, filepath.Join(r.outbox, "only-new.txt"), errNotOpened)
	if err := os.RemoveAll(r.outbox); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(r.outside, r.outbox); err != nil {
		t.Logf("not verified: the swap for a link needs a privilege: %v", err)
		return
	}
	r.refuse(t, filepath.Join(r.outbox, "secret.txt"), nil)
}

func TestSendPathDeletedRoot(t *testing.T) {
	r := newPathRig(t)
	if err := os.RemoveAll(r.second); err != nil {
		t.Logf("an allowed directory cannot be deleted while its root is open: %v", err)
		return
	}
	t.Log("an allowed directory can be deleted while its root is open")
	if _, err := os.Stat(r.second); err == nil {
		t.Log("the deleted directory still shows while the handle is open")
	}
}

func TestSendPathCountingCopy(t *testing.T) {
	for name, tt := range map[string]struct {
		content string
		size    int64
		want    error
	}{
		"exact": {"abcd", 4, nil}, "grown": {"abcde", 4, errFileChanged}, "shrunk": {"abc", 4, errFileChanged},
	} {
		var out strings.Builder
		err := copyChecked(t.Context(), &out, strings.NewReader(tt.content), tt.size)
		if !errors.Is(err, tt.want) || (tt.want == nil && out.String() != tt.content) || int64(out.Len()) > tt.size {
			t.Errorf("%s: %v, wrote %q", name, err, out.String())
		}
	}
	// A reader that never ends is read no further than one byte past the limit.
	var read int64
	endless := readerFunc(func(p []byte) (int, error) { read += int64(len(p)); return len(p), nil })
	if err := copyChecked(t.Context(), io.Discard, endless, maxFileSize); !errors.Is(err, errFileChanged) || read > maxFileSize+1 {
		t.Errorf("endless source: %v after %d bytes", err, read)
	}
}

func FuzzSyntacticPath(f *testing.F) {
	for _, seed := range []string{"/a/b", `C:\a\b`, `\\server\share`, `\\?\C:\a`, "a", "/a//b", `C:\a:s`, "//x", `C:a`, "/", `C:\`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if CheckPathSyntax(path) != nil {
			return
		}
		if !filepath.IsAbs(path) || len(path) > MaxPathBytes || strings.IndexByte(path, 0) >= 0 {
			t.Errorf("accepted %q", path)
		}
		volume := filepath.VolumeName(path)
		if volume != "" && (len(volume) != 2 || volume[1] != ':' || strings.Contains(path[2:], ":")) {
			t.Errorf("accepted a UNC, device or stream path %q", path)
		}
	})
}
