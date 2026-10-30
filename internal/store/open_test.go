package store

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

func TestOpenFreshDirectory(t *testing.T) {
	dir := newDataDir(t)
	st, recs := openTest(t, dir)

	var device, messages int
	if err := st.reader.QueryRow("SELECT version FROM whatsmeow_version").Scan(&device); err != nil || device != 16 {
		t.Errorf("whatsmeow_version = %d, %v; want 16", device, err)
	}
	if err := st.reader.QueryRow("SELECT version FROM mcp_version").Scan(&messages); err != nil || messages != 1 {
		t.Errorf("mcp_version = %d, %v; want 1", messages, err)
	}
	if got := len(recs.events("store.ready")); got != 1 {
		t.Errorf("store.ready events = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "whatsapp.db")); err != nil {
		t.Error(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestReopenKeepsVersionsAndData(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	mustStore(t, st, msg("491511234567@s.whatsapp.net", "A", "491511234567@s.whatsapp.net", at(0)))
	st.Close(context.Background())

	again, recs := openTest(t, dir)
	if got := mcpVersion(t, again); got != 1 || deviceVersion(t, again) != 16 {
		t.Errorf("versions = %d / %d", got, deviceVersion(t, again))
	}
	if !messageExists(t, again, "491511234567@s.whatsapp.net", "A") {
		t.Error("message lost on reopen")
	}
	if n := len(recs.events("store.migrate")); n != 0 {
		t.Errorf("a reopen ran %d migration steps", n)
	}
}

func TestOpenNotADatabase(t *testing.T) {
	dir := newDataDir(t)
	random := make([]byte, 4096)
	for i := range random {
		random[i] = byte(i*31 + 7)
	}
	path := filepath.Join(dir, dbFileName)
	if err := os.WriteFile(path, random, 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotDir(t, dir)
	_, err := Open(context.Background(), dir, nil)
	if !errors.Is(err, ErrNotDatabase) {
		t.Fatalf("random bytes: %v, want ErrNotDatabase", err)
	}
	if !strings.Contains(err.Error(), "is not a usable SQLite database") {
		t.Errorf("text = %q", err)
	}
	if after := snapshotDir(t, dir); !maps.Equal(before, after) {
		t.Error("the file changed")
	}
}

func TestOpenTruncatedDatabase(t *testing.T) {
	path := closedDB(t)
	dir := filepath.Dir(path)
	if err := os.Truncate(path, 1000); err != nil {
		t.Fatal(err)
	}
	before := snapshotDir(t, dir)
	_, err := Open(context.Background(), dir, nil)
	if !errors.Is(err, ErrNotDatabase) {
		t.Fatalf("truncated database: %v, want ErrNotDatabase", err)
	}
	if after := snapshotDir(t, dir); !maps.Equal(before, after) {
		t.Error("the file changed")
	}
}

func TestOpenIgnoresOldFiles(t *testing.T) {
	dir := newDataDir(t)
	old := filepath.Join(dir, "store")
	if err := private.MkdirAll(old); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"whatsapp.db", "messages.db"} {
		if err := os.WriteFile(filepath.Join(old, n), []byte("old deployment"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotDir(t, old)
	st, _ := openTest(t, dir)
	st.Close(context.Background())
	if after := snapshotDir(t, old); !maps.Equal(before, after) {
		t.Error("an old file changed")
	}
}

func TestOpenZeroLengthFileIsFresh(t *testing.T) {
	dir := newDataDir(t)
	if err := os.WriteFile(filepath.Join(dir, dbFileName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := openTest(t, dir)
	if mcpVersion(t, st) != 1 {
		t.Error("an empty file was not initialised")
	}
}

// pinned returns n connections of a pool, all open at once.
func pinned(t *testing.T, db *sql.DB, n int) []*sql.Conn {
	t.Helper()
	var conns []*sql.Conn
	for i := 0; i < n; i++ {
		c, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		t.Cleanup(func() { c.Close() })
	}
	return conns
}

func TestEveryConnectionHasItsPragmas(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	for name, conns := range map[string][]*sql.Conn{
		"writer": pinned(t, st.writer, 1),
		"reader": pinned(t, st.reader, readConns),
	} {
		for i, c := range conns {
			var limit, temp int64
			if err := c.QueryRowContext(ctx, "PRAGMA hard_heap_limit").Scan(&limit); err != nil || limit != maxHeapBytes {
				t.Errorf("%s %d: hard_heap_limit = %d, %v", name, i, limit, err)
			}
			if err := c.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&temp); err != nil || temp != 2 {
				t.Errorf("%s %d: temp_store = %d, %v; want 2 (memory)", name, i, temp, err)
			}
			var fk int
			if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
				t.Errorf("%s %d: foreign_keys = %d, %v", name, i, fk, err)
			}
		}
	}
}

func TestReaderPoolConnections(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	for i, c := range pinned(t, st.reader, readConns) {
		var got string
		if err := c.QueryRowContext(ctx, "SELECT mcp_fold('ÉTÉ')").Scan(&got); err != nil || got != "été" {
			t.Errorf("reader %d: mcp_fold = %q, %v", i, got, err)
		}
		tx, err := c.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Errorf("reader %d: read-only BeginTx: %v", i, err)
			continue
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM chats").Scan(&n); err != nil {
			t.Errorf("reader %d: query in a read-only transaction: %v", i, err)
		}
		tx.Rollback()
		_, err = c.ExecContext(ctx, "INSERT INTO chats (jid) VALUES ('x@lid')")
		if sqliteCode(err) != sqliteReadonly {
			t.Errorf("reader %d: write error = %v, want SQLITE_READONLY", i, err)
		}
	}
}

func TestNoTemporaryFilesOnDisk(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	exec(t, st.writer, "CREATE TABLE scratch (id INTEGER PRIMARY KEY, a TEXT, b INTEGER)")
	exec(t, st.writer, "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 50000) "+
		"INSERT INTO scratch SELECT i, hex(randomblob(64)), i * 7919 % 1000 FROM n")
	listing := func() string {
		var names []string
		for _, d := range []string{dir, os.TempDir(), os.Getenv("SQLITE_TMPDIR")} {
			if d == "" {
				continue
			}
			entries, _ := os.ReadDir(d)
			for _, e := range entries {
				if d == dir || strings.HasPrefix(e.Name(), "etilqs_") {
					names = append(names, d+"/"+e.Name())
				}
			}
		}
		sort.Strings(names)
		return strings.Join(names, "\n")
	}
	before := listing()
	rows, err := st.reader.Query("SELECT a FROM scratch ORDER BY substr(a, 3, 20) || b")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		n++
	}
	rows.Close()
	if n != 50000 {
		t.Fatalf("sorted %d rows", n)
	}
	exec(t, st.writer, "CREATE INDEX scratch_b ON scratch (b, a)")
	if after := listing(); after != before {
		t.Errorf("a sort or index build created files:\n before %s\n after  %s", before, after)
	}
}

func TestPrivateArtifacts(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	mustStore(t, st, msg("491511234567@s.whatsapp.net", "A", "491511234567@s.whatsapp.net", at(0)))
	for _, s := range []string{"", "-wal", "-shm"} {
		p := filepath.Join(dir, dbFileName+s)
		if _, err := os.Stat(p); err != nil {
			if s == "" {
				t.Fatal(err)
			}
			continue // a sidecar may already be gone
		}
		if _, err := private.CheckFile(p); err != nil {
			t.Errorf("%s: %v", dbFileName+s, err)
		}
	}
}
