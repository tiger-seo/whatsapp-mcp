package store

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/store/sqlstore/upgrades"
)

// TestDeviceUpgradeStepsAreTransactional guards the assumption that every whatsmeow device-schema step runs in
// one transaction with its version record, so a failed step leaves the schema at the previous version. The
// transaction mode is not exported, so it reads the pinned module's upgrade files.
func TestDeviceUpgradeStepsAreTransactional(t *testing.T) {
	out, err := osexec.Command("go", "list", "-m", "-f", "{{.Dir}}", "go.mau.fi/whatsmeow").Output()
	if err != nil {
		t.Skipf("go is not available to locate the whatsmeow module: %v", err)
	}
	dir := filepath.Join(strings.TrimSpace(string(out)), "store", "sqlstore", "upgrades")
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no upgrade files in %s: %v", dir, err)
	}
	header := regexp.MustCompile(`(?m)^--\s*transaction:\s*(\S+)`)
	number := regexp.MustCompile(`^(\d+)-`)
	highest := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := header.FindSubmatch(b); m != nil && string(m[1]) != "on" {
			t.Errorf("%s has -- transaction: %s; the upgrade steps are no longer one transaction each, so review the schema-upgrade rules before this whatsmeow version", filepath.Base(f), m[1])
		}
		if m := number.FindStringSubmatch(filepath.Base(f)); m != nil {
			n, _ := strconv.Atoi(m[1])
			// File NN-name.sql upgrades to version NN; 00-latest-schema.sql goes from nothing to the latest.
			if n > highest {
				highest = n
			}
		}
	}
	if highest != len(upgrades.Table) {
		t.Errorf("highest target version = %d, upgrades.Table has %d", highest, len(upgrades.Table))
	}
}
