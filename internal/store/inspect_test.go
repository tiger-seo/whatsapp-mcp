package store

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// inspectUnchanged runs Inspect and fails if it created, removed or changed any file in dir.
func inspectUnchanged(t *testing.T, dir string) (Inspection, error) {
	t.Helper()
	before := snapshotDir(t, dir)
	got, err := Inspect(context.Background(), dir)
	if after := snapshotDir(t, dir); !maps.Equal(before, after) {
		t.Errorf("Inspect changed the directory:\n before %v\n after  %v", before, after)
	}
	return got, err
}

func TestInspectAbsentStorageCreatesNothing(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		dir := filepath.Join(newDataDir(t), "never-created")
		got, err := Inspect(context.Background(), dir)
		if err != nil || got != (Inspection{}) {
			t.Fatalf("Inspect = %+v, %v; want the empty unpaired snapshot", got, err)
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Error("Inspect created the directory")
		}
	})
	t.Run("empty directory", func(t *testing.T) {
		dir := newDataDir(t)
		got, err := inspectUnchanged(t, dir)
		if err != nil || got != (Inspection{}) {
			t.Fatalf("Inspect = %+v, %v; want the empty unpaired snapshot", got, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("directory holds %d entries after Inspect", len(entries))
		}
	})
	t.Run("unrelated file", func(t *testing.T) {
		dir := newDataDir(t)
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := inspectUnchanged(t, dir); err != nil || got != (Inspection{}) {
			t.Fatalf("Inspect = %+v, %v", got, err)
		}
	})
}

func TestInspectCleanSchema3(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate []string
		want   bool
	}{
		// stateDirs needs one change to leave a log behind; this one alters nothing.
		{"paired", []string{"UPDATE mcp_version SET version = version"}, true},
		{"unpaired", []string{"DELETE FROM whatsmeow_device"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			clean, wal := stateDirs(t, c.mutate...)
			// The second directory holds the change only in an un-checkpointed log, as a crash leaves it.
			for state, dir := range map[string]string{"closed": clean, "log only": wal} {
				got, err := inspectUnchanged(t, dir)
				if err != nil {
					t.Fatalf("%s: %v", state, err)
				}
				want := Inspection{Present: true, SchemaVersion: 3, IdentityPresent: c.want}
				if got != want {
					t.Errorf("%s: Inspect = %+v, want %+v", state, got, want)
				}
			}
		})
	}
}

func TestInspectUnsafeMarker(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate []string
		want   bool
	}{
		{"paired", []string{"UPDATE unsafe_run SET state = 'unsafe'"}, true},
		{"unpaired", []string{"UPDATE unsafe_run SET state = 'unsafe'", "DELETE FROM whatsmeow_device"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			clean, wal := stateDirs(t, c.mutate...)
			for state, dir := range map[string]string{"closed": clean, "log only": wal} {
				got, err := inspectUnchanged(t, dir)
				if err != nil {
					t.Fatalf("%s: %v", state, err)
				}
				want := Inspection{Present: true, SchemaVersion: 3, Unsafe: true, IdentityPresent: c.want}
				if got != want {
					t.Errorf("%s: Inspect = %+v, want %+v", state, got, want)
				}
			}
		})
	}
}

func TestInspectLegacySchemasAreUnknownNotClean(t *testing.T) {
	list, err := loadMigrations(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2} {
		for _, paired := range []bool{true, false} {
			name := map[bool]string{true: "paired", false: "unpaired"}[paired]
			t.Run(string(rune('0'+n))+" "+name, func(t *testing.T) {
				dir := newDataDir(t)
				old, _ := openTestWith(t, dir, openOptions{migrations: list[:n]})
				if paired {
					seedDevice(t, old)
				}
				mustStore(t, old, msg(alice, "legacy-sentinel", alice, at(0)))
				if err := old.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				got, err := inspectUnchanged(t, dir)
				if err != nil {
					t.Fatal(err)
				}
				want := Inspection{Present: true, SchemaVersion: n, Legacy: true, Unsafe: true, IdentityPresent: paired}
				if got != want {
					t.Errorf("Inspect = %+v, want %+v", got, want)
				}
			})
		}
	}
}

func TestInspectMarkerProblemsAreUnsafeStorage(t *testing.T) {
	for name, mutate := range map[string][]string{
		"missing marker":   {"DELETE FROM unsafe_run"},
		"malformed marker": {"PRAGMA ignore_check_constraints = ON", "UPDATE unsafe_run SET repair_bits = 32"},
	} {
		t.Run(name, func(t *testing.T) {
			clean, wal := stateDirs(t, mutate...)
			for state, dir := range map[string]string{"closed": clean, "log only": wal} {
				got, err := inspectUnchanged(t, dir)
				if !errors.Is(err, ErrUnsafeStorage) {
					t.Errorf("%s: Inspect = %+v, %v; want ErrUnsafeStorage, never an inferred clean state", state, got, err)
				}
			}
		})
	}
}

func TestInspectReportsPendingRepairWithoutRunningIt(t *testing.T) {
	clean, wal := stateDirs(t,
		"INSERT INTO device_repair (device_jid, collection, revision, updated_at_ms) SELECT jid, 'regular', 1, 0 FROM whatsmeow_device")
	for state, dir := range map[string]string{"closed": clean, "log only": wal} {
		got, err := inspectUnchanged(t, dir)
		if err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		want := Inspection{Present: true, SchemaVersion: 3, IdentityPresent: true, RepairPending: true}
		if got != want {
			t.Errorf("%s: Inspect = %+v, want %+v", state, got, want)
		}
	}
}

func TestInspectRefusalsChangeNothing(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate []string
		want   error
	}{
		{"message schema too new", []string{"UPDATE mcp_version SET version = 4"}, ErrSchemaTooNew},
		{"device schema too new", []string{"UPDATE whatsmeow_version SET compat = 17"}, ErrSchemaTooNew},
		{"mcp_version without rows", []string{"DELETE FROM mcp_version"}, ErrSchemaInvalid},
		{"device tables without whatsmeow_version", []string{"DROP TABLE whatsmeow_version"}, ErrSchemaInvalid},
	} {
		t.Run(c.name, func(t *testing.T) {
			clean, wal := stateDirs(t, c.mutate...)
			for state, dir := range map[string]string{"closed": clean, "log only": wal} {
				if _, err := inspectUnchanged(t, dir); !errors.Is(err, c.want) {
					t.Errorf("%s: Inspect = %v, want %v", state, err, c.want)
				}
			}
		})
	}
	t.Run("not a database", func(t *testing.T) {
		dir := newDataDir(t)
		random := make([]byte, 4096)
		for i := range random {
			random[i] = byte(i*31 + 7)
		}
		if err := os.WriteFile(filepath.Join(dir, dbFileName), random, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := inspectUnchanged(t, dir); !errors.Is(err, ErrNotDatabase) {
			t.Errorf("random bytes: %v, want ErrNotDatabase", err)
		}
	})
	t.Run("truncated database", func(t *testing.T) {
		path := closedDB(t)
		if err := os.Truncate(path, 1000); err != nil {
			t.Fatal(err)
		}
		if _, err := inspectUnchanged(t, filepath.Dir(path)); !errors.Is(err, ErrNotDatabase) {
			t.Errorf("truncated: %v, want ErrNotDatabase", err)
		}
	})
	t.Run("orphan sidecars", func(t *testing.T) {
		for _, suffix := range []string{"-wal", "-shm"} {
			dir := newDataDir(t)
			if err := os.WriteFile(filepath.Join(dir, dbFileName+suffix), []byte("orphan"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := inspectUnchanged(t, dir); !errors.Is(err, ErrUnsafeStorage) {
				t.Errorf("orphan %s: %v, want ErrUnsafeStorage", suffix, err)
			}
		}
	})
	t.Run("rollback journal", func(t *testing.T) {
		path := closedDB(t)
		if err := os.WriteFile(path+"-journal", []byte("interrupted"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := inspectUnchanged(t, filepath.Dir(path)); !errors.Is(err, ErrUnsafeStorage) {
			t.Errorf("journal: %v, want ErrUnsafeStorage", err)
		}
	})
}

func TestInspectHonoursCancellation(t *testing.T) {
	dir := filepath.Dir(closedDB(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := snapshotDir(t, dir)
	if _, err := Inspect(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Errorf("Inspect with a cancelled context = %v, want context.Canceled", err)
	}
	if after := snapshotDir(t, dir); !maps.Equal(before, after) {
		t.Error("a cancelled Inspect changed the directory")
	}
}
