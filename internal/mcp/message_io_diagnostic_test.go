//go:build messageiodiagnostic && (darwin || linux) && (amd64 || arm64)

package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sys/unix"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

type ioTally struct{ calls, bytes int64 }

type scaleIO struct {
	mu    sync.Mutex
	main  unix.Stat_t
	wal   unix.Stat_t
	fds   map[int32]int
	reads [3]ioTally
}

var currentScaleIO atomic.Pointer[scaleIO]

func scalePread(tls *libc.TLS, fd int32, buf uintptr, size uint64, offset int64) int64 {
	n := libc.Xpread(tls, fd, buf, size, offset)
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

func TestSQLiteScaleBackingReads(t *testing.T) {
	installScalePread(t)
	for _, query := range []struct {
		name, text string
		count      int
	}{{"sparse", "sparse needle", 12}, {"empty", "not-present", 0}} {
		modes := []bool{false, true}
		if query.count == 0 {
			modes = []bool{true, false}
		}
		for _, checkpoint := range modes {
			mode := "post-ingest"
			if checkpoint {
				mode = "checkpointed"
			}
			t.Run(query.name+"/"+mode, func(t *testing.T) {
				st, dir := seedMessageScale(t)
				path := filepath.Join(dir, "whatsapp.db")
				if checkpoint {
					dsn := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw"}
					db, err := sql.Open("sqlite", dsn.String())
					if err != nil {
						t.Fatal(err)
					}
					var busy, log, done int
					err = db.QueryRowContext(t.Context(), `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &done)
					if closeErr := db.Close(); err != nil || closeErr != nil || busy != 0 {
						t.Fatalf("diagnostic checkpoint busy=%d: %v", busy, errors.Join(err, closeErr))
					}
				}
				var main, wal unix.Stat_t
				if err := unix.Stat(path, &main); err != nil {
					t.Fatal(err)
				}
				if err := unix.Stat(path+"-wal", &wal); err != nil {
					t.Fatal(err)
				}
				if checkpoint && wal.Size != 0 || !checkpoint && wal.Size == 0 {
					t.Fatalf("unexpected WAL state: %d bytes", wal.Size)
				}
				c := chatTestServer(t, st.ToolReader())
				for trial := 1; trial <= 2; trial++ {
					waitToolCredit(t, c.guard)
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					sample := &scaleIO{main: main, wal: wal, fds: make(map[int32]int)}
					currentScaleIO.Store(sample)
					start := time.Now()
					result, err := c.CallTool(ctx, &sdk.CallToolParams{Name: "list_messages", Arguments: map[string]any{"query": query.text}})
					elapsed := time.Since(start)
					cancel()
					currentScaleIO.Store(nil)
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
					t.Logf("trial=%d result=%s elapsed=%v main_size=%d wal_size=%d main_reads=%d main_bytes=%d wal_reads=%d wal_bytes=%d other_reads=%d other_bytes=%d",
						trial, status, elapsed, main.Size, wal.Size, reads[0].calls, reads[0].bytes, reads[1].calls, reads[1].bytes, reads[2].calls, reads[2].bytes)
					if reads[0].calls == 0 || reads[2].calls != 0 || checkpoint && reads[1].bytes != 0 {
						t.Fatal("unexpected read attribution")
					}
				}
			})
		}
	}
}
