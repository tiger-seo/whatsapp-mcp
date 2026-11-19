//go:build linux || darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"golang.org/x/sys/unix"
)

func TestMCPDiagnosticShutdownNative(t *testing.T) {
	if mode := os.Getenv("WA_TEST_NATIVE_DIAGNOSTICS"); mode != "" {
		runDiagnosticChild(mode)
		return
	}
	for _, mode := range []string{"blocked_startup", "blocked_operation", "blocked_drain", "closed_stderr", "first_signal", "forced_second_signal"} {
		t.Run(mode, func(t *testing.T) { testDiagnosticChild(t, mode) })
	}
}

func runDiagnosticChild(mode string) {
	control := os.NewFile(3, "test control")
	report := func(s string) { _, _ = fmt.Fprintln(control, s) }
	args := []string{"stdio", "--data-dir", os.Getenv("WA_TEST_NATIVE_DIAGNOSTICS_DIR"), "--log-level", "error"}
	if mode == "blocked_startup" || mode == "closed_stderr" {
		args[len(args)-1] = "invalid"
	}
	lifecycleFactory = func(logger *slog.Logger) *lifecycle {
		lc := newLifecycle(logger)
		lc.exit = func(code int) { report("forced_exit"); os.Exit(code) }
		lc.diagnosticFinalizing = func(pending bool) {
			if pending {
				report("pending_finalization")
			} else {
				report("finalizing")
			}
		}
		return lc
	}
	newOwner = func(deps ownerDeps) (*ownerHandle, error) {
		h := newHeldOwner()
		h.startHook = func() {
			report("ready")
			if mode == "blocked_operation" {
				deps.log.Warn("synthetic", "event", "history_incomplete")
			}
		}
		o := &diagnosticChildOwner{heldOwner: h, stop: func() {
			if mode != "blocked_operation" {
				deps.log.Warn("synthetic", "event", "history_incomplete")
			}
			report("owner_stop")
			h.complete()
		}}
		return &ownerHandle{owner: o}, nil
	}
	os.Exit(run(args, os.Stdin, os.Stdout, os.Stderr, os.Environ()))
}

func testDiagnosticChild(t *testing.T, mode string) {
	dir := privateDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestMCPDiagnosticShutdownNative$")
	cmd.Env = childEnv("WA_TEST_NATIVE_DIAGNOSTICS="+mode, "WA_TEST_NATIVE_DIAGNOSTICS_DIR="+dir)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	control, signalOut := nativeTestPipe(t)
	stderr, stderrOut := nativeTestPipe(t)
	if mode == "closed_stderr" {
		stderr.Close()
	} else {
		fillDiagnosticPipe(t, stderrOut)
	}
	cmd.Stderr = stderrOut
	cmd.ExtraFiles = []*os.File{signalOut}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	signalOut.Close()
	stderrOut.Close()
	reader := bufio.NewReader(control)
	require := func(want string) {
		t.Helper()
		line, err := reader.ReadString('\n')
		if strings.TrimSpace(line) != want || err != nil {
			t.Fatalf("control event = %q, %v; want %q", line, err, want)
		}
	}
	if mode != "blocked_startup" && mode != "closed_stderr" {
		require("ready")
		switch mode {
		case "first_signal", "forced_second_signal":
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
		case "blocked_drain":
			stdin.Close()
		}
		require("owner_stop")
	}
	switch mode {
	case "blocked_operation":
		require("finalizing")
	case "closed_stderr":
		line, err := reader.ReadString('\n')
		if err != nil || (line != "finalizing\n" && line != "pending_finalization\n") {
			t.Fatalf("unexpected closed-pipe finalization: %q, %v", line, err)
		}
	default:
		require("pending_finalization")
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatalf("lock held during diagnostic finalization: %v", err)
	}
	l.Release()
	if mode == "forced_second_signal" {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		require("forced_exit")
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		t.Fatalf("unexpected control data or read error: %v", err)
	}
	err = cmd.Wait()
	var ex *exec.ExitError
	if !errors.As(err, &ex) || ex.ExitCode() != exitError || stdout.Len() != 0 {
		t.Fatalf("child exit=%v stdout=%q; want exit 1 and empty stdout", err, stdout.String())
	}
	ws := ex.Sys().(syscall.WaitStatus)
	if !ws.Exited() {
		t.Fatalf("child terminated by %v instead of returning exit 1", ws.Signal())
	}
	t.Logf("child_exit=%d expected_exit=1 signal=none forced_callback=%t", ex.ExitCode(), mode == "forced_second_signal")
}

func nativeTestPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return r, w
}

func fillDiagnosticPipe(t *testing.T, w *os.File) {
	t.Helper()
	fd := int(w.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	var filler [4096]byte
	for _, size := range []int{len(filler), 1} {
		for {
			if _, err := unix.Write(fd, filler[:size]); errors.Is(err, unix.EAGAIN) {
				break
			} else if err != nil && !errors.Is(err, unix.EINTR) {
				t.Fatal(err)
			}
		}
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		t.Fatal(err)
	}
}
