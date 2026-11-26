//go:build messageiodiagnostic && (darwin || linux) && (amd64 || arm64)

package mcp

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sys/unix"
	"modernc.org/libc"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// candidateMmapBytes covers the whole scale fixture. A larger database would
// be mapped only up to this offset and read with pread beyond it.
const candidateMmapBytes = 512 << 20

var (
	diagnosticMmap       atomic.Int64
	diagnosticMapped     atomic.Int64
	registerDiagnosticIO sync.Once
)

// registerMmapCandidate maps read-only connections while diagnosticMmap is set.
// Production connection settings are unchanged.
func registerMmapCandidate() {
	registerDiagnosticIO.Do(func() {
		sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
			size := diagnosticMmap.Load()
			if size == 0 || !strings.Contains(dsn, "query_only(1)") {
				return nil
			}
			rows, err := conn.QueryContext(context.Background(), fmt.Sprintf("PRAGMA mmap_size=%d", size), nil)
			if err != nil {
				return err
			}
			row := make([]driver.Value, 1)
			err = rows.Next(row)
			if closeErr := rows.Close(); errors.Is(err, io.EOF) || err != nil || closeErr != nil {
				return fmt.Errorf("mmap_size not reported: %w", errors.Join(err, closeErr))
			}
			if row[0] != size {
				return fmt.Errorf("mmap_size is %v, want %d", row[0], size)
			}
			diagnosticMapped.Add(1)
			return nil
		})
	})
}

type ioTally struct {
	calls, bytes int64
	wall         time.Duration
}

type scaleIO struct {
	mu    sync.Mutex
	main  unix.Stat_t
	wal   unix.Stat_t
	fds   map[int32]int
	reads [3]ioTally
}

var currentScaleIO atomic.Pointer[scaleIO]

func scalePread(tls *libc.TLS, fd int32, buf uintptr, size uint64, offset int64) int64 {
	start := time.Now()
	n := libc.Xpread(tls, fd, buf, size, offset)
	wall := time.Since(start)
	if sample := currentScaleIO.Load(); sample != nil {
		sample.mu.Lock()
		defer sample.mu.Unlock()
		kind, known := sample.fds[fd]
		if !known {
			kind = 2
			var stat unix.Stat_t
			if unix.Fstat(int(fd), &stat) == nil {
				switch {
				case stat.Dev == sample.main.Dev && stat.Ino == sample.main.Ino:
					kind = 0
				case stat.Dev == sample.wal.Dev && stat.Ino == sample.wal.Ino:
					kind = 1
				}
			}
			sample.fds[fd] = kind
		}
		sample.reads[kind].calls++
		sample.reads[kind].bytes += max(0, n)
		sample.reads[kind].wall += wall
	}
	return n
}

// The generated modernc v1.62 VFS represents callbacks as Go func-value
// pointers. Install only for this opt-in, non-parallel synthetic test.
func installScalePread(t *testing.T) {
	t.Helper()
	tls := libc.NewTLS()
	t.Cleanup(tls.Close)
	address := sqlite3.Xsqlite3_vfs_find(tls, 0)
	if address == 0 {
		t.Fatal("no default SQLite VFS")
	}
	vfs := (*sqlite3.Tsqlite3_vfs)(*(*unsafe.Pointer)(unsafe.Pointer(&address)))
	if vfs.FiVersion < 3 || vfs.FxSetSystemCall == 0 || vfs.FxGetSystemCall == 0 {
		t.Fatal("SQLite VFS lacks syscall overrides")
	}
	set := *(*func(*libc.TLS, uintptr, uintptr, uintptr) int32)(unsafe.Pointer(&vfs.FxSetSystemCall))
	get := *(*func(*libc.TLS, uintptr, uintptr) uintptr)(unsafe.Pointer(&vfs.FxGetSystemCall))
	hook := scalePread
	original := libc.Xpread
	var installed int
	for _, name := range []string{"pread", "pread64"} {
		cname, err := libc.CString(name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { libc.Xfree(tls, cname) })
		previous := get(tls, address, cname)
		if previous == 0 {
			continue
		}
		if previous != *(*uintptr)(unsafe.Pointer(&original)) {
			t.Fatalf("unexpected %s implementation", name)
		}
		if rc := set(tls, address, cname, *(*uintptr)(unsafe.Pointer(&hook))); rc != sqlite3.SQLITE_OK {
			t.Fatalf("set %s: %d", name, rc)
		}
		t.Cleanup(func() {
			if rc := set(tls, address, cname, previous); rc != sqlite3.SQLITE_OK {
				t.Errorf("restore %s: %d", name, rc)
			}
		})
		installed++
	}
	if installed == 0 {
		t.Fatal("SQLite VFS has no pread hook")
	}
}

// processSample holds process-wide counters; resident and peak are bytes.
type processSample struct {
	user, system                    time.Duration
	minorFaults, majorFaults, input int64
	resident, peak, sqliteHeapPeak  int64
}

func sampleProcess(t *testing.T, tls *libc.TLS, resetHeapPeak int32) processSample {
	t.Helper()
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		t.Fatal(err)
	}
	resident, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	peak := int64(usage.Maxrss)
	if runtime.GOOS == "linux" {
		peak *= 1024
	}
	return processSample{
		user: time.Duration(usage.Utime.Nano()), system: time.Duration(usage.Stime.Nano()),
		minorFaults: int64(usage.Minflt), majorFaults: int64(usage.Majflt), input: int64(usage.Inblock),
		resident: resident * 1024, peak: peak,
		sqliteHeapPeak: sqlite3.Xsqlite3_memory_highwater(tls, resetHeapPeak),
	}
}

func logScalePages(t *testing.T, path string) {
	t.Helper()
	dsn := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"messages", "messages_time"} {
		var pages, bytes int64
		if err := db.QueryRowContext(t.Context(), `SELECT count(*),coalesce(sum(pgsize),0) FROM dbstat WHERE name=?`, name).Scan(&pages, &bytes); err != nil {
			t.Fatal(err)
		}
		t.Logf("btree=%s pages=%d bytes=%d", name, pages, bytes)
	}
}

// TestSQLiteScaleBackingReads compares the production read settings with the
// mapped-read candidate. Each case seeds its own history and alternates order
// between rounds; neither arm warms or checkpoints the store.
func TestSQLiteScaleBackingReads(t *testing.T) {
	registerMmapCandidate()
	installScalePread(t)
	tls := libc.NewTLS()
	t.Cleanup(tls.Close)
	pagesLogged := false
	for round := 1; round <= 3; round++ {
		for _, query := range []struct {
			name, text string
			count      int
		}{{"sparse", "sparse needle", 12}, {"empty", "not-present", 0}} {
			modes := []int64{0, candidateMmapBytes}
			if round%2 == 0 {
				modes = []int64{candidateMmapBytes, 0}
			}
			for _, mapping := range modes {
				mode := "baseline"
				if mapping != 0 {
					mode = "mmap"
				}
				t.Run(fmt.Sprintf("%s/round-%d/%s", query.name, round, mode), func(t *testing.T) {
					diagnosticMmap.Store(mapping)
					t.Cleanup(func() { diagnosticMmap.Store(0) })
					mapped := diagnosticMapped.Load()
					st, dir := seedMessageScale(t)
					path := filepath.Join(dir, "whatsapp.db")
					var main, wal unix.Stat_t
					if err := unix.Stat(path, &main); err != nil {
						t.Fatal(err)
					}
					if err := unix.Stat(path+"-wal", &wal); err != nil {
						t.Fatal(err)
					}
					if wal.Size == 0 {
						t.Fatal("expected the post-ingest WAL")
					}
					c := chatTestServer(t, st.ToolReader())
					// The pool opens readers on demand; each one opened while the
					// candidate is set must have accepted the mapping.
					if got := diagnosticMapped.Load() - mapped; (got > 0) != (mapping != 0) {
						t.Fatalf("mapped connections=%d with mmap_size=%d", got, mapping)
					}
					for trial := 1; trial <= 2; trial++ {
						waitToolCredit(t, c.guard)
						before := sampleProcess(t, tls, 1)
						ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
						sample := &scaleIO{main: main, wal: wal, fds: make(map[int32]int)}
						currentScaleIO.Store(sample)
						start := time.Now()
						result, err := c.CallTool(ctx, &sdk.CallToolParams{Name: "list_messages", Arguments: map[string]any{"query": query.text}})
						elapsed := time.Since(start)
						cancel()
						currentScaleIO.Store(nil)
						after := sampleProcess(t, tls, 0)
						sample.mu.Lock()
						reads := sample.reads
						sample.mu.Unlock()
						if err != nil {
							t.Fatal(err)
						}
						status := "exact"
						if result.IsError {
							root := checkToolResult(t, "list_messages", result, true)
							if e, _ := root["error"].(map[string]any); e["code"] != "timeout" {
								t.Fatalf("unexpected tool result: %v", root)
							}
							status = "timeout"
						} else {
							root := checkToolResult(t, "list_messages", result, false)
							rows := root["messages"].([]any)
							if len(rows) != query.count {
								t.Fatalf("matches=%d, want %d", len(rows), query.count)
							}
							for i, row := range rows {
								if want := fmt.Sprintf("scale-%08d", 360000-i*30000); row.(map[string]any)["id"] != want {
									t.Fatalf("result %d: want %s", i, want)
								}
							}
						}
						t.Logf("query=%s mode=%s round=%d trial=%d result=%s elapsed=%v main_size=%d wal_size=%d main_reads=%d main_bytes=%d main_wall=%v wal_reads=%d wal_bytes=%d wal_wall=%v other_reads=%d other_bytes=%d",
							query.name, mode, round, trial, status, elapsed, main.Size, wal.Size, reads[0].calls, reads[0].bytes, reads[0].wall, reads[1].calls, reads[1].bytes, reads[1].wall, reads[2].calls, reads[2].bytes)
						t.Logf("query=%s mode=%s round=%d trial=%d user=%v system=%v minor_faults=%d major_faults=%d input_blocks=%d rss_before=%d rss_after=%d rss_peak_before=%d rss_peak_after=%d sqlite_heap_peak=%d",
							query.name, mode, round, trial, after.user-before.user, after.system-before.system,
							after.minorFaults-before.minorFaults, after.majorFaults-before.majorFaults, after.input-before.input,
							before.resident, after.resident, before.peak, after.peak, after.sqliteHeapPeak)
						if reads[2].calls != 0 || mapping == 0 && reads[0].calls == 0 {
							t.Fatal("unexpected read attribution")
						}
					}
					t.Logf("query=%s mode=%s round=%d mapped_connections=%d", query.name, mode, round, diagnosticMapped.Load()-mapped)
					if !pagesLogged {
						pagesLogged = true
						logScalePages(t, path)
					}
				})
			}
		}
	}
}
