package store

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"modernc.org/sqlite/vfs"
)

// The committed view lets the preflight read a database, including transactions committed only to its
// write-ahead log, without SQLite opening a real file. Every SQLite open mode that reads the log creates,
// rewrites or deletes -wal or -shm in some state, so the log is replayed here instead, by the rules of SQLite's
// own recovery, and SQLite reads one virtual file through a read-only VFS that cannot write or create anything.
//
// -shm is never read: it only coordinates live connections, and the preflight runs under the instance lock, so
// there are none.

const (
	viewName = "db" // the only name the view serves

	dbHeaderSize       = 100
	walHeaderSize      = 32
	walFrameHeaderSize = 24
	walVersion         = 3007000
	walMagicLE         = 0x377f0682 // checksums over little-endian words
	walMagicBE         = 0x377f0683 // checksums over big-endian words
)

var dbMagic = []byte("SQLite format 3\x00")

// pageSizeFromHeader returns the page size recorded in a main database file's header.
func pageSizeFromHeader(hdr []byte) (int64, error) {
	if len(hdr) < dbHeaderSize || !bytes.Equal(hdr[:len(dbMagic)], dbMagic) {
		return 0, ErrNotDatabase
	}
	ps := int64(binary.BigEndian.Uint16(hdr[16:18]))
	if ps == 1 {
		ps = 65536
	}
	if !validPageSize(ps) {
		return 0, ErrNotDatabase
	}
	return ps, nil
}

func validPageSize(ps int64) bool { return ps >= 512 && ps <= 65536 && ps&(ps-1) == 0 }

// walIndex locates the pages of a log's committed transactions.
type walIndex struct {
	dbPages uint32           // database size in pages after the last commit; 0 when the log holds no commit
	frames  map[uint32]int64 // page number to the offset of that page's newest committed data in the log
}

// scanWAL reads a log once, front to back, and indexes its committed pages. It keeps offsets, not page data, so
// its memory grows with the number of distinct pages in the log and never with the database.
//
// A log that SQLite would ignore gives an empty index: one no longer than its header, or whose header has an unknown
// magic, an invalid page size or a checksum that does not match, as a header of zeros after a power loss does. A
// log that SQLite would refuse, or whose page size differs from the main file's, is ErrNotDatabase.
func scanWAL(r io.Reader, mainPageSize int64) (walIndex, error) {
	idx := walIndex{frames: map[uint32]int64{}}
	br := bufio.NewReaderSize(r, 64<<10)
	var hdr [walHeaderSize]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return idx, nil
		}
		return walIndex{}, err
	}
	// SQLite reads the header only of a log longer than the header itself.
	if _, err := br.Peek(1); err != nil {
		if errors.Is(err, io.EOF) {
			return idx, nil
		}
		return walIndex{}, err
	}

	var order binary.ByteOrder
	switch binary.BigEndian.Uint32(hdr[0:4]) {
	case walMagicLE:
		order = binary.LittleEndian
	case walMagicBE:
		order = binary.BigEndian
	default:
		return idx, nil
	}
	ps := int64(binary.BigEndian.Uint32(hdr[8:12]))
	if !validPageSize(ps) {
		return idx, nil
	}
	s0, s1 := walChecksum(order, hdr[:24], 0, 0)
	if s0 != binary.BigEndian.Uint32(hdr[24:28]) || s1 != binary.BigEndian.Uint32(hdr[28:32]) {
		return idx, nil
	}
	if binary.BigEndian.Uint32(hdr[4:8]) != walVersion {
		return walIndex{}, fmt.Errorf("%w: the write-ahead log has an unsupported format version", ErrNotDatabase)
	}
	if ps != mainPageSize {
		return walIndex{}, fmt.Errorf("%w: the write-ahead log's page size differs from the database's", ErrNotDatabase)
	}

	// A frame counts only while it carries the header's salts, a page number and the running checksum. The first
	// frame that does not, including a torn last one, ends the log, and frames after the last commit are dropped.
	frame := make([]byte, walFrameHeaderSize+ps)
	pending := map[uint32]int64{}
	for off := int64(walHeaderSize); ; off += int64(len(frame)) {
		if _, err := io.ReadFull(br, frame); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return walIndex{}, err
		}
		pgno := binary.BigEndian.Uint32(frame[0:4])
		if pgno == 0 || !bytes.Equal(frame[8:16], hdr[16:24]) {
			break
		}
		s0, s1 = walChecksum(order, frame[:8], s0, s1)
		s0, s1 = walChecksum(order, frame[walFrameHeaderSize:], s0, s1)
		if s0 != binary.BigEndian.Uint32(frame[16:20]) || s1 != binary.BigEndian.Uint32(frame[20:24]) {
			break
		}
		pending[pgno] = off + walFrameHeaderSize
		if commit := binary.BigEndian.Uint32(frame[4:8]); commit != 0 {
			maps.Copy(idx.frames, pending)
			clear(pending)
			idx.dbPages = commit
		}
	}
	return idx, nil
}

// walChecksum continues SQLite's log checksum over b, whose length is a multiple of 8.
func walChecksum(order binary.ByteOrder, b []byte, s0, s1 uint32) (uint32, uint32) {
	for i := 0; i+8 <= len(b); i += 8 {
		s0 += order.Uint32(b[i:]) + s1
		s1 += order.Uint32(b[i+4:]) + s0
	}
	return s0, s1
}

type committedView struct {
	main, wal *os.File // wal is nil when there is no log
	pageSize  int64
	size      int64
	frames    map[uint32]int64
}

// openCommittedView prepares a read-only view of the database at path as of its last commit, and returns a DSN
// that opens it. Both files are opened read-only. Callers close every connection on the DSN before release, which
// unregisters the view and closes the files.
func openCommittedView(path string) (dsn string, release func() error, err error) {
	dsn, release, _, err = openCommittedViewIDs(path)
	return dsn, release, err
}

// viewIDs holds the identities of the files the view read, taken from its own handles. wal is zero when there
// is no log.
type viewIDs struct {
	main, wal private.FileID
	hasWAL    bool
}

// openCommittedViewIDs is openCommittedView that also reports the identity of each file it opened, so the caller
// can check that they are the files it examined earlier.
func openCommittedViewIDs(path string) (dsn string, release func() error, ids viewIDs, err error) {
	v := &committedView{}
	defer func() {
		if err != nil {
			v.closeFiles()
		}
	}()

	if v.main, err = os.Open(path); err != nil {
		return "", nil, viewIDs{}, err
	}
	if ids.main, err = private.FileIDOf(v.main); err != nil {
		return "", nil, viewIDs{}, err
	}
	fi, err := v.main.Stat()
	if err != nil {
		return "", nil, viewIDs{}, err
	}
	hdr := make([]byte, dbHeaderSize)
	if _, err := v.main.ReadAt(hdr, 0); err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil, viewIDs{}, ErrNotDatabase
		}
		return "", nil, viewIDs{}, err
	}
	if v.pageSize, err = pageSizeFromHeader(hdr); err != nil {
		return "", nil, viewIDs{}, err
	}
	v.size = fi.Size()

	v.wal, err = os.Open(path + "-wal")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		v.wal, err = nil, nil
	case err != nil:
		return "", nil, viewIDs{}, err
	default:
		if ids.wal, err = private.FileIDOf(v.wal); err != nil {
			return "", nil, viewIDs{}, err
		}
		ids.hasWAL = true
		idx, err := scanWAL(v.wal, v.pageSize)
		if err != nil {
			return "", nil, viewIDs{}, err
		}
		if idx.dbPages != 0 {
			v.frames, v.size = idx.frames, int64(idx.dbPages)*v.pageSize
		}
	}

	name, fsys, err := vfs.New(v)
	if err != nil {
		return "", nil, viewIDs{}, err
	}
	release = func() error {
		if err := fsys.Close(); err != nil {
			return err
		}
		return v.closeFiles()
	}
	return "file:" + viewName + "?mode=ro&vfs=" + name, release, ids, nil
}

func (v *committedView) closeFiles() error {
	var errs []error
	for _, f := range []*os.File{v.main, v.wal} {
		if f != nil {
			errs = append(errs, f.Close())
		}
	}
	return errors.Join(errs...)
}

func (v *committedView) Open(name string) (fs.File, error) {
	if name != viewName {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &viewFile{v: v}, nil
}

// readAt reads the committed database image: indexed pages from the log, the rest from the main file.
func (v *committedView) readAt(b []byte, off int64) (int, error) {
	total := 0
	for len(b) > 0 {
		if off >= v.size {
			return total, io.EOF
		}
		pgno := uint32(off/v.pageSize) + 1
		in := off % v.pageSize
		n := min(int64(len(b)), v.pageSize-in, v.size-off)
		if at, ok := v.frames[pgno]; ok {
			if _, err := v.wal.ReadAt(b[:n], at+in); err != nil {
				return total, err
			}
		} else if got, err := v.main.ReadAt(b[:n], off); err != nil {
			if !errors.Is(err, io.EOF) {
				return total, err
			}
			clear(b[got:n]) // the log grew the database, but this page was never written
		}
		// Bytes 18 and 19 are the file format versions; 2 means a log. Presenting 1 (rollback journal) keeps
		// SQLite from asking for shared memory, which this VFS does not have.
		for i := max(off, 18); i < min(off+n, 20); i++ {
			b[i-off] = 1
		}
		b, off, total = b[n:], off+n, total+int(n)
	}
	return total, nil
}

// viewFile is the one file SQLite opens through the view. modernc's VFS reads with Seek and Read.
type viewFile struct {
	v   *committedView
	off int64
}

func (f *viewFile) Stat() (fs.FileInfo, error) { return viewInfo{f.v.size}, nil }
func (f *viewFile) Close() error               { return nil }

func (f *viewFile) Seek(off int64, whence int) (int64, error) {
	if whence != io.SeekStart || off < 0 {
		return 0, errors.New("committed view: unsupported seek")
	}
	f.off = off
	return off, nil
}

func (f *viewFile) Read(b []byte) (int, error) {
	n, err := f.v.readAt(b, f.off)
	f.off += int64(n)
	return n, err
}

type viewInfo struct{ size int64 }

func (i viewInfo) Name() string       { return viewName }
func (i viewInfo) Size() int64        { return i.size }
func (i viewInfo) Mode() fs.FileMode  { return 0o400 }
func (i viewInfo) ModTime() time.Time { return time.Time{} }
func (i viewInfo) IsDir() bool        { return false }
func (i viewInfo) Sys() any           { return nil }
