//go:build messageiodiagnostic && (darwin || linux) && (amd64 || arm64)

package mcp

import (
	"context"
	"database/sql"
	"fmt"
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
	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"golang.org/x/sys/unix"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

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

// coveringIndex is the candidate: it takes the place of the newest-first time
// index in a temporary database, so the production search uses it unchanged.
const coveringIndex = `CREATE INDEX messages_time ON messages (time, chat_jid, id, text, caption, text_fold, caption_fold)`

type scaleFixture struct {
	name, query string
	want        []string
	seed        func(*testing.T, *store.Store)
}

// seedScaleRows stores the same rows as the acceptance scale fixture.
func seedScaleRows(t *testing.T, st *store.Store) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for start := 1; start <= 400000; start += 1000 {
		rows := make([]store.Message, 0, 1000)
		for i := start; i < start+1000; i++ {
			text := "ordinary chatter"
			if i <= 360000 && i%30000 == 0 {
				text = "sparse needle"
			}
			rows = append(rows, store.Message{ChatJID: fixtureJID(0), SenderJID: fixtureJID(0), ID: fmt.Sprintf("scale-%08d", i), Time: base.Add(time.Duration(i) * time.Second), Text: text})
		}
		if err := st.StoreMessages(t.Context(), rows); err != nil {
			t.Fatalf("seed %d: %v", start, err)
		}
	}
}

// seedLargeRows stores one old match behind 128 newer maximum-size rows made
// of unit, in both text and caption.
func seedLargeRows(unit string) func(*testing.T, *store.Store) {
	return func(t *testing.T, st *store.Store) {
		t.Helper()
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		field := strings.Repeat(unit, (256<<10)/len(unit))
		rows := []store.Message{{ChatJID: fixtureJID(0), SenderJID: fixtureJID(0), ID: "older", Time: base, Text: "needle"}}
		for i := 1; i <= 128; i++ {
			rows = append(rows, store.Message{ChatJID: fixtureJID(0), SenderJID: fixtureJID(0), ID: fmt.Sprintf("large-%03d", i), Time: base.Add(time.Duration(i) * time.Second), Text: field, Caption: field})
			if len(rows) >= 4 || i == 128 {
				if err := st.StoreMessages(t.Context(), rows); err != nil {
					t.Fatalf("seed large %d: %v", i, err)
				}
				rows = rows[:0]
			}
		}
	}
}

func scaleFixtures() []scaleFixture {
	var sparse []string
	for i := range 12 {
		sparse = append(sparse, fmt.Sprintf("scale-%08d", 360000-i*30000))
	}
	return []scaleFixture{
		{"sparse", "sparse needle", sparse, seedScaleRows},
		{"empty", "not-present", nil, seedScaleRows},
		{"large-ascii", "needle", []string{"older"}, seedLargeRows("a")},
		{"large-expanded", "needle", []string{"older"}, seedLargeRows("ΐ")},
	}
}

func openDiagnosticStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := private.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Release(); err != nil {
			t.Error(err)
		}
	})
	st, err := store.Open(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return st, filepath.Join(dir, "whatsapp.db")
}

func openDiagnosticDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func useCoveringIndex(t *testing.T, path string) {
	t.Helper()
	db := openDiagnosticDB(t, path)
	defer db.Close()
	for _, statement := range []string{`DROP INDEX messages_time`, coveringIndex} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

// checkScalePlan confirms which schema the search shape runs against.
func checkScalePlan(t *testing.T, path string, covering bool) {
	t.Helper()
	db := openDiagnosticDB(t, path)
	defer db.Close()
	var id, parent, unused int
	var detail string
	if err := db.QueryRowContext(t.Context(), `EXPLAIN QUERY PLAN SELECT time,chat_jid,id,rowid,text,caption,text_fold,caption_fold
	 FROM messages INDEXED BY messages_time WHERE (time,chat_jid,id)<(?1,?2,?3) ORDER BY time DESC,chat_jid DESC,id DESC LIMIT 64`,
		int64(1)<<62, "", "").Scan(&id, &parent, &unused, &detail); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(detail, "COVERING INDEX messages_time") != covering {
		t.Fatalf("plan with covering=%t: %s", covering, detail)
	}
}

func logScalePages(t *testing.T, fixture, schema, path string) {
	t.Helper()
	db := openDiagnosticDB(t, path)
	defer db.Close()
	for _, name := range []string{"messages", "messages_time"} {
		var pages, payload int64
		if err := db.QueryRowContext(t.Context(), `SELECT count(*),coalesce(sum(payload),0) FROM dbstat WHERE name=?`, name).Scan(&pages, &payload); err != nil {
			t.Fatal(err)
		}
		t.Logf("fixture=%s schema=%s btree=%s pages=%d payload=%d", fixture, schema, name, pages, payload)
	}
}

func fileSize(t *testing.T, path string) (unix.Stat_t, int64) {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	return stat, int64(stat.Size)
}

// TestSQLiteScaleBackingReads compares today's schema with the covering time
// index. Each case seeds its own history and alternates order between rounds;
// neither arm warms or checkpoints the store.
func TestSQLiteScaleBackingReads(t *testing.T) {
	installScalePread(t)
	tls := libc.NewTLS()
	t.Cleanup(tls.Close)
	for round := 1; round <= 3; round++ {
		for _, fixture := range scaleFixtures() {
			schemas := []string{"today", "covering"}
			if round%2 == 0 {
				schemas = []string{"covering", "today"}
			}
			for _, schema := range schemas {
				t.Run(fmt.Sprintf("%s/round-%d/%s", fixture.name, round, schema), func(t *testing.T) {
					st, path := openDiagnosticStore(t)
					if schema == "covering" {
						useCoveringIndex(t, path)
					}
					seeding := time.Now()
					fixture.seed(t, st)
					ingest := time.Since(seeding)
					checkScalePlan(t, path, schema == "covering")
					main, mainSize := fileSize(t, path)
					wal, walSize := fileSize(t, path+"-wal")
					t.Logf("fixture=%s schema=%s round=%d ingest=%v main_size=%d wal_size=%d", fixture.name, schema, round, ingest, mainSize, walSize)
					c := chatTestServer(t, st.ToolReader())
					for trial := 1; trial <= 2; trial++ {
						waitToolCredit(t, c.guard)
						before := sampleProcess(t, tls, 1)
						ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
						sample := &scaleIO{main: main, wal: wal, fds: make(map[int32]int)}
						currentScaleIO.Store(sample)
						start := time.Now()
						result, err := c.CallTool(ctx, &sdk.CallToolParams{Name: "list_messages", Arguments: map[string]any{"query": fixture.query}})
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
							e, _ := root["error"].(map[string]any)
							status = fmt.Sprint(e["code"])
							if status != "timeout" {
								t.Errorf("unexpected tool error: %v", root)
							}
						} else {
							root := checkToolResult(t, "list_messages", result, false)
							rows := root["messages"].([]any)
							if len(rows) != len(fixture.want) {
								t.Fatalf("matches=%d, want %d", len(rows), len(fixture.want))
							}
							for i, row := range rows {
								if got := row.(map[string]any)["id"]; got != fixture.want[i] {
									t.Fatalf("result %d: got %v, want %s", i, got, fixture.want[i])
								}
							}
						}
						t.Logf("fixture=%s schema=%s round=%d trial=%d result=%s elapsed=%v main_reads=%d main_bytes=%d main_wall=%v wal_reads=%d wal_bytes=%d wal_wall=%v other_reads=%d other_bytes=%d",
							fixture.name, schema, round, trial, status, elapsed, reads[0].calls, reads[0].bytes, reads[0].wall, reads[1].calls, reads[1].bytes, reads[1].wall, reads[2].calls, reads[2].bytes)
						t.Logf("fixture=%s schema=%s round=%d trial=%d user=%v system=%v minor_faults=%d major_faults=%d input_blocks=%d rss_before=%d rss_after=%d rss_peak_before=%d rss_peak_after=%d sqlite_heap_peak=%d",
							fixture.name, schema, round, trial, after.user-before.user, after.system-before.system,
							after.minorFaults-before.minorFaults, after.majorFaults-before.majorFaults, after.input-before.input,
							before.resident, after.resident, before.peak, after.peak, after.sqliteHeapPeak)
						if reads[2].calls != 0 || reads[0].calls+reads[1].calls == 0 && trial == 1 {
							t.Fatal("unexpected read attribution")
						}
					}
					if round == 1 {
						logScalePages(t, fixture.name, schema, path)
					}
				})
			}
		}
	}
}
