package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

// forbidAccountWork installs seams that fail the test on any attempt to start a client, a version
// discovery request or any other network work: status is offline.
func forbidAccountWork(t *testing.T) {
	t.Helper()
	prev := ownerSeam
	t.Cleanup(func() { ownerSeam = prev })
	ownerSeam = &ownerSeamConfig{
		discoveryRoundTripper: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			t.Errorf("status made a network request to %s", r.URL.Host)
			return nil, errors.New("forbidden")
		}),
		sessionFactory: func(wa.SessionConfig) (wa.SessionHandle, error) {
			t.Error("status constructed a WhatsApp session")
			return nil, errors.New("forbidden")
		},
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// storageState records every file in dir except the lock file, whose PID bookkeeping the contract
// allows status to change.
func storageState(t *testing.T, dir string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == lock.FileName {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel, _ := filepath.Rel(dir, path)
		state[rel] = fmt.Sprintf("%d %s %s", fi.Size(), fi.ModTime().UTC().Format("20060102150405.000000000"), hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func wantStatus(loggedIn bool) string {
	return fmt.Sprintf("connected: false\nlogged_in: %t\npairing_required: false\nwa_version: %s (bundled; not refreshed)\n",
		loggedIn, wa.BundledVersion())
}

const offlineWarning = "Offline snapshot; saved credentials have not been verified with WhatsApp."

// statusOn runs status and fails if it changed anything but the lock file.
func statusOn(t *testing.T, dir string) result {
	t.Helper()
	forbidAccountWork(t)
	before := storageState(t, dir)
	r := runIn(t, nil, "status", "--data-dir", dir)
	if after := storageState(t, dir); !maps.Equal(before, after) {
		t.Errorf("status changed the storage:\n before %v\n after  %v", before, after)
	}
	return r
}

func execSQL(t *testing.T, dir string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath(dir))+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath(dir) + s); err == nil {
			t.Fatalf("fixture left %s behind", s)
		}
	}
}

func seedUnpairedStorage(t *testing.T, dir string) {
	t.Helper()
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStatusMissingDirectoryCreatesNothing(t *testing.T) {
	forbidAccountWork(t)
	nested := filepath.Join(t.TempDir(), "a", "b")
	r := runIn(t, nil, "status", "--data-dir", nested)
	r.expect(t, exitOK)
	if r.stdout != wantStatus(false) {
		t.Errorf("stdout = %q, want %q", r.stdout, wantStatus(false))
	}
	if strings.Contains(r.stderr, offlineWarning) {
		t.Errorf("the offline-credentials warning appeared without an identity:\n%s", r.stderr)
	}
	if _, err := os.Stat(filepath.Join(nested, "..")); err == nil {
		if _, err := os.Stat(nested); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("status created the data directory (stat: %v)", err)
		}
	}
}

func TestStatusEmptyDirectoryCreatesNothing(t *testing.T) {
	dir := privateDir(t)
	r := statusOn(t, dir)
	r.expect(t, exitOK)
	if r.stdout != wantStatus(false) {
		t.Errorf("stdout = %q", r.stdout)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("status left %v in an empty directory", names)
	}
}

func TestStatusSavedIdentity(t *testing.T) {
	dir := privateDir(t)
	seedSavedIdentity(t, dir)
	r := statusOn(t, dir)
	r.expect(t, exitOK)
	if r.stdout != wantStatus(true) {
		t.Errorf("stdout = %q, want %q", r.stdout, wantStatus(true))
	}
	if !strings.Contains(r.stderr, offlineWarning) {
		t.Errorf("stderr lacks %q:\n%s", offlineWarning, r.stderr)
	}
}

func TestStatusUnpairedDatabase(t *testing.T) {
	dir := privateDir(t)
	seedUnpairedStorage(t, dir)
	r := statusOn(t, dir)
	r.expect(t, exitOK)
	if r.stdout != wantStatus(false) {
		t.Errorf("stdout = %q", r.stdout)
	}
	if strings.Contains(r.stderr, offlineWarning) {
		t.Errorf("unpaired status warned about credentials:\n%s", r.stderr)
	}
}

func TestStatusPresentDatabaseWithoutLockUsesTheNormalLock(t *testing.T) {
	dir := privateDir(t)
	seedSavedIdentity(t, dir)
	_ = os.Remove(filepath.Join(dir, lock.FileName))
	r := statusOn(t, dir)
	r.expect(t, exitOK)
	if r.stdout != wantStatus(true) {
		t.Errorf("stdout = %q", r.stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, lock.FileName)); err != nil {
		t.Errorf("no lock file after inspecting a present database: %v", err)
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatalf("status left the lock held: %v", err)
	}
	l.Release()
}

func TestStatusUnsafeAndLegacyStorageExitNonZero(t *testing.T) {
	for _, c := range []struct {
		name   string
		seed   func(t *testing.T, dir string)
		stmts  []string
		paired bool
	}{
		{"unsafe marker, paired", func(t *testing.T, dir string) { seedSavedIdentity(t, dir) },
			[]string{"UPDATE unsafe_run SET state = 'unsafe'"}, true},
		{"unsafe marker, unpaired", seedUnpairedStorage,
			[]string{"UPDATE unsafe_run SET state = 'unsafe'"}, false},
		{"legacy schema 2, paired", func(t *testing.T, dir string) { seedSavedIdentity(t, dir) },
			[]string{"DROP TABLE unsafe_run", "UPDATE mcp_version SET version = 2"}, true},
		{"legacy schema 1, unpaired", seedUnpairedStorage,
			[]string{"DROP TABLE unsafe_run", "DROP TABLE device_repair", "UPDATE mcp_version SET version = 1"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := privateDir(t)
			c.seed(t, dir)
			execSQL(t, dir, c.stmts...)
			r := statusOn(t, dir)
			r.expect(t, exitError)
			if r.stdout != wantStatus(c.paired) {
				t.Errorf("stdout = %q, want the honest local fields %q", r.stdout, wantStatus(c.paired))
			}
			if !strings.Contains(r.stderr, "offline recovery") {
				t.Errorf("stderr lacks the offline-recovery warning:\n%s", r.stderr)
			}
			if c.paired != strings.Contains(r.stderr, offlineWarning) {
				t.Errorf("credentials warning present = %t, want %t:\n%s", !c.paired, c.paired, r.stderr)
			}
		})
	}
}

func TestStatusStorageRefusalsExitNonZeroAndChangeNothing(t *testing.T) {
	random := make([]byte, 4096)
	for i := range random {
		random[i] = byte(i*31 + 7)
	}
	for _, c := range []struct {
		name  string
		build func(t *testing.T, dir string)
		want  string
	}{
		{"missing marker", func(t *testing.T, dir string) {
			seedSavedIdentity(t, dir)
			execSQL(t, dir, "DELETE FROM unsafe_run")
		}, "marker"},
		{"not a database", func(t *testing.T, dir string) {
			if err := os.WriteFile(dbPath(dir), random, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "not a usable SQLite database"},
		{"orphan sidecar", func(t *testing.T, dir string) {
			if err := os.WriteFile(dbPath(dir)+"-wal", []byte("orphan"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "orphan"},
		{"rollback journal", func(t *testing.T, dir string) {
			seedSavedIdentity(t, dir)
			if err := os.WriteFile(dbPath(dir)+"-journal", []byte("interrupted"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "journal"},
		{"schema too new", func(t *testing.T, dir string) {
			seedSavedIdentity(t, dir)
			execSQL(t, dir, "UPDATE mcp_version SET version = 4")
		}, "schema"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := privateDir(t)
			c.build(t, dir)
			r := statusOn(t, dir)
			r.expect(t, exitError)
			if !strings.Contains(strings.ToLower(r.stderr), strings.ToLower(c.want)) {
				t.Errorf("stderr lacks %q:\n%s", c.want, r.stderr)
			}
			if strings.Contains(r.stdout, "logged_in:") {
				t.Errorf("a refused inspection still claimed local fields:\n%s", r.stdout)
			}
		})
	}
}

func TestStatusHeldLockReportsHolderWithoutInspecting(t *testing.T) {
	dir := privateDir(t)
	// A corrupt database that an inspection would report instead of the holder.
	if err := os.WriteFile(dbPath(dir), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	r := statusOn(t, dir)
	r.expect(t, exitError)
	want := fmt.Sprintf("another whatsapp-mcp (PID %d) is using the data directory %s", os.Getpid(), dir)
	if !strings.Contains(r.stderr, want) {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
	if strings.Contains(r.stderr, "SQLite") {
		t.Errorf("status inspected the database while another process held the lock:\n%s", r.stderr)
	}
	if strings.Contains(r.stdout, "logged_in:") {
		t.Errorf("status claimed the owner's state:\n%s", r.stdout)
	}
}

func TestStatusHeldLockBeforeDatabaseCreation(t *testing.T) {
	dir := privateDir(t)
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	r := statusOn(t, dir)
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, fmt.Sprintf("PID %d", os.Getpid())) {
		t.Errorf("stderr lacks the holder PID:\n%s", r.stderr)
	}
	if _, err := os.Stat(dbPath(dir)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("status created the database (stat: %v)", err)
	}
}

func TestStatusUsageAndConfigurationErrorsStayDistinct(t *testing.T) {
	forbidAccountWork(t)
	dir := privateDir(t)
	runIn(t, nil, "status", "--data-dir", dir, "extra").expect(t, exitUsage)
	runIn(t, []string{"WHATSAPP_MCP_LOG_LEVEL=verbose"}, "status", "--data-dir", dir).expect(t, exitUsage)
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := runIn(t, nil, "status", "--data-dir", file)
	r.expect(t, exitError)
	if !strings.Contains(r.stderr, "not a directory") {
		t.Errorf("stderr = %q", r.stderr)
	}
}
