package media

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxFileSize is the largest file that is sent, and the largest output of a conversion.
const maxFileSize = 100 << 20

// ErrPathSyntax refuses a path that is not a plain absolute local path.
var ErrPathSyntax = errors.New("must be an absolute path of at most 4096 bytes on a local drive, with no empty component, and not a UNC, device or alternate data stream path")

// AllowedDir is one allowed directory: its real path and the handle opened at startup. Every file is opened
// through the handle, so a directory swapped for a link afterwards does not redirect an open.
type AllowedDir struct {
	Path string
	Root *os.Root
}

// copyChunk is how much is copied between two looks at the call's context.
const copyChunk = 1 << 20

var (
	errOutsideDirs  = errors.New("media_path is outside every directory in send_allowed_dirs")
	errUnresolved   = errors.New("media_path does not exist or cannot be resolved (a path through a junction is refused)")
	errNotOpened    = errors.New("media_path could not be opened inside its directory in send_allowed_dirs")
	errNotRegular   = errors.New("media_path is not a regular file")
	errHardLinked   = errors.New("media_path has more than one hard link")
	errEmptyFile    = errors.New("media_path is empty")
	errTooLarge     = errors.New("media_path is larger than 100 MiB")
	errFileChanged  = errors.New("the file changed while it was read")
	errPathRelative = errors.New("media_path must be an absolute path on a local drive; UNC, device and alternate data stream paths are refused")
)

// CheckPathSyntax checks a path as text, before anything is asked of the file system.
func CheckPathSyntax(p string) error {
	if p == "" || len(p) > MaxPathBytes || strings.IndexByte(p, 0) >= 0 {
		return ErrPathSyntax
	}
	rest, ok := cutRoot(p)
	if !ok {
		return ErrPathSyntax
	}
	if rest == "" {
		return nil
	}
	for _, part := range splitComponents(rest) {
		if part == "" {
			return ErrPathSyntax
		}
	}
	return nil
}

// fileAccess is everything the source stage asks of the file system about the user's file. Tests replace its parts
// to hold a call or to change a file between two steps.
type fileAccess struct {
	resolve func(path string) (string, error)
	open    func(root *os.Root, name string) (*os.File, error)
	// reader wraps the open file for the copy; nil reads it directly.
	reader func(f *os.File) io.Reader
}

func systemFiles() fileAccess {
	return fileAccess{
		resolve: filepath.EvalSymlinks,
		open: func(root *os.Root, name string) (*os.File, error) {
			return root.OpenFile(name, os.O_RDONLY|openNonblock, 0)
		},
	}
}

// sourceFile is the user's file, open and checked. Every later read comes from this one handle.
type sourceFile struct {
	file *os.File
	size int64
}

// openSource checks path as text, resolves its links, opens the resolved file confined to the allowed directory
// that holds it and checks the open handle. A link or junction swapped in after the resolution makes the confined
// open fail, so what is opened never lies outside the directory.
func openSource(files fileAccess, dirs []AllowedDir, path string) (sourceFile, error) {
	if err := CheckPathSyntax(path); err != nil {
		return sourceFile{}, errPathRelative
	}
	resolved, err := files.resolve(path)
	if err != nil {
		return sourceFile{}, errUnresolved
	}
	for _, dir := range dirs {
		name, err := filepath.Rel(dir.Path, resolved)
		if err != nil || name == "." || !filepath.IsLocal(name) {
			continue
		}
		f, err := files.open(dir.Root, name)
		if err != nil {
			return sourceFile{}, errNotOpened
		}
		size, err := checkHandle(f)
		if err != nil {
			_ = f.Close()
			return sourceFile{}, err
		}
		return sourceFile{file: f, size: size}, nil
	}
	return sourceFile{}, errOutsideDirs
}

// checkHandle accepts a regular file with exactly one link and 1 byte to 100 MiB, and returns its size.
func checkHandle(f *os.File) (int64, error) {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, errNotRegular
	}
	links, err := linkCount(f)
	if err != nil {
		return 0, errNotRegular
	}
	switch {
	case links != 1:
		return 0, errHardLinked
	case info.Size() < 1:
		return 0, errEmptyFile
	case info.Size() > maxFileSize:
		return 0, errTooLarge
	}
	return info.Size(), nil
}

// copyChecked copies a file whose handle showed size bytes. It counts what is really read, stops at one byte more
// than that size or than the size limit, and looks at ctx between chunks. A file that grew or shrank is refused.
func copyChecked(ctx context.Context, dst io.Writer, src io.Reader, size int64) error {
	limit := min(size, maxFileSize) + 1
	counted := &io.LimitedReader{R: src, N: limit}
	buf := make([]byte, copyChunk)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := counted.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > size {
				return errFileChanged
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return errWorkFile
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errNotOpened
		}
	}
	if total != size {
		return errFileChanged
	}
	return nil
}
