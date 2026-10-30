package store

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// fixtureWAL returns the log of the gate's "wal" fixture: committed transactions, none checkpointed.
func fixtureWAL(t testing.TB) []byte {
	t.Helper()
	dirs := gateFixtures(t, t.TempDir())
	wal, err := os.ReadFile(filepath.Join(dirs["wal"], gateDB+"-wal"))
	if err != nil {
		t.Fatal(err)
	}
	return wal
}

// reencodeWAL rewrites a log's magic and every checksum for the given word order, frame by frame.
func reencodeWAL(wal []byte, magic uint32, order binary.ByteOrder) []byte {
	out := bytes.Clone(wal)
	binary.BigEndian.PutUint32(out[0:4], magic)
	s0, s1 := walChecksum(order, out[:24], 0, 0)
	binary.BigEndian.PutUint32(out[24:28], s0)
	binary.BigEndian.PutUint32(out[28:32], s1)
	ps := int(binary.BigEndian.Uint32(out[8:12]))
	for off := walHeaderSize; off+walFrameHeaderSize+ps <= len(out); off += walFrameHeaderSize + ps {
		f := out[off : off+walFrameHeaderSize+ps]
		s0, s1 = walChecksum(order, f[:8], s0, s1)
		s0, s1 = walChecksum(order, f[walFrameHeaderSize:], s0, s1)
		binary.BigEndian.PutUint32(f[16:20], s0)
		binary.BigEndian.PutUint32(f[20:24], s1)
	}
	return out
}

func TestScanWALBigEndianMatchesLittleEndian(t *testing.T) {
	le := fixtureWAL(t)
	if binary.BigEndian.Uint32(le[0:4]) != walMagicLE {
		t.Fatalf("fixture magic %#x, want the little-endian one", binary.BigEndian.Uint32(le[0:4]))
	}
	be := reencodeWAL(le, walMagicBE, binary.BigEndian)

	a, b := mustScan(t, le, 4096), mustScan(t, be, 4096)
	if a.dbPages == 0 || len(a.frames) == 0 {
		t.Fatalf("little-endian log indexed nothing: %+v", a)
	}
	if a.dbPages != b.dbPages || !maps.Equal(a.frames, b.frames) {
		t.Errorf("big-endian index %d pages / %d frames, little-endian %d / %d",
			b.dbPages, len(b.frames), a.dbPages, len(a.frames))
	}
}

func TestScanWALRefusals(t *testing.T) {
	wal := fixtureWAL(t)

	badVersion := bytes.Clone(wal)
	binary.BigEndian.PutUint32(badVersion[4:8], walVersion+1)
	badVersion = reencodeWAL(badVersion, walMagicLE, binary.LittleEndian)

	for name, tc := range map[string]struct {
		wal      []byte
		pageSize int64
	}{
		"format version":            {badVersion, 4096},
		"page size unlike the main": {wal, 8192},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := scanWAL(bytesReader(tc.wal), tc.pageSize); !errors.Is(err, ErrNotDatabase) {
				t.Errorf("err = %v, want ErrNotDatabase", err)
			}
		})
	}
}

func TestScanWALIgnores(t *testing.T) {
	wal := fixtureWAL(t)
	badHeaderSum := bytes.Clone(wal)
	badHeaderSum[24] ^= 0xff
	badMagic := bytes.Clone(wal)
	binary.BigEndian.PutUint32(badMagic[0:4], 0x12345678)
	badPageSize := bytes.Clone(wal)
	binary.BigEndian.PutUint32(badPageSize[8:12], 1000)

	for name, b := range map[string][]byte{
		"empty":                 nil,
		"shorter than a header": wal[:walHeaderSize-1],
		"header only":           wal[:walHeaderSize],
		"header only, bad version": func() []byte {
			b := bytes.Clone(wal[:walHeaderSize])
			binary.BigEndian.PutUint32(b[4:8], walVersion+1)
			return reencodeWAL(b, walMagicLE, binary.LittleEndian)
		}(),
		"bad header checksum": badHeaderSum,
		"unknown magic":       badMagic,
		"invalid page size":   badPageSize,
		"zeros":               make([]byte, walHeaderSize+walFrameHeaderSize+4096),
	} {
		t.Run(name, func(t *testing.T) {
			idx := mustScan(t, b, 4096)
			if idx.dbPages != 0 || len(idx.frames) != 0 {
				t.Errorf("indexed %d pages / %d frames, want none", idx.dbPages, len(idx.frames))
			}
		})
	}
}

func FuzzWALScan(f *testing.F) {
	dirs := gateFixtures(f, f.TempDir())
	for _, d := range dirs {
		if wal, err := os.ReadFile(filepath.Join(d, gateDB+"-wal")); err == nil {
			f.Add(wal)
		}
	}
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0x37, 0x7f, 0x06, 0x82}, 64))

	const ps = 4096
	f.Fuzz(func(t *testing.T, wal []byte) {
		idx, err := scanWAL(bytesReader(wal), ps)
		if err != nil {
			if !errors.Is(err, ErrNotDatabase) {
				t.Fatalf("unexpected error %v", err)
			}
			return
		}
		whole := 0
		if len(wal) >= walHeaderSize {
			whole = (len(wal) - walHeaderSize) / (walFrameHeaderSize + ps)
		}
		if len(idx.frames) > whole {
			t.Fatalf("%d pages indexed from %d whole frames", len(idx.frames), whole)
		}
		for pg, off := range idx.frames {
			if pg == 0 || off < walHeaderSize+walFrameHeaderSize || off+ps > int64(len(wal)) {
				t.Fatalf("page %d at offset %d lies outside a log of %d bytes", pg, off, len(wal))
			}
		}
	})
}
