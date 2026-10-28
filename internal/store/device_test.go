package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// rawDB opens the database file directly, for putting it into a state the store would never create.
func rawDB(t testing.TB, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", fileDSN(filepath.Join(dir, dbFileName), "_pragma=journal_mode(WAL)"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func deviceVersion(t testing.TB, st *Store) int {
	t.Helper()
	var v int
	if err := st.reader.QueryRow("SELECT version FROM whatsmeow_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDeviceUpgradeFromPreviousVersion(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	dev := seedDevice(t, st)
	seedContacts(t, dev, contactSeed{jid: "491511234567@s.whatsapp.net", full: "Alice Example"})
	st.Close(context.Background())

	db := rawDB(t, dir)
	exec(t, db, "ALTER TABLE whatsmeow_chat_settings DROP COLUMN wasa_root_secret_id")
	exec(t, db, "UPDATE whatsmeow_version SET version = 15, compat = 8")
	db.Close()

	again, recs := openTest(t, dir)
	if got := deviceVersion(t, again); got != 16 {
		t.Errorf("device version = %d, want 16", got)
	}
	var cols int
	if err := again.reader.QueryRow("SELECT count(*) FROM pragma_table_info('whatsmeow_chat_settings') WHERE name = 'wasa_root_secret_id'").Scan(&cols); err != nil || cols != 1 {
		t.Errorf("the column is back = %d, %v", cols, err)
	}
	var n int
	if err := again.reader.QueryRow("SELECT count(*) FROM whatsmeow_contacts").Scan(&n); err != nil || n != 1 {
		t.Errorf("device data after the upgrade = %d contacts, %v; want it kept", n, err)
	}
	ev := recs.events("store.upgrade")
	if len(ev) != 1 || ev[0].attrs["from"] != "15" || ev[0].attrs["to"] != "16" {
		t.Errorf("store.upgrade events = %v", ev)
	}
}

func TestDeviceUpgradeFailureStopsStartup(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	st.Close(context.Background())

	// Version 15 without dropping the column: the v16 ALTER fails because the column exists.
	db := rawDB(t, dir)
	exec(t, db, "UPDATE whatsmeow_version SET version = 15, compat = 8")
	db.Close()

	logger, _ := newTestLogger()
	s, err := open(context.Background(), dir, logger, openOptions{})
	var me *MigrationError
	if !errors.As(err, &me) || me.Schema != "device" {
		t.Fatalf("open = %v, %v; want a device *MigrationError", s, err)
	}
	if s != nil {
		t.Error("a store was returned with a failed upgrade")
	}
	if !strings.Contains(err.Error(), "device 15") {
		t.Errorf("error %q lacks the completed version", err)
	}
	check := rawDB(t, dir)
	var dv, mv int
	if err := check.QueryRow("SELECT version FROM whatsmeow_version").Scan(&dv); err != nil || dv != 15 {
		t.Errorf("whatsmeow_version = %d, %v; want 15", dv, err)
	}
	if err := check.QueryRow("SELECT version FROM mcp_version").Scan(&mv); err != nil || mv != 1 {
		t.Errorf("mcp_version = %d, %v; the message migrations must not have run", mv, err)
	}
}

func TestDeviceSucceedsAndMessageMigrationFails(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTestWith(t, dir, openOptions{migrations: testMigrations(t, goodThird)[:2]})
	st.Close(context.Background())

	db := rawDB(t, dir)
	exec(t, db, "ALTER TABLE whatsmeow_chat_settings DROP COLUMN wasa_root_secret_id")
	exec(t, db, "UPDATE whatsmeow_version SET version = 15, compat = 8")
	db.Close()

	_, err := open(context.Background(), dir, nil, openOptions{migrations: testMigrations(t, failingThird)})
	var me *MigrationError
	if !errors.As(err, &me) || me.Schema != "messages" || me.Completed != "device 16, messages 2" {
		t.Fatalf("open = %v; want a messages MigrationError completed at device 16, messages 2", err)
	}
	check := rawDB(t, dir)
	var dv, mv int
	check.QueryRow("SELECT version FROM whatsmeow_version").Scan(&dv)
	check.QueryRow("SELECT version FROM mcp_version").Scan(&mv)
	if dv != 16 || mv != 2 {
		t.Errorf("versions = device %d, messages %d; want 16 and 2", dv, mv)
	}
}
