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

const (
	guardChildDirEnv  = "GO_STORE_GUARD_CHILD"
	guardChildModeEnv = "GO_STORE_GUARD_MODE"
)

// TestRunGuardKillChild is the child of TestRunGuardSurvivesKilledProcess: it reaches one marker
// state, reports it and waits to be killed. It is evidence for an abrupt process kill only, not for
// power loss or an OS crash.
func TestRunGuardKillChild(t *testing.T) {
	dir := os.Getenv(guardChildDirEnv)
	if dir == "" {
		t.Skip("runs as a child of the kill test")
	}
	ctx := context.Background()
	st, err := Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	switch mode := os.Getenv(guardChildModeEnv); mode {
	case "opened":
	case "armed":
		if err := st.ArmRunGuard(ctx); err != nil {
			t.Fatal(err)
		}
	case "armed-cleared":
		if err := st.ArmRunGuard(ctx); err != nil {
			t.Fatal(err)
		}
		if err := st.ClearRunGuard(ctx); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	fmt.Println("READY")
	time.Sleep(60 * time.Second)
}

func TestRunGuardSurvivesKilledProcess(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"opened", "clean"},
		{"armed", "unsafe"},
		{"armed-cleared", "clean"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			dir := newDataDir(t)
			cmd := osexec.Command(os.Args[0], "-test.run=^TestRunGuardKillChild$", "-test.timeout=90s")
			cmd.Env = append(os.Environ(), guardChildDirEnv+"="+dir, guardChildModeEnv+"="+tc.mode)
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			ready := false
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				if strings.TrimSpace(sc.Text()) == "READY" {
					ready = true
					break
				}
			}
			if !ready {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatal("the child never reached its marker state")
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatalf("kill: %v", err)
			}
			_ = cmd.Wait()

			st, _ := openTest(t, dir)
			state, bits, err := st.RunGuard(context.Background())
			if err != nil {
				t.Fatalf("run guard after the kill: %v", err)
			}
			if state != tc.want {
				t.Errorf("marker after a kill in state %q = %q (bits %d), want %q", tc.mode, state, bits, tc.want)
			}
			var check string
			if err := st.reader.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
				t.Errorf("integrity_check = %q, %v", check, err)
			}
		})
	}
}
