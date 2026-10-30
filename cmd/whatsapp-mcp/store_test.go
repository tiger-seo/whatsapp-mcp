package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	_ "modernc.org/sqlite"
)

func dbPath(dir string) string { return filepath.Join(dir, "whatsapp.db") }

func TestStdioCreatesTheDatabase(t *testing.T) {
	dir := privateDir(t)
	r := runIn(t, nil, "stdio", "--data-dir", dir)
	r.expect(t, exitOK)
	if _, err := os.Stat(dbPath(dir)); err != nil {
		t.Fatalf("no database: %v", err)
	}
	if !strings.Contains(r.stderr, "event=store.ready") {
		t.Errorf("stderr lacks store.ready:\n%s", r.stderr)
	}
	second := runIn(t, nil, "stdio", "--data-dir", dir)
	second.expect(t, exitOK)
	if strings.Contains(second.stderr, "event=store.migrate") {
		t.Errorf("a second start ran a migration:\n%s", second.stderr)
	}
}

func TestLogTimeIsUTC(t *testing.T) {
	r := runIn(t, nil, "stdio", "--data-dir", privateDir(t))
	r.expect(t, exitOK)
	var checked int
	for _, line := range strings.Split(r.stderr, "\n") {
		if m := regexp.MustCompile(`^time=(\S+) `).FindStringSubmatch(line); m != nil {
			checked++
			if !strings.HasSuffix(m[1], "Z") {
				t.Errorf("log time %q is not UTC", m[1])
			}
		}
	}
	if checked == 0 {
		t.Error("no log line with a time")
	}
}

func TestPairOpensAndClosesTheStore(t *testing.T) {
	dir := privateDir(t)
	r := runIn(t, nil, "pair", "--data-dir", dir)
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, "pair is not implemented yet") {
		t.Errorf("stderr = %s", r.stderr)
	}
	if _, err := os.Stat(dbPath(dir)); err != nil {
		t.Errorf("pair did not open the store: %v", err)
	}
	if _, err := os.Stat(dbPath(dir) + "-wal"); err == nil {
		t.Error("pair left the store open: -wal still exists")
	}
}

func TestStatusAndVersionDoNotCreateTheDatabase(t *testing.T) {
	dir := privateDir(t)
	runIn(t, nil, "status", "--data-dir", dir)
	runIn(t, nil, "version")
	if _, err := os.Stat(dbPath(dir)); err == nil {
		t.Error("status or version created whatsapp.db")
	}
}

func TestStdioRefusesANonDatabase(t *testing.T) {
	dir := privateDir(t)
	junk := make([]byte, 4096)
	for i := range junk {
		junk[i] = byte(i*13 + 5)
	}
	if err := os.WriteFile(dbPath(dir), junk, 0o600); err != nil {
		t.Fatal(err)
	}
	r := runIn(t, nil, "stdio", "--data-dir", dir)
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, "is not a usable SQLite database") {
		t.Errorf("stderr = %s", r.stderr)
	}
	if b, _ := os.ReadFile(dbPath(dir)); !bytes.Equal(b, junk) {
		t.Error("the file changed")
	}
}

func TestStdioRefusesATooNewSchemaAndReleasesTheLock(t *testing.T) {
	dir := privateDir(t)
	runIn(t, nil, "stdio", "--data-dir", dir).expect(t, exitOK)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath(dir))+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE mcp_version SET version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	for i := 0; i < 2; i++ {
		r := runIn(t, nil, "stdio", "--data-dir", dir)
		r.expect(t, exitError)
		for _, want := range []string{"message schema version 99", "newer than this binary supports (1)"} {
			if !strings.Contains(r.stderr, want) {
				t.Errorf("run %d: stderr lacks %q:\n%s", i, want, r.stderr)
			}
		}
		if strings.Contains(r.stderr, "lock") {
			t.Errorf("run %d: the lock was still held:\n%s", i, r.stderr)
		}
	}
}

// runWith runs body as a stdio command would, so a test can drive the store through the real startup and
// shutdown path.
func runWith(t *testing.T, dir string, body func(context.Context, *lifecycle, *slog.Logger, *store.Store)) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	s := &streams{stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &stderr}
	cmd := &command{name: "stdio", flags: true}
	code := runUntilStopped(cmd, []string{"--data-dir", dir}, s, body)
	return code, stderr.String()
}

func TestShutdownUnderWriteLoad(t *testing.T) {
	dir := privateDir(t)
	var acked atomic.Int64
	var shutdownStart atomic.Int64
	code, stderr := runWith(t, dir, func(ctx context.Context, lc *lifecycle, _ *slog.Logger, st *store.Store) {
		go func() {
			for i := 0; ; i++ {
				err := st.StoreMessage(context.Background(), store.Message{
					ChatJID: "491511234567@s.whatsapp.net", ID: fmt.Sprintf("load-%d", i), SenderJID: "491511234567@s.whatsapp.net",
					Time: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second), Text: "x",
				})
				if err != nil {
					return
				}
				acked.Add(1)
			}
		}()
		for acked.Load() < 50 {
			time.Sleep(time.Millisecond)
		}
		shutdownStart.Store(time.Now().UnixNano())
		lc.requestStop("test")
		<-ctx.Done()
	})
	elapsed := time.Since(time.Unix(0, shutdownStart.Load()))
	if code != exitOK {
		t.Fatalf("exit code = %d\n%s", code, stderr)
	}
	if elapsed >= shutdownTimeout {
		t.Errorf("shutdown took %v, want under %v", elapsed, shutdownTimeout)
	}
	if !strings.Contains(stderr, "event=store.close") {
		t.Errorf("store did not close:\n%s", stderr)
	}

	// Every write the store acknowledged is there after a reopen.
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath(dir))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int64
	if err := db.QueryRow("SELECT count(*) FROM messages").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < acked.Load() {
		t.Errorf("%d messages stored, %d were acknowledged", n, acked.Load())
	}
}
