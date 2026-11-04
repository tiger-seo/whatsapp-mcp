package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func repairMigrations(t *testing.T) []migration {
	t.Helper()
	list, err := loadMigrations(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[1].version != 2 || list[1].name != "device_repair" {
		t.Fatalf("embedded migration count = %d, want init and device_repair", len(list))
	}
	return list
}

func TestDeviceRepairMigrationFresh(t *testing.T) {
	st, recs := openTest(t, newDataDir(t))
	if got := mcpVersion(t, st); got != 2 {
		t.Errorf("message schema = %d, want 2", got)
	}
	var deviceVersion int
	if err := st.reader.QueryRow("SELECT version FROM whatsmeow_version").Scan(&deviceVersion); err != nil || deviceVersion != 16 {
		t.Errorf("device schema = %d, err = %v; want 16", deviceVersion, err)
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair").Scan(&count); err != nil || count != 0 {
		t.Errorf("fresh repair table count = %d, err = %v", count, err)
	}
	events := recs.events("store.migrate")
	if len(events) != 2 || events[0].attrs["to"] != "1" || events[1].attrs["to"] != "2" {
		t.Errorf("fresh migration events = %v, want 1 then 2", events)
	}
}

func TestDeviceRepairMigrationUpgradePreservesData(t *testing.T) {
	list := repairMigrations(t)
	dir := newDataDir(t)
	old, _ := openTestWith(t, dir, openOptions{migrations: list[:1]})
	dev := seedDevice(t, old)
	jid := dev.ID.String()
	mustStore(t, old, msg(alice, "migration-sentinel", alice, at(0)))
	var keyBefore []byte
	if err := old.reader.QueryRow("SELECT identity_key FROM whatsmeow_device WHERE jid = ?", jid).Scan(&keyBefore); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	upgraded, recs := openTest(t, dir)
	if got := mcpVersion(t, upgraded); got != 2 {
		t.Errorf("upgraded schema = %d, want 2", got)
	}
	if len(recs.events("store.migrate")) != 1 || !messageExists(t, upgraded, alice, "migration-sentinel") {
		t.Error("upgrade did not preserve message or did not apply exactly one migration")
	}
	var keyAfter []byte
	if err := upgraded.reader.QueryRow("SELECT identity_key FROM whatsmeow_device WHERE jid = ?", jid).Scan(&keyAfter); err != nil || !bytes.Equal(keyBefore, keyAfter) {
		t.Errorf("device identity key changed during upgrade: %v", err)
	}
	var version int
	if err := upgraded.reader.QueryRow("SELECT version FROM whatsmeow_version").Scan(&version); err != nil || version != 16 {
		t.Errorf("device schema = %d, err = %v; want 16", version, err)
	}
}

func TestDeviceRepairMigrationConstraintsAndCascade(t *testing.T) {
	repairMigrations(t)
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	jid := dev.ID.String()
	mustStore(t, st, msg(alice, "cascade-sentinel", alice, at(0)))
	insert := `INSERT INTO device_repair (device_jid, collection, revision, target_version, repair_ticket, updated_at_ms) VALUES (?, ?, ?, ?, ?, ?)`
	for _, collection := range []string{"critical_block", "critical_unblock_low", "regular_high", "regular", "regular_low"} {
		if _, err := st.writer.Exec(insert, jid, collection, 1, make([]byte, 8), make([]byte, 16), int64(1)); err != nil {
			t.Fatalf("valid collection %s: %v", collection, err)
		}
	}
	if _, err := st.writer.Exec("DELETE FROM device_repair WHERE device_jid=? AND collection='regular'", jid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.writer.Exec(insert, jid, "regular", 1, nil, nil, int64(1)); err != nil {
		t.Fatalf("nullable marker fields: %v", err)
	}
	if _, err := st.writer.Exec("DELETE FROM device_repair WHERE device_jid=? AND collection='regular'", jid); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]any{
		"duplicate":          {jid, "critical_block", 1, nil, nil, 1},
		"unknown device":     {"999999@s.whatsapp.net", "regular", 1, nil, nil, 1},
		"empty device":       {"", "regular", 1, nil, nil, 1},
		"long device":        {strings.Repeat("9", 257), "regular", 1, nil, nil, 1},
		"unknown collection": {jid, "unknown", 1, nil, nil, 1},
		"null collection":    {jid, nil, 1, nil, nil, 1},
		"zero revision":      {jid, "regular", 0, nil, nil, 1},
		"negative revision":  {jid, "regular", -1, nil, nil, 1},
		"null revision":      {jid, "regular", nil, nil, nil, 1},
		"short target":       {jid, "regular", 1, make([]byte, 7), nil, 1},
		"long target":        {jid, "regular", 1, make([]byte, 9), nil, 1},
		"short ticket":       {jid, "regular", 1, nil, make([]byte, 15), 1},
		"long ticket":        {jid, "regular", 1, nil, make([]byte, 17), 1},
		"null timestamp":     {jid, "regular", 1, nil, nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := st.writer.Exec(insert, args...); err == nil {
				t.Error("invalid marker accepted")
			}
		})
	}
	if _, err := st.writer.Exec("DELETE FROM whatsmeow_device WHERE jid = ?", jid); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair WHERE device_jid = ?", jid).Scan(&count); err != nil || count != 0 {
		t.Errorf("markers after device deletion = %d, err = %v", count, err)
	}
	if !messageExists(t, st, alice, "cascade-sentinel") {
		t.Error("device deletion removed application messages")
	}
}

func TestDeviceRepairMigrationFailureIsAtomic(t *testing.T) {
	list := repairMigrations(t)
	dir := newDataDir(t)
	old, _ := openTestWith(t, dir, openOptions{migrations: list[:1]})
	dev := seedDevice(t, old)
	jid := dev.ID.String()
	var originalKey []byte
	if err := old.reader.QueryRow("SELECT identity_key FROM whatsmeow_device WHERE jid = ?", jid).Scan(&originalKey); err != nil {
		t.Fatal(err)
	}
	mustStore(t, old, msg(alice, "rollback-sentinel", alice, at(0)))
	if err := old.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	broken := append([]migration(nil), list...)
	broken[1].sql += "\nTHIS IS NOT SQL;"
	if _, err := open(context.Background(), dir, nil, openOptions{migrations: broken}); err == nil {
		t.Fatal("broken migration opened successfully")
	} else {
		var migrationErr *MigrationError
		if !errors.As(err, &migrationErr) {
			t.Fatalf("failure = %v, want MigrationError", err)
		}
	}
	check, _ := openTestWith(t, dir, openOptions{migrations: list[:1]})
	if mcpVersion(t, check) != 1 || !messageExists(t, check, alice, "rollback-sentinel") {
		t.Error("failed migration changed version or lost message")
	}
	var keyAfter []byte
	if err := check.reader.QueryRow("SELECT identity_key FROM whatsmeow_device WHERE jid = ?", jid).Scan(&keyAfter); err != nil || !bytes.Equal(originalKey, keyAfter) {
		t.Errorf("failed migration changed device key: %v", err)
	}
	var tables int
	if err := check.reader.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='device_repair'").Scan(&tables); err != nil || tables != 0 {
		t.Errorf("repair tables after failed migration = %d, err = %v", tables, err)
	}
	if err := check.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, _ := openTest(t, dir)
	if mcpVersion(t, reopened) != 2 || !messageExists(t, reopened, alice, "rollback-sentinel") {
		t.Error("reopen did not upgrade or preserve message")
	}
}
