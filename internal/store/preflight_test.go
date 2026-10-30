package store

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stateDirs builds two data directories holding a store that was modified by mutate: one after a clean close, the
// other as a crash leaves it, with the modification only in an un-checkpointed log.
func stateDirs(t *testing.T, mutate ...string) (clean, wal string) {
	t.Helper()
	src := newDataDir(t)
	st, _ := openTest(t, src)
	seedDevice(t, st)
	mustStore(t, st, msg("491511234567@s.whatsapp.net", "A", "491511234567@s.whatsapp.net", at(0)))
	st.Close(context.Background())

	db, err := sql.Open("sqlite", fileDSN(filepath.Join(src, dbFileName), "_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, q := range mutate {
		exec(t, db, q)
	}
	wal = newDataDir(t)
	copyFiles(t, src, wal, "", "-wal")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	clean = src
	return clean, wal
}

func TestPreflightRefusalsChangeNothing(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate []string
		want   error
		text   []string
	}{
		{"message schema too new", []string{"UPDATE mcp_version SET version = 2"}, ErrSchemaTooNew,
			[]string{"message schema", "2", "(1)"}},
		{"device schema too new", []string{"UPDATE whatsmeow_version SET compat = 17"}, ErrSchemaTooNew,
			[]string{"device schema", "17", "16"}},
		{"mcp_version without rows", []string{"DELETE FROM mcp_version"}, ErrSchemaInvalid, []string{"message schema"}},
		{"mcp_version with two rows", []string{"INSERT INTO mcp_version VALUES (1)"}, ErrSchemaInvalid, []string{"message schema"}},
		{"whatsmeow_version without rows", []string{"DELETE FROM whatsmeow_version"}, ErrSchemaInvalid, []string{"device schema"}},
		{"whatsmeow_version with two rows", []string{"INSERT INTO whatsmeow_version SELECT * FROM whatsmeow_version"}, ErrSchemaInvalid, []string{"device schema"}},
		{"chats without mcp_version", []string{"DROP TABLE mcp_version"}, ErrSchemaInvalid, []string{"message schema"}},
		{"device tables without whatsmeow_version", []string{"DROP TABLE whatsmeow_version"}, ErrSchemaInvalid, []string{"device schema"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			clean, wal := stateDirs(t, c.mutate...)
			for state, dir := range map[string]string{"clean": clean, "wal": wal} {
				before := snapshotDir(t, dir)
				_, err := open(context.Background(), dir, nil, openOptions{})
				if !errors.Is(err, c.want) {
					t.Fatalf("%s: open = %v, want %v", state, err, c.want)
				}
				for _, s := range c.text {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("%s: error %q lacks %q", state, err, s)
					}
				}
				if after := snapshotDir(t, dir); !maps.Equal(before, after) {
					t.Errorf("%s: the directory changed during a refused open:\n before %v\n after  %v", state, before, after)
				}
			}
		})
	}
}

func TestPreflightInterruptedFirstStartIsNotRefused(t *testing.T) {
	// dbutil creates whatsmeow_version in its own statement before the first upgrade step, so a start killed in
	// between leaves an empty version table and no other device table.
	src := newDataDir(t)
	st, _ := openTest(t, src)
	st.Close(context.Background())
	db, err := sql.Open("sqlite", fileDSN(filepath.Join(src, dbFileName), "_pragma=journal_mode(WAL)"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'whatsmeow_%' AND name <> 'whatsmeow_version'")
	if err != nil {
		t.Fatal(err)
	}
	var drop []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		drop = append(drop, n)
	}
	rows.Close()
	for _, n := range drop {
		exec(t, db, "DROP TABLE "+n)
	}
	exec(t, db, "DELETE FROM whatsmeow_version")
	db.Close()

	again, _ := openTest(t, src)
	var v int
	if err := again.reader.QueryRow("SELECT version FROM whatsmeow_version").Scan(&v); err != nil || v != 16 {
		t.Errorf("device version = %d, %v; want the first upgrade to run and reach 16", v, err)
	}
}

func TestOpenFreshSchemaWithWALHoldingCommittedData(t *testing.T) {
	// A start after a crash: the log holds the only copy of recent commits, and the store keeps them.
	_, wal := stateDirs(t, "UPDATE chats SET name = 'from the log', name_fold = 'from the log', name_quality = 3, name_time = 946684800000")
	st, _ := openTest(t, wal)
	var name string
	if err := st.reader.QueryRow("SELECT name FROM chats").Scan(&name); err != nil || name != "from the log" {
		t.Errorf("name = %q, %v; want the committed value from the log", name, err)
	}
}

func TestCheckHeader(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	random := make([]byte, 4096)
	for i := range random {
		random[i] = byte(i*7 + 3)
	}
	if err := checkHeader(write("random.db", random)); !errors.Is(err, ErrNotDatabase) {
		t.Errorf("random bytes: %v", err)
	}
	if err := checkHeader(write("short.db", []byte("SQLite format 3\x00"))); !errors.Is(err, ErrNotDatabase) {
		t.Errorf("short file: %v", err)
	}
	good := append([]byte("SQLite format 3\x00"), make([]byte, 4096)...)
	good[16], good[17] = 0x10, 0x00 // page size 4096
	if err := checkHeader(write("good.db", good)); err != nil {
		t.Errorf("valid header: %v", err)
	}
	good[16], good[17] = 0x03, 0xe9 // 1001, not a power of two
	if err := checkHeader(write("bad-size.db", good)); !errors.Is(err, ErrNotDatabase) {
		t.Errorf("bad page size: %v", err)
	}
}
