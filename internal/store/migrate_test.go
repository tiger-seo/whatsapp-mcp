package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

// testMigrations is the real first migration plus a table and a column, as the runner sees a database that has
// evolved twice.
func testMigrations(t testing.TB, third string) []migration {
	t.Helper()
	real, err := loadMigrations(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	return []migration{
		real[0],
		{version: 2, name: "add-table", sql: "CREATE TABLE extra (id INTEGER PRIMARY KEY) STRICT;"},
		{version: 3, name: "add-column", sql: third},
	}
}

const goodThird = "ALTER TABLE chats ADD COLUMN note TEXT;"

// failingThird is valid for one statement and then invalid, so a partial application would be visible.
const failingThird = "ALTER TABLE chats ADD COLUMN note TEXT; ALTER TABLE chats ADD COLUMN note TEXT;"

func mcpVersion(t testing.TB, st *Store) int {
	t.Helper()
	var v int
	if err := st.reader.QueryRow("SELECT version FROM mcp_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func hasColumnT(t testing.TB, st *Store, table, column string) bool {
	t.Helper()
	ok, err := hasColumn(context.Background(), st.reader, table, column)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestMigrateFreshAppliesInOrder(t *testing.T) {
	st, recs := openTestWith(t, newDataDir(t), openOptions{migrations: testMigrations(t, goodThird)})
	if got := mcpVersion(t, st); got != 3 {
		t.Errorf("version = %d, want 3", got)
	}
	if !hasColumnT(t, st, "chats", "note") {
		t.Error("migration 3 did not run")
	}
	var names []string
	for _, r := range recs.events("store.migrate") {
		names = append(names, r.attrs["to"]+":"+r.attrs["name"])
	}
	if got := strings.Join(names, ","); got != "1:init,2:add-table,3:add-column" {
		t.Errorf("migrate events = %s", got)
	}
}

func TestMigrateResumesFromStoredVersion(t *testing.T) {
	dir := newDataDir(t)
	list := testMigrations(t, goodThird)
	first, _ := openTestWith(t, dir, openOptions{migrations: list[:2]})
	mustStore(t, first, msg("491511234567@s.whatsapp.net", "A", "491511234567@s.whatsapp.net", at(0)))
	first.Close(context.Background())

	second, recs := openTestWith(t, dir, openOptions{migrations: list})
	if got := mcpVersion(t, second); got != 3 {
		t.Errorf("version = %d, want 3", got)
	}
	if n := len(recs.events("store.migrate")); n != 1 {
		t.Errorf("migrations run = %d, want only the third", n)
	}
	if !messageExists(t, second, "491511234567@s.whatsapp.net", "A") {
		t.Error("seeded message lost")
	}
}

func TestMigrateFailureRollsBackItsStep(t *testing.T) {
	dir := newDataDir(t)
	list := testMigrations(t, goodThird)
	first, _ := openTestWith(t, dir, openOptions{migrations: list[:2]})
	mustStore(t, first, msg("491511234567@s.whatsapp.net", "A", "491511234567@s.whatsapp.net", at(0)))
	first.Close(context.Background())

	_, err := open(context.Background(), dir, nil, openOptions{migrations: testMigrations(t, failingThird)})
	var me *MigrationError
	if !errors.As(err, &me) {
		t.Fatalf("open = %v, want a *MigrationError", err)
	}
	if me.Schema != "messages" || !strings.Contains(me.Step, "0003") || me.Completed != "device 16, messages 2" {
		t.Errorf("error = %+v, want step 0003 and completed device 16, messages 2", me)
	}
	if !strings.Contains(err.Error(), "0003") || !strings.Contains(err.Error(), "completed: device 16, messages 2") {
		t.Errorf("text = %q", err)
	}

	// The failed step left nothing behind, and a retry with a fixed migration resumes from 2.
	retry, _ := openTestWith(t, dir, openOptions{migrations: list})
	if got := mcpVersion(t, retry); got != 3 {
		t.Errorf("after the retry version = %d, want 3", got)
	}
	if !messageExists(t, retry, "491511234567@s.whatsapp.net", "A") {
		t.Error("seeded message lost")
	}
}

func TestMigrateFailureLeavesNoPartialChange(t *testing.T) {
	dir := newDataDir(t)
	list := testMigrations(t, goodThird)
	first, _ := openTestWith(t, dir, openOptions{migrations: list[:2]})
	first.Close(context.Background())
	if _, err := open(context.Background(), dir, nil, openOptions{migrations: testMigrations(t, failingThird)}); err == nil {
		t.Fatal("a failing migration opened")
	}
	check, _ := openTestWith(t, dir, openOptions{migrations: list[:2]})
	if hasColumnT(t, check, "chats", "note") {
		t.Error("the first statement of the failed migration stayed")
	}
	if got := mcpVersion(t, check); got != 2 {
		t.Errorf("version = %d, want 2", got)
	}
}

func TestLoadMigrationsRejectsGapsAndDuplicates(t *testing.T) {
	good := fstest.MapFS{"m/0001_a.sql": {Data: []byte("x")}, "m/0002_b.sql": {Data: []byte("y")}}
	if list, err := loadMigrations(good, "m"); err != nil || len(list) != 2 || list[1].name != "b" {
		t.Errorf("good set: %v, %v", list, err)
	}
	for name, fsys := range map[string]fstest.MapFS{
		"gap":       {"m/0001_a.sql": {Data: nil}, "m/0003_c.sql": {Data: nil}},
		"no start":  {"m/0002_b.sql": {Data: nil}},
		"duplicate": {"m/0001_a.sql": {Data: nil}, "m/0001_b.sql": {Data: nil}},
		"bad name":  {"m/1_a.sql": {Data: nil}},
		"no name":   {"m/0001.sql": {Data: nil}},
	} {
		if _, err := loadMigrations(fsys, "m"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEmbeddedMigrationsAreContiguous(t *testing.T) {
	list, err := loadMigrations(embeddedMigrations, "migrations")
	if err != nil || len(list) == 0 || list[0].name != "init" {
		t.Errorf("embedded migrations: %v, %v", list, err)
	}
}
