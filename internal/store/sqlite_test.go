package store

import (
	"database/sql"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const childEnv = "GO_STORE_CHILD"

// runChild re-executes the test binary to run one test in its own process, for tests that change process-wide
// SQLite state. The child test skips unless childEnv names it.
func runChild(t *testing.T, test string, env ...string) string {
	t.Helper()
	cmd := osexec.Command(os.Args[0], "-test.run=^"+test+"$", "-test.v", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), append([]string{childEnv + "=" + test}, env...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child %s failed: %v\n%s", test, err, out)
	}
	return string(out)
}

func inChild(t *testing.T, test string) {
	t.Helper()
	if os.Getenv(childEnv) != test {
		t.Skip("runs in a child process")
	}
}

func TestMemoryStatisticsAreOn(t *testing.T) {
	if err := memoryLimitAvailable(); err != nil {
		t.Fatalf("memory statistics could not be switched on: %v", err)
	}
	runChild(t, "TestHeapLimitChild")
}

// TestHeapLimitChild shows that the hard heap limit is enforced and not only accepted: with the limit at 4 MiB, a
// sort of 2,000 rows of 64 KiB fails with SQLITE_NOMEM, and the high-water mark moved.
func TestHeapLimitChild(t *testing.T) {
	inChild(t, "TestHeapLimitChild")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "heap.db"))+"?_pragma=temp_store(MEMORY)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, b BLOB)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 2000) " +
		"INSERT INTO t (id, b) SELECT i, zeroblob(65536) FROM n"); err != nil {
		t.Fatal(err)
	}
	memoryHighwater(true)
	setHardHeapLimit(4 * mib)
	rows, err := db.Query("SELECT id, b FROM t ORDER BY random()")
	if err == nil {
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
	}
	if err == nil {
		t.Fatal("a sort far over the heap limit succeeded")
	}
	if got := sqliteCode(err); got != sqliteNoMem {
		t.Errorf("error code = %d (%v), want SQLITE_NOMEM", got, err)
	}
	if mapped := mapDriverError(err); !strings.Contains(mapped.Error(), "memory") {
		t.Errorf("mapped error = %v, want the memory limit", mapped)
	}
	if hw := memoryHighwater(false); hw <= 0 {
		t.Errorf("high-water mark = %d, want it to move", hw)
	}
}
