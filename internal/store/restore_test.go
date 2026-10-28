package store

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"
)

const journalChildEnv = "GO_STORE_JOURNAL_CHILD"

// seedBackupSource builds a store with a synthetic device and 5,000 messages, closes it cleanly, and returns
// its directory.
func seedBackupSource(t *testing.T) string {
	t.Helper()
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	seedDevice(t, st)
	for b := 0; b < 5; b++ {
		batch := make([]Message, 1000)
		for i := range batch {
			n := b*1000 + i
			chat := alice
			if n%3 == 0 {
				chat = groupG
			}
			batch[i] = msg(chat, fmt.Sprintf("r-%d", n), alice, at(n%900), withText(fmt.Sprintf("restore body %d", n)))
		}
		mustStore(t, st, batch...)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	return dir
}

// copyFileSet copies the database and any sidecar into a new private directory.
func copyFileSet(t *testing.T, from string) string {
	t.Helper()
	to := newDataDir(t)
	for _, s := range []string{"", "-wal", "-shm", "-journal"} {
		b, err := os.ReadFile(filepath.Join(from, dbFileName+s))
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(to, dbFileName+s), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return to
}

// TestRestoredBackupIsValidatedOffline follows the restore procedure: copy the quiesced file set to a new
// private directory, check it with plain SQLite, then open it with the store and compare it with the original.
func TestRestoredBackupIsValidatedOffline(t *testing.T) {
	src := seedBackupSource(t)
	restored := copyFileSet(t, src)

	db := rawDB(t, restored)
	var check string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Errorf("integrity_check = %q, %v", check, err)
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Error("foreign_key_check reported a row")
	}
	rows.Close()
	var mv, dv, dc, n int
	db.QueryRow("SELECT version FROM mcp_version").Scan(&mv)
	db.QueryRow("SELECT version, compat FROM whatsmeow_version").Scan(&dv, &dc)
	db.QueryRow("SELECT count(*) FROM messages").Scan(&n)
	if mv != 1 || dv != 16 || n != 5000 {
		t.Errorf("versions %d / %d, %d messages; want 1 / 16 and 5000", mv, dv, n)
	}
	db.Close()

	orig, _ := openTest(t, src)
	want := tableDump(t, orig)
	orig.Close(context.Background())
	again, _ := openTest(t, restored)
	if got := tableDump(t, again); got != want {
		t.Error("the restored copy differs from the original")
	}
}

// TestJournalChild is the child of the hot-journal case: it starts a rollback-journal transaction that spills
// pages, reports READY, and waits to be killed before it commits.
func TestJournalChild(t *testing.T) {
	dir := os.Getenv(journalChildEnv)
	if dir == "" {
		t.Skip("runs as a child of the restore test")
	}
	db, err := sql.Open("sqlite", fileDSN(filepath.Join(dir, dbFileName), "_pragma=journal_mode(DELETE)&_pragma=cache_size(10)"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE messages SET text = text || hex(randomblob(1500)), text_fold = text_fold || hex(randomblob(1500))"); err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	time.Sleep(60 * time.Second)
}

func hotJournal(t *testing.T, dir string) {
	t.Helper()
	cmd := osexec.Command(os.Args[0], "-test.run=^TestJournalChild$", "-test.timeout=90s")
	cmd.Env = append(os.Environ(), journalChildEnv+"="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := false
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if sc.Text() == "READY" {
			ready = true
			break
		}
	}
	cmd.Process.Kill()
	cmd.Wait()
	if !ready {
		t.Fatal("the child never started its transaction")
	}
	if fi, err := os.Stat(filepath.Join(dir, dbFileName+"-journal")); err != nil || fi.Size() == 0 {
		t.Fatalf("no hot journal was left: %v", err)
	}
}

// TestRollbackJournalRecoveryProcedure follows OPERATIONS.md: the store refuses a start with a journal; the
// operator copies the file set aside, opens the live database once with SQLite, which rolls back a hot journal,
// moves any journal that is left into the copy, and starts again.
func TestRollbackJournalRecoveryProcedure(t *testing.T) {
	src := seedBackupSource(t)
	orig, _ := openTest(t, src)
	want := tableDump(t, orig)
	orig.Close(context.Background())

	cases := map[string]func(dir string){
		"empty":  func(dir string) { os.WriteFile(filepath.Join(dir, dbFileName+"-journal"), nil, 0o600) },
		"zeroed": func(dir string) { os.WriteFile(filepath.Join(dir, dbFileName+"-journal"), make([]byte, 512), 0o600) },
		"hot":    func(dir string) { hotJournal(t, dir) },
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			live := copyFileSet(t, src)
			prepare(live)
			wantStorage(t, live, "rollback_journal", dbFileName+"-journal")

			aside := copyFileSet(t, live) // step: copy the whole file set to a private place
			db := rawDB(t, live)          // step: open the live database once through SQLite
			var check string
			if err := db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
				t.Fatalf("integrity_check = %q, %v", check, err)
			}
			db.Close()
			if _, err := os.Stat(filepath.Join(live, dbFileName+"-journal")); err == nil {
				// SQLite leaves a journal that is not hot; move it into the private copy.
				if err := os.Rename(filepath.Join(live, dbFileName+"-journal"), filepath.Join(aside, "left-over-journal")); err != nil {
					t.Fatal(err)
				}
			}
			st, _ := openTest(t, live)
			if got := tableDump(t, st); got != want {
				t.Error("after recovery the data differs from the state before the killed transaction")
			}
		})
	}
}
