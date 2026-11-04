package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRunGuardFreshStorageHasMarkerSchema(t *testing.T) {
	st, _ := openTestWith(t, newDataDir(t), openOptions{})
	if got := mcpVersion(t, st); got != 3 {
		t.Errorf("application schema = %d, want 3 with unsafe-run marker", got)
	}
	assertRunGuardState(t, st, "clean")
}

func assertRunGuardState(t *testing.T, st *Store, want string) {
	t.Helper()
	var state string
	var bits int
	if err := st.reader.QueryRow("SELECT state, repair_bits FROM unsafe_run WHERE id = 1").Scan(&state, &bits); err != nil {
		t.Fatalf("read run marker: %v", err)
	}
	if state != want || bits != 0 {
		t.Errorf("run marker = %q, %d; want %q, 0", state, bits, want)
	}
}

func TestRunGuardLegacyMigrationIsUnsafe(t *testing.T) {
	list, err := loadMigrations(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2} {
		t.Run(string(rune('0'+n)), func(t *testing.T) {
			dir := newDataDir(t)
			old, _ := openTestWith(t, dir, openOptions{migrations: list[:n]})
			mustStore(t, old, msg(alice, "run-guard-sentinel", alice, at(0)))
			if err := old.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			upgraded, _ := openTest(t, dir)
			assertRunGuardState(t, upgraded, "unsafe")
			if !messageExists(t, upgraded, alice, "run-guard-sentinel") {
				t.Error("legacy upgrade lost a message")
			}
		})
	}
}

func TestRunGuardInterruptedCreationIsUnsafe(t *testing.T) {
	dir := newDataDir(t)
	if _, err := createDB(filepath.Join(dir, dbFileName)); err != nil {
		t.Fatal(err)
	}
	st, _ := openTest(t, dir)
	assertRunGuardState(t, st, "unsafe")
}

func TestRunGuardFreshLowDiskReopensUnsafe(t *testing.T) {
	dir := newDataDir(t)
	_, err := open(context.Background(), dir, nil, openOptions{hooks: testHooks{free: func(string) (uint64, error) { return diskReserve - 1, nil }}})
	if !errors.Is(err, ErrDiskLow) {
		t.Fatalf("fresh low disk = %v", err)
	}
	st, _ := openTest(t, dir)
	assertRunGuardState(t, st, "unsafe")
}

func TestRunGuardWriterSynchronization(t *testing.T) {
	st, _ := openTestWith(t, newDataDir(t), openOptions{})
	ctx := context.Background()
	for _, name := range []string{"initial", "replacement"} {
		t.Run(name, func(t *testing.T) {
			conn, err := st.writer.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var mode string
			var synchronous int
			if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
				t.Fatal(err)
			}
			if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
				t.Fatal(err)
			}
			if mode != "wal" || synchronous != 2 {
				t.Errorf("writer journal_mode=%q synchronous=%d; require WAL and FULL", mode, synchronous)
			}
			// Discard the physical connection so the next iteration exercises its replacement.
			if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Errorf("discard writer: %v", err)
			}
			_ = conn.Close()
		})
	}
}

func TestRunGuardArmClearAndReopen(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	ctx := context.Background()
	if err := st.ArmRunGuard(ctx); err != nil {
		t.Fatal(err)
	}
	assertRunGuardState(t, st, "unsafe")
	if err := st.ClearRunGuard(ctx); err != nil {
		t.Fatal(err)
	}
	assertRunGuardState(t, st, "clean")
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = openTest(t, dir)
	assertRunGuardState(t, st, "clean")
}

func TestRunGuardMissingMarkerRefused(t *testing.T) {
	dir := newDataDir(t)
	st, _ := openTest(t, dir)
	if _, err := st.writer.Exec("DELETE FROM unsafe_run"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := open(context.Background(), dir, nil, openOptions{}); err == nil {
		t.Fatal("missing marker accepted")
	}
}

func TestRunGuardRevocationTransfersRepair(t *testing.T) {
	dir := newDataDir(t)
	st, dev, barrier := loadedDevice(t, dir)
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := dev.Container.DeleteDevice(ctx, dev); !errors.Is(err, ErrDeleteDeferred) {
		t.Fatal(err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := barrier.removeRevoked(ctx); err != nil {
		t.Fatal(err)
	}
	_, bits, err := st.RunGuard(ctx)
	if err != nil || bits != 1<<3 {
		t.Fatalf("transferred bits = %d, %v", bits, err)
	}
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = openTest(t, dir)
	fresh, freshBarrier := loadDevice(t, st)
	fresh.ID, fresh.LID, fresh.Account = dev.ID, dev.LID, dev.Account
	if err := fresh.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.AppState.GetAppStateVersion(ctx, "regular"); !errors.Is(err, ErrRecoveryPending) {
		t.Fatalf("ordinary sync admitted: %v", err)
	}
	names, err := freshBarrier.PendingRepairs(ctx)
	if err != nil || len(names) != 1 || names[0] != "regular" {
		t.Fatalf("materialized repairs = %v, %v", names, err)
	}
	_, ticket, err := freshBarrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	if err := freshBarrier.CompleteRepair(ctx, ticket); err != nil {
		t.Fatal(err)
	}
	_, bits, err = st.RunGuard(ctx)
	if err != nil || bits != 0 {
		t.Fatalf("repaired bits = %d, %v", bits, err)
	}
}

func TestRunGuardRepairClearRejectsMissingMarker(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	_, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.writer.Exec("DELETE FROM unsafe_run"); err != nil {
		t.Fatal(err)
	}
	if err := barrier.CompleteRepair(ctx, ticket); err == nil {
		t.Fatal("repair cleared without singleton")
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair").Scan(&count); err != nil || count != 1 {
		t.Fatalf("repair count = %d, %v", count, err)
	}
}

func TestRunGuardCleanupChecksActualWriter(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := dev.Container.DeleteDevice(ctx, dev); !errors.Is(err, ErrDeleteDeferred) {
		t.Fatal(err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.writer.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		t.Fatal(err)
	}
	if err := barrier.removeRevoked(ctx); err == nil {
		t.Fatal("cleanup accepted non-FULL writer")
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair").Scan(&count); err != nil || count != 1 {
		t.Fatalf("repair count = %d, %v", count, err)
	}
	conn, err := st.writer.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := barrier.removeRevoked(ctx); err != nil {
		t.Fatalf("replacement writer cleanup: %v", err)
	}
	_, bits, err := st.RunGuard(ctx)
	if err != nil || bits != 8 {
		t.Fatalf("transferred bits = %d, %v", bits, err)
	}
}

func TestRunGuardClearClosesDeviceAdmission(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	if err := st.ArmRunGuard(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearRunGuard(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.LoadDevice(ctx); err == nil {
		t.Fatal("new device admitted after clear")
	}
}

func TestRunGuardOfflineRecoveryPreservesRepair(t *testing.T) {
	dir := newDataDir(t)
	st, dev, barrier := loadedDevice(t, dir)
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	mustStore(t, st, msg(alice, "recovery-sentinel", alice, at(0)))
	if err := st.ArmRunGuard(ctx); err != nil {
		t.Fatal(err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = openTest(t, dir)
	if err := st.RecoverRunGuard(ctx, false); err == nil {
		t.Fatal("identity removed without authorization")
	}
	if err := st.RecoverRunGuard(ctx, true); err != nil {
		t.Fatal(err)
	}
	state, bits, err := st.RunGuard(ctx)
	if err != nil || state != "clean" || bits != 8 {
		t.Fatalf("marker = %s, %d, %v", state, bits, err)
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM whatsmeow_device").Scan(&count); err != nil || count != 0 {
		t.Fatalf("devices = %d, %v", count, err)
	}
	if !messageExists(t, st, alice, "recovery-sentinel") {
		t.Fatal("recovery deleted message")
	}
	if _, _, err := st.LoadDevice(ctx); err == nil {
		t.Fatal("recovery admitted a new device")
	}
}

func TestRunGuardOfflineRecoveryRollback(t *testing.T) {
	dir := newDataDir(t)
	st, dev, barrier := loadedDevice(t, dir)
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := st.ArmRunGuard(ctx); err != nil {
		t.Fatal(err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = openTest(t, dir)
	if _, err := st.writer.Exec(`CREATE TRIGGER fail_recovery_clear BEFORE UPDATE OF state ON unsafe_run WHEN NEW.state='clean' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := st.RecoverRunGuard(ctx, true); err == nil {
		t.Fatal("injected failure accepted")
	}
	state, bits, err := st.RunGuard(ctx)
	if err != nil || state != "unsafe" || bits != 0 {
		t.Fatalf("marker = %s, %d, %v", state, bits, err)
	}
	var devices, repairs int
	if err := st.reader.QueryRow("SELECT (SELECT count(*) FROM whatsmeow_device), (SELECT count(*) FROM device_repair)").Scan(&devices, &repairs); err != nil || devices != 1 || repairs != 1 {
		t.Fatalf("devices=%d repairs=%d err=%v", devices, repairs, err)
	}
}

func TestRunGuardOfflineRecoveryDeleteFailure(t *testing.T) {
	dir := newDataDir(t)
	st, dev, barrier := loadedDevice(t, dir)
	ctx := context.Background()
	mustStore(t, st, msg(alice, "delete-failure-sentinel", alice, at(0)))
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := st.ArmRunGuard(ctx); err != nil {
		t.Fatal(err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = openTest(t, dir)
	if _, err := st.writer.Exec(`CREATE TRIGGER fail_device_delete BEFORE DELETE ON whatsmeow_device BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := st.RecoverRunGuard(ctx, true); err == nil {
		t.Fatal("failed deletion accepted")
	}
	state, bits, err := st.RunGuard(ctx)
	if err != nil || state != "unsafe" || bits != 0 {
		t.Fatalf("marker = %s, %d, %v", state, bits, err)
	}
	var devices, repairs int
	if err := st.reader.QueryRow(`SELECT (SELECT count(*) FROM whatsmeow_device), (SELECT count(*) FROM device_repair)`).Scan(&devices, &repairs); err != nil || devices != 1 || repairs != 1 {
		t.Fatalf("devices=%d repairs=%d err=%v", devices, repairs, err)
	}
	if !messageExists(t, st, alice, "delete-failure-sentinel") {
		t.Fatal("message lost")
	}
}

func TestRunGuardOfflineRecoveryChecksActualWriter(t *testing.T) {
	dir := newDataDir(t)
	st, dev, barrier := loadedDevice(t, dir)
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := st.ArmRunGuard(ctx); err != nil {
		t.Fatal(err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = openTest(t, dir)
	if _, err := st.writer.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecoverRunGuard(ctx, true); err == nil {
		t.Fatal("recovery accepted non-FULL writer")
	}
	var devices, repairs int
	if err := st.reader.QueryRow("SELECT (SELECT count(*) FROM whatsmeow_device), (SELECT count(*) FROM device_repair)").Scan(&devices, &repairs); err != nil || devices != 1 || repairs != 1 {
		t.Fatalf("devices=%d repairs=%d err=%v", devices, repairs, err)
	}
}

func TestRunGuardSaveRejectsMissingMarker(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	if _, err := st.writer.Exec("DELETE FROM unsafe_run"); err != nil {
		t.Fatal(err)
	}
	dev.PushName = "changed"
	if err := dev.Save(context.Background()); err == nil {
		t.Fatal("save accepted missing marker")
	}
	var name string
	if err := st.reader.QueryRow("SELECT push_name FROM whatsmeow_device").Scan(&name); err != nil || name == "changed" {
		t.Fatalf("saved name=%q, err=%v", name, err)
	}
}

func TestRunGuardDeviceWriteRejectsMissingMarker(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	if _, err := st.writer.Exec("DELETE FROM unsafe_run"); err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(context.Background(), "regular", 1, [128]byte{}); err == nil {
		t.Fatal("write accepted missing marker")
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair").Scan(&count); err != nil || count != 0 {
		t.Fatalf("repair count=%d err=%v", count, err)
	}
}

func TestRunGuardFailedArmBlocksDeviceAdmission(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	if _, err := st.writer.Exec("DELETE FROM unsafe_run"); err != nil {
		t.Fatal(err)
	}
	if err := st.ArmRunGuard(context.Background()); err == nil {
		t.Fatal("arm accepted missing marker")
	}
	if _, _, err := st.LoadDevice(context.Background()); err == nil {
		t.Fatal("device admitted after failed arm")
	}
}

func TestRunGuardSaveRejectsTextMarkerID(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	mustStore(t, st, msg(alice, "save-corruption-sentinel", alice, at(0)))
	if _, err := st.writer.Exec(`DROP TABLE unsafe_run; CREATE TABLE unsafe_run(id TEXT, state TEXT, repair_bits INTEGER); INSERT INTO unsafe_run VALUES('1','clean',8)`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := st.reader.QueryRow(`SELECT push_name FROM whatsmeow_device`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	dev.PushName = "synthetic changed name"
	if err := dev.Save(ctx); err == nil {
		t.Errorf("save with text marker id: %v", err)
	}
	var after string
	var repairs int
	if err := st.reader.QueryRow(`SELECT push_name, (SELECT count(*) FROM device_repair) FROM whatsmeow_device`).Scan(&after, &repairs); err != nil || after != before || repairs != 0 {
		t.Errorf("save changed name=%q repairs=%d err=%v", after, repairs, err)
	}
	if !messageExists(t, st, alice, "save-corruption-sentinel") {
		t.Error("message lost")
	}
}

func TestRunGuardRepairRejectsMarkerCorruptionDuringDelete(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	mustStore(t, st, msg(alice, "repair-corruption-sentinel", alice, at(0)))
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.writer.Exec(`DROP TABLE unsafe_run; CREATE TABLE unsafe_run(id, state, repair_bits); INSERT INTO unsafe_run VALUES(1,'clean',8)`); err != nil {
		t.Fatal(err)
	}
	_, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.writer.Exec(`CREATE TRIGGER corrupt_marker AFTER DELETE ON device_repair BEGIN UPDATE unsafe_run SET id=1.0; END`); err != nil {
		t.Fatal(err)
	}
	if err := barrier.CompleteRepair(ctx, ticket); err == nil {
		t.Errorf("completion after marker corruption: %v", err)
	}
	var idType string
	var bits, repairs int
	if err := st.reader.QueryRow(`SELECT typeof(id), repair_bits, (SELECT count(*) FROM device_repair) FROM unsafe_run`).Scan(&idType, &bits, &repairs); err != nil || idType != "integer" || bits != 8 || repairs != 1 {
		t.Errorf("rollback id type=%q bits=%d repairs=%d err=%v", idType, bits, repairs, err)
	}
	if !messageExists(t, st, alice, "repair-corruption-sentinel") {
		t.Error("message lost")
	}
}

func TestRunGuardTransitionFailuresLatch(t *testing.T) {
	for _, phase := range []string{"arm", "clear"} {
		t.Run(phase, func(t *testing.T) {
			dir := newDataDir(t)
			st, _ := openTest(t, dir)
			ctx := context.Background()
			mustStore(t, st, msg(alice, "transition-sentinel", alice, at(0)))
			if phase == "clear" {
				if err := st.ArmRunGuard(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := st.writer.Exec(`CREATE TRIGGER fail_marker BEFORE UPDATE OF state ON unsafe_run BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
				t.Fatal(err)
			}
			var err error
			if phase == "arm" {
				err = st.ArmRunGuard(ctx)
			} else {
				err = st.ClearRunGuard(ctx)
			}
			if err == nil {
				t.Fatal("injected transition succeeded")
			}
			if _, err := st.writer.Exec(`DROP TRIGGER fail_marker`); err != nil {
				t.Fatal(err)
			}
			if err := st.ClearRunGuard(ctx); !errors.Is(err, ErrUnsafeRun) {
				t.Fatalf("clear after failed transition: %v", err)
			}
			if _, _, err := st.LoadDevice(ctx); err == nil {
				t.Fatal("device admitted after failed transition")
			}
			if err := st.Close(ctx); err != nil {
				t.Fatal(err)
			}
			st, _ = openTest(t, dir)
			want := "unsafe"
			if phase == "arm" {
				want = "clean"
			}
			assertRunGuardState(t, st, want)
			if !messageExists(t, st, alice, "transition-sentinel") {
				t.Fatal("message lost")
			}
		})
	}
}

func TestRunGuardMalformedMarkerTransition(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	// Replace the singleton with a permissive synthetic schema to model corruption.
	if _, err := st.writer.Exec(`DROP TABLE unsafe_run; CREATE TABLE unsafe_run(id TEXT, state TEXT, repair_bits INTEGER); INSERT INTO unsafe_run VALUES('1','clean',0)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RunGuard(ctx); !errors.Is(err, ErrUnsafeRun) {
		t.Fatalf("read malformed marker: %v", err)
	}
	if err := st.ArmRunGuard(ctx); !errors.Is(err, ErrUnsafeRun) {
		t.Fatalf("arm malformed marker: %v", err)
	}
	var state string
	if err := st.reader.QueryRow(`SELECT state FROM unsafe_run`).Scan(&state); err != nil || state != "clean" {
		t.Fatalf("marker changed: %q, %v", state, err)
	}
}

func TestRunGuardLowDiskRecoveryPreservesData(t *testing.T) {
	dir := newDataDir(t)
	st, dev, barrier := loadedDevice(t, dir)
	ctx := context.Background()
	mustStore(t, st, msg(alice, "disk-recovery-sentinel", alice, at(0)))
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := st.ArmRunGuard(ctx); err != nil {
		t.Fatal(err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	free, now := uint64(diskReserve-1), time.Unix(5000, 0)
	st.disk.free = func(string) (uint64, error) { return free, nil }
	st.disk.now = func() time.Time { return now }
	st.disk.measured = time.Time{}
	if err := st.ClearRunGuard(ctx); !errors.Is(err, ErrDiskLow) {
		t.Fatalf("clear at low disk: %v", err)
	}
	free, now = diskResume-1, now.Add(time.Minute)
	if err := st.RecoverRunGuard(ctx, true); !errors.Is(err, ErrUnsafeRun) {
		t.Fatalf("same invocation recovery: %v", err)
	}
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = openTest(t, dir)
	st.disk.free = func(string) (uint64, error) { return free, nil }
	st.disk.now = func() time.Time { return now }
	st.disk.measured = time.Time{}
	// Re-establish the latch for this invocation, then check the intermediate range.
	free = diskReserve - 1
	if err := st.RecoverRunGuard(ctx, true); !errors.Is(err, ErrDiskLow) {
		t.Fatalf("low disk recovery: %v", err)
	}
	free, now = diskResume-1, now.Add(time.Minute)
	if err := st.RecoverRunGuard(ctx, true); !errors.Is(err, ErrDiskLow) {
		t.Fatalf("hysteresis recovery: %v", err)
	}
	state, bits, err := st.RunGuard(ctx)
	if err != nil || state != "unsafe" || bits != 0 {
		t.Fatalf("marker: %s %d %v", state, bits, err)
	}
	var devices, repairs int
	if err := st.reader.QueryRow(`SELECT (SELECT count(*) FROM whatsmeow_device), (SELECT count(*) FROM device_repair)`).Scan(&devices, &repairs); err != nil || devices != 1 || repairs != 1 {
		t.Fatalf("devices=%d repairs=%d err=%v", devices, repairs, err)
	}
	free, now = diskResume, now.Add(time.Minute)
	if err := st.RecoverRunGuard(ctx, false); !errors.Is(err, ErrUnsafeRun) {
		t.Fatalf("unauthorized removal: %v", err)
	}
	if err := st.RecoverRunGuard(ctx, true); err != nil {
		t.Fatal(err)
	}
	state, bits, err = st.RunGuard(ctx)
	if err != nil || state != "clean" || bits != 8 {
		t.Fatalf("recovered marker: %s %d %v", state, bits, err)
	}
	if !messageExists(t, st, alice, "disk-recovery-sentinel") {
		t.Fatal("message lost")
	}
}
