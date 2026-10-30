package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fileNames(t testing.TB, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestLowDiskRefusesWritesAndKeepsEveryFile(t *testing.T) {
	free := uint64(diskReserve - 1)
	now := time.Unix(5000, 0)
	dir := newDataDir(t)
	st, _ := openTestWith(t, dir, openOptions{hooks: testHooks{free: func(string) (uint64, error) { return free, nil }}})
	st.disk.now = func() time.Time { return now }
	names := strings.Join(fileNames(t, dir), ",")
	ctx := context.Background()

	if err := st.StoreMessages(ctx, []Message{msg(alice, "A", alice, at(0))}); !errors.Is(err, ErrDiskLow) {
		t.Errorf("StoreMessages: %v, want ErrDiskLow", err)
	}
	if err := st.UpdateChats(ctx, []ChatUpdate{{JID: alice}}); !errors.Is(err, ErrDiskLow) {
		t.Errorf("UpdateChats: %v, want ErrDiskLow", err)
	}
	if countRows(t, st, "messages") != 0 || countRows(t, st, "chats") != 0 {
		t.Error("a refused write left rows")
	}
	if got := strings.Join(fileNames(t, dir), ","); got != names {
		t.Errorf("files changed: %s, was %s", got, names)
	}

	// Writes resume only once 512 MiB is free.
	free, now = diskResume-1, now.Add(time.Minute)
	if err := st.StoreMessage(ctx, msg(alice, "A", alice, at(0))); !errors.Is(err, ErrDiskLow) {
		t.Errorf("below the resume level: %v, want ErrDiskLow", err)
	}
	free, now = diskResume, now.Add(time.Minute)
	if err := st.StoreMessage(ctx, msg(alice, "A", alice, at(0))); err != nil {
		t.Errorf("at the resume level: %v", err)
	}
}

func TestDiskFullIsReportedAndRecoverable(t *testing.T) {
	dir := newDataDir(t)
	st, recs := openTest(t, dir)
	mustStore(t, st, msg(alice, "before", alice, at(0)))
	var pages int
	if err := st.writer.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	// The writer pool has one connection, so the limit stays in force for the next call.
	if _, err := st.writer.Exec(fmt.Sprintf("PRAGMA max_page_count = %d", pages+2)); err != nil {
		t.Fatal(err)
	}
	big := msg(alice, "big", alice, at(1), withText(strings.Repeat("x", maxTextBytes)))
	err := st.StoreMessage(context.Background(), big)
	var se *StorageError
	if !errors.Is(err, ErrIO) || !errors.As(err, &se) || se.Code != "disk_full" {
		t.Fatalf("a write on a full database: %v, want ErrIO disk_full", err)
	}
	if len(recs.events("store.io_error")) == 0 {
		t.Error("no store.io_error event")
	}
	for _, s := range []string{"", "-wal"} {
		if _, err := os.Stat(filepath.Join(dir, dbFileName+s)); err != nil {
			t.Errorf("%s: %v", dbFileName+s, err)
		}
	}
	if !messageExists(t, st, alice, "before") || messageExists(t, st, alice, "big") {
		t.Error("the earlier commit is gone or the failed write stayed")
	}
	if _, err := st.writer.Exec("PRAGMA max_page_count = 1000000"); err != nil {
		t.Fatal(err)
	}
	if err := st.StoreMessage(context.Background(), big); err != nil {
		t.Errorf("after the limit was raised: %v", err)
	}
}

func TestReadOnlyDatabaseIsRefusedSafely(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	path := closedDB(t)
	dir := filepath.Dir(path)
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	before := snapshotDir(t, dir)
	_, err := Open(context.Background(), dir, nil)
	var se *StorageError
	if !errors.Is(err, ErrIO) || !errors.As(err, &se) || se.Code != "readonly" {
		t.Fatalf("Open on a read-only database: %v, want ErrIO readonly", err)
	}
	// SQLite may leave empty sidecars of its own, but nothing that was there is deleted or changed.
	after := snapshotDir(t, dir)
	for name, state := range before {
		if after[name] != state {
			t.Errorf("%s changed or was deleted", name)
		}
	}
}

// writeLarge stores n batches of 16 messages of 256 KiB, 4 MiB each.
func writeLarge(t *testing.T, st *Store, from, n int) {
	t.Helper()
	text := strings.Repeat("w", maxTextBytes)
	for b := from; b < from+n; b++ {
		batch := make([]Message, 16)
		for i := range batch {
			batch[i] = msg(groupG, fmt.Sprintf("large-%d-%d", b, i), alice, at(b), withText(text))
		}
		mustStore(t, st, batch...)
	}
}

func TestWALStaysWithinItsLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("writes about 90 MiB")
	}
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	writeLarge(t, st, 0, 22)
	if _, err := st.writer.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, dbFileName+"-wal"))
	if err == nil && fi.Size() > walLimitBytes {
		t.Errorf("-wal is %d bytes after a checkpoint, want at most %d", fi.Size(), walLimitBytes)
	}
	if n := countRows(t, st, "messages"); n != 22*16 {
		t.Errorf("messages = %d, want %d", n, 22*16)
	}
}

// TestLongReaderEndsAtTheOperationLimit holds a snapshot open while writes continue. The reader's operation ends
// with a deadline error at the operation limit, and after it ends a checkpoint brings the log back within bounds.
func TestLongReaderEndsAtTheOperationLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("writes about 40 MiB")
	}
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	mustStore(t, st, msg(alice, "seed", alice, at(0)))

	started := make(chan struct{})
	done := make(chan error, 1)
	readerCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go func() {
		done <- st.read(readerCtx, "long", func(ctx context.Context, conn *sql.Conn) error {
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM messages").Scan(&n); err != nil {
				return err
			}
			close(started)
			<-ctx.Done() // the snapshot stays open until the deadline
			return ctx.Err()
		})
	}()
	<-started
	writeLarge(t, st, 0, 10)

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("the long reader ended with %v, want a deadline error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the long reader never ended")
	}
	if _, err := st.writer.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, dbFileName+"-wal")); err == nil && fi.Size() > walLimitBytes {
		t.Errorf("-wal is %d bytes after the reader ended", fi.Size())
	}
}
