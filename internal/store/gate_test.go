package store

import (
	"database/sql"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	_ "modernc.org/sqlite"
)

const (
	gateCheckpointedRows = 100 // rows checkpointed into the main file
	gateWALRows          = 50  // further rows, one transaction each, committed only to the log
	gateDB               = "whatsapp.db"
)

// gateFixtures builds every file state the committed view must read correctly, each in its own directory under
// root, and returns the directories by name.
func gateFixtures(t testing.TB, root string) map[string]string {
	t.Helper()
	dirs := map[string]string{}
	newDir := func(name string) string {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		dirs[name] = d
		return d
	}

	src := filepath.Join(root, "source")
	db := openSource(t, src)
	writeRows(t, db, 1, gateCheckpointedRows, 1)
	exec(t, db, "PRAGMA wal_checkpoint(TRUNCATE)")
	writeRows(t, db, gateCheckpointedRows+1, gateWALRows, 1)

	// Copies taken while the source is still open are what a crash leaves behind.
	copyFiles(t, src, newDir("wal"), "", "-wal")
	copyFiles(t, src, newDir("wal+shm"), "", "-wal", "-shm")
	copyFiles(t, src, newDir("wal+zeroed-shm"), "", "-wal")
	writeZeros(t, filepath.Join(dirs["wal+zeroed-shm"], gateDB+"-shm"), fileSize(t, filepath.Join(src, gateDB+"-shm")))
	copyFiles(t, src, newDir("torn-last-frame"), "", "-wal")
	walPath := filepath.Join(dirs["torn-last-frame"], gateDB+"-wal")
	if err := os.Truncate(walPath, fileSize(t, walPath)-(walFrameHeaderSize+4096)/2); err != nil {
		t.Fatal(err)
	}

	// A clean close checkpoints the log into the main file and deletes -wal and -shm.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	copyFiles(t, src, newDir("clean"), "")
	copyFiles(t, src, newDir("empty-wal"), "")
	writeZeros(t, filepath.Join(dirs["empty-wal"], gateDB+"-wal"), 0)
	copyFiles(t, src, newDir("empty-wal+empty-shm"), "")
	writeZeros(t, filepath.Join(dirs["empty-wal+empty-shm"], gateDB+"-wal"), 0)
	writeZeros(t, filepath.Join(dirs["empty-wal+empty-shm"], gateDB+"-shm"), 0)

	// A log restarted after a full checkpoint: new frames under new salts, with stale frames behind them.
	src2 := filepath.Join(root, "source-restart")
	db2 := openSource(t, src2)
	writeRows(t, db2, 1, gateCheckpointedRows, 1)
	exec(t, db2, "PRAGMA wal_checkpoint(TRUNCATE)")
	writeRows(t, db2, gateCheckpointedRows+1, gateWALRows, 1)
	exec(t, db2, "PRAGMA wal_checkpoint(PASSIVE)")
	writeRows(t, db2, gateCheckpointedRows+gateWALRows+1, 10, 1)
	copyFiles(t, src2, newDir("restarted-wal"), "", "-wal")
	if err := db2.Close(); err != nil {
		t.Fatal(err)
	}
	return dirs
}

func openSource(t testing.TB, dir string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, gateDB)+
		"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)&_pragma=synchronous(NORMAL)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	exec(t, db, "CREATE TABLE t (id INTEGER PRIMARY KEY, v BLOB NOT NULL)")
	return db
}

// writeRows inserts n rows from id first on, perTx rows per transaction.
func writeRows(t testing.TB, db *sql.DB, first, n, perTx int) {
	t.Helper()
	for i := 0; i < n; i += perTx {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for j := i; j < min(i+perTx, n); j++ {
			if _, err := tx.Exec("INSERT INTO t VALUES (?, randomblob(300))", first+j); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func exec(t testing.TB, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// copyFiles copies whatsapp.db plus each named suffix from src to dst.
func copyFiles(t testing.TB, src, dst string, suffixes ...string) {
	t.Helper()
	for _, s := range suffixes {
		b, err := os.ReadFile(filepath.Join(src, gateDB+s))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, gateDB+s), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeZeros(t testing.TB, path string, n int64) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileSize(t testing.TB, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

type rowSummary struct{ count, maxID int }

func summarize(t testing.TB, dsn string) rowSummary {
	t.Helper()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var s rowSummary
	if err := db.QueryRow("SELECT count(*), coalesce(max(id), 0) FROM t").Scan(&s.count, &s.maxID); err != nil {
		t.Fatalf("query through %s: %v", dsn, err)
	}
	return s
}

func TestCommittedViewChangesNothing(t *testing.T) {
	root := t.TempDir()
	fixtures := gateFixtures(t, filepath.Join(root, "fixtures"))
	want := map[string]rowSummary{
		"clean":               {150, 150},
		"empty-wal":           {150, 150},
		"empty-wal+empty-shm": {150, 150},
		"wal":                 {150, 150},
		"wal+shm":             {150, 150},
		"wal+zeroed-shm":      {150, 150},
		"torn-last-frame":     {149, 149},
		"restarted-wal":       {160, 160},
	}
	for _, name := range slices.Sorted(maps.Keys(fixtures)) {
		t.Run(name, func(t *testing.T) {
			dir := fixtures[name]
			before := snapshotDir(t, dir)

			dsn, release, err := openCommittedView(filepath.Join(dir, gateDB))
			if err != nil {
				t.Fatal(err)
			}
			got := summarize(t, dsn)
			if err := release(); err != nil {
				t.Fatal(err)
			}

			if after := snapshotDir(t, dir); !maps.Equal(before, after) {
				t.Errorf("files changed:\nbefore %v\nafter  %v", before, after)
			}

			// SQLite's own answer, from a throwaway copy it may recover and rewrite.
			scratch := t.TempDir()
			for rel := range before {
				copyFiles(t, dir, scratch, rel[len(gateDB):])
			}
			sqliteSays := summarize(t, "file:"+filepath.Join(scratch, gateDB))
			if got != sqliteSays {
				t.Errorf("view read %+v, SQLite reads %+v", got, sqliteSays)
			}
			if got != want[name] {
				t.Errorf("view read %+v, want %+v", got, want[name])
			}
		})
	}

	// The restarted log must really hold stale frames behind the live ones, or that fixture tests nothing new.
	wal, err := os.ReadFile(filepath.Join(fixtures["restarted-wal"], gateDB+"-wal"))
	if err != nil {
		t.Fatal(err)
	}
	idx := mustScan(t, wal, 4096)
	if whole := (len(wal) - walHeaderSize) / (walFrameHeaderSize + 4096); len(idx.frames) >= whole {
		t.Errorf("restarted log: %d indexed pages of %d frames; expected stale frames", len(idx.frames), whole)
	}
}

func mustScan(t testing.TB, wal []byte, pageSize int64) walIndex {
	t.Helper()
	idx, err := scanWAL(bytesReader(wal), pageSize)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}
