package store

import (
	"bufio"
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"testing"
	"time"
)

const crashEnv = "GO_STORE_CRASH_CHILD"

// TestCrashChild is the child of TestKilledProcessKeepsEveryCommittedWrite: it writes messages in a loop and
// prints each ID once the call has returned, until the parent kills it.
func TestCrashChild(t *testing.T) {
	dir := os.Getenv(crashEnv)
	if dir == "" {
		t.Skip("runs as a child of the crash test")
	}
	st, err := Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		id := fmt.Sprintf("crash-%d", i)
		if err := st.StoreMessage(context.Background(), msg(alice, id, alice, at(i%5000), withText(strings.Repeat("x", 200)))); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("COMMITTED %s\n", id)
	}
}

func TestKilledProcessKeepsEveryCommittedWrite(t *testing.T) {
	dir := newDataDir(t)
	cmd := osexec.Command(os.Args[0], "-test.run=^TestCrashChild$", "-test.timeout=90s")
	cmd.Env = append(os.Environ(), crashEnv+"="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var committed []string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if id, ok := strings.CutPrefix(sc.Text(), "COMMITTED "); ok {
			committed = append(committed, id)
			if len(committed) >= 300 {
				break
			}
		}
	}
	if len(committed) < 300 {
		t.Fatalf("the child committed only %d writes before it ended", len(committed))
	}
	cmd.Process.Kill()
	cmd.Wait()

	st, _ := openTest(t, dir)
	for _, id := range committed {
		if !messageExists(t, st, alice, id) {
			t.Fatalf("committed write %s is missing after the kill", id)
		}
	}
	var check string
	if err := st.reader.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Errorf("integrity_check = %q, %v", check, err)
	}
}
