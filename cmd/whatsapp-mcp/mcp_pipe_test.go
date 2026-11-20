package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

const pipeChildMode = "WA_TEST_MCP_PIPE_MODE"
const pipeJID = "15550000001@s.whatsapp.net"

type pipeProbe struct {
	io.ReadWriter
	calls, completed atomic.Int32
	onFirst          func()
	pad              bool
}

func (p *pipeProbe) Write(b []byte) (int, error) {
	if p.calls.Add(1) == 1 {
		if p.onFirst != nil {
			p.onFirst()
		}
		if p.pad {
			// Fill the inherited pipe inside the first real diagnostic write.
			// The reporter receives no acknowledgment while the OS call blocks.
			if _, err := p.ReadWriter.Write(make([]byte, 256<<10)); err != nil {
				return 0, err
			}
		}
	}
	n, err := p.ReadWriter.Write(b)
	if err == nil && n == len(b) {
		p.completed.Add(1)
	}
	return n, err
}

func (p *pipeProbe) Read(b []byte) (int, error) {
	if p.calls.Add(1) == 1 && p.onFirst != nil {
		p.onFirst()
	}
	n, err := p.ReadWriter.Read(b)
	p.completed.Add(1)
	return n, err
}

type pipeCancelReader struct {
	mcp.ChatContactReader
	mark func(string)
}

func (r pipeCancelReader) GetChat(ctx context.Context, _ string, _ bool) (store.ChatView, error) {
	r.mark("tool-entered")
	<-ctx.Done()
	r.mark("tool-cancelled")
	return store.ChatView{}, ctx.Err()
}

func runMCPPipeChild(mode string) {
	dir, events := os.Getenv("WA_TEST_MCP_PIPE_DATA"), os.Getenv("WA_TEST_MCP_PIPE_EVENTS")
	mark := func(name string) {
		if err := os.WriteFile(filepath.Join(events, name), nil, 0600); err != nil {
			os.Exit(97)
		}
	}
	await := func(name string) {
		for {
			if _, err := os.Stat(filepath.Join(events, name)); err == nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	in := &pipeProbe{ReadWriter: os.Stdin, onFirst: func() { mark("stdin-entered") }}
	out := &pipeProbe{ReadWriter: os.Stdout}
	errOut := &pipeProbe{ReadWriter: os.Stderr, onFirst: func() { mark("stderr-entered") }, pad: pipeStderrBlocked(mode)}
	var st *store.Store
	lifecycleFactory = func(log *slog.Logger) *lifecycle {
		lc := newLifecycle(log)
		lc.exit = func(code int) { mark("forced"); os.Exit(code) }
		lc.diagnosticFinalizing = func(pending bool) {
			if st != nil {
				select {
				case <-st.Quiesced():
					mark("store-quiesced")
				default:
					os.Exit(98)
				}
			}
			mark(fmt.Sprintf("finalizing-%t", pending))
		}
		return lc
	}
	if mode == "client_cancel" {
		stdioToolReader = func(st *store.Store) mcp.ChatContactReader {
			return pipeCancelReader{st.ToolReader(), mark}
		}
	}
	newOwner = func(deps ownerDeps) (*ownerHandle, error) {
		st = deps.store
		if strings.HasSuffix(mode, "stdout") {
			if err := st.StoreMessage(context.Background(), store.Message{ChatJID: pipeJID, SenderJID: pipeJID, ID: "fixture", Time: time.Now(), Text: strings.Repeat("x", 240<<10)}); err != nil {
				return nil, err
			}
		}
		h := newHeldOwner()
		h.startHook = func() {
			mark("ready")
			if mode == "stderr_blocked_operation" || mode == "stderr_closed" {
				deps.log.Info("config")
				await("stderr-entered")
				for range 64 {
					deps.log.Info("config")
				}
				deps.log.Warn("loss", "event", "history_incomplete")
				deps.log.Warn("loss", "event", "receive_overload")
				mark("loss-submitted")
			}
		}
		return &ownerHandle{owner: &diagnosticChildOwner{heldOwner: h, stop: func() {
			mark("owner-stop")
			if mode == "stderr_blocked_drain" || mode == "stderr_blocked_overflow" || mode == "forced_second_signal" {
				await("stderr-entered")
				for range 64 {
					deps.log.Info("config")
				}
				deps.log.Warn("loss", "event", "history_incomplete")
				deps.log.Warn("loss", "event", "receive_overload")
				if mode == "stderr_blocked_overflow" {
					for range 17 {
						deps.log.Error("loss", "event", "receive_overload")
					}
				}
				mark("loss-submitted")
			}
			if mode == "device_fault_eof" {
				h.mu.Lock()
				h.health.StorageFault = true
				h.mu.Unlock()
			}
			if mode == "first_signal" {
				await("release-owner")
			}
			h.complete()
		}}}, nil
	}
	level := "info"
	if pipeStartupRefusal(mode) {
		level = "invalid"
	}
	code := run([]string{"stdio", "--data-dir", dir, "--log-level", level}, in, out, errOut, os.Environ())
	mark(fmt.Sprintf("stats-%d-%d-%d-%d-%d-%d", in.calls.Load(), in.completed.Load(), out.calls.Load(), out.completed.Load(), errOut.calls.Load(), errOut.completed.Load()))
	mark("normal-exit")
	os.Exit(code)
}

func pipeStartupRefusal(mode string) bool {
	return mode == "stderr_blocked_startup" || mode == "stderr_closed_startup"
}

func pipeStderrBlocked(mode string) bool {
	return strings.HasPrefix(mode, "stderr_blocked") || mode == "all_blocked_stdout" || mode == "forced_second_signal"
}

func TestMCPPipeNative(t *testing.T) {
	if mode := os.Getenv(pipeChildMode); mode != "" {
		runMCPPipeChild(mode)
	}
	for _, tc := range []struct {
		mode string
		want int
	}{
		{"first_signal", 0}, {"partial_stdin_eof", 1}, {"partial_stdin_timeout", 1},
		{"slow_stdout", 0}, {"blocked_stdout", 1}, {"all_blocked_stdout", 1}, {"closed_stdout", 1}, {"client_cancel", 0},
		{"stderr_blocked_startup", 1}, {"stderr_blocked_operation", 1}, {"stderr_blocked_drain", 1},
		{"stderr_blocked_overflow", 1}, {"stderr_closed", 1},
	} {
		t.Run(tc.mode, func(t *testing.T) { testMCPPipeChild(t, tc.mode, tc.want) })
	}
}

func testMCPPipeChild(t *testing.T, mode string, want int) {
	dir, events := privateDir(t), t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMCPPipeNative$")
	cmd.Env = childEnv(pipeChildMode+"="+mode, "WA_TEST_MCP_PIPE_DATA="+dir, "WA_TEST_MCP_PIPE_EVENTS="+events)
	if mode == "first_signal" || mode == "forced_second_signal" {
		startForSignal(cmd)
	}
	pipe := func() (*os.File, *os.File) {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close(); w.Close() })
		return r, w
	}
	inR, in := pipe()
	out, outW := pipe()
	stderr, errW := pipe()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	if strings.HasPrefix(mode, "stderr_closed") {
		stderr.Close()
	}
	if mode == "closed_stdout" {
		out.Close()
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	inR.Close()
	outW.Close()
	errW.Close()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	await := func(name string) {
		t.Helper()
		for ctx.Err() == nil {
			if _, err := os.Stat(filepath.Join(events, name)); err == nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("missing child event %s", name)
	}
	send := func(frame string) {
		t.Helper()
		if _, err := io.WriteString(in, frame); err != nil {
			t.Fatal(err)
		}
	}
	reader := bufio.NewReader(out)
	reply := func(wantID int) []byte {
		t.Helper()
		var line []byte
		var err error
		if mode == "slow_stdout" && wantID == 2 {
			buf := make([]byte, 2048)
			for len(line) < 2<<20 && (len(line) == 0 || line[len(line)-1] != '\n') {
				n, readErr := reader.Read(buf)
				line = append(line, buf[:n]...)
				if readErr != nil {
					err = readErr
					break
				}
				time.Sleep(time.Millisecond)
			}
		} else {
			line, err = reader.ReadBytes('\n')
		}
		var response struct{ ID int }
		if err != nil || json.Unmarshal(line, &response) != nil || response.ID != wantID {
			t.Fatalf("invalid reply: want id=%d, bytes=%d, err=%v", wantID, len(line), err)
		}
		return line
	}
	if !pipeStartupRefusal(mode) && mode != "stderr_closed" {
		await("stdin-entered")
	}
	const init = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n"
	const initialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	const tool = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_chat","arguments":{"chat_jid":"` + pipeJID + `"}}}` + "\n"
	switch mode {
	case "first_signal", "forced_second_signal":
		if err := sendStop(cmd.Process, "interrupt"); err != nil {
			t.Fatalf("send native stop: %v", err)
		}
		await("owner-stop")
		if mode == "forced_second_signal" {
			break
		}
		if l, err := lock.Acquire(dir); err == nil {
			l.Release()
			t.Fatal("lock released while owner was held")
		}
		if err := os.WriteFile(filepath.Join(events, "release-owner"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	case "partial_stdin_timeout", "partial_stdin_eof":
		send(`{"jsonrpc":"2.0"`)
		if mode == "partial_stdin_eof" {
			in.Close()
		}
	case "closed_stdout":
		send(init)
	case "stderr_closed_ordinary", "device_fault_eof":
		send(init)
		reply(1)
		in.Close()
	case "slow_stdout", "blocked_stdout", "all_blocked_stdout", "client_cancel", "stderr_blocked_drain", "stderr_blocked_overflow":
		send(init)
		reply(1)
		send(initialized)
		if strings.HasPrefix(mode, "stderr_") {
			send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":7}}` + "\n")
			if !strings.Contains(string(reply(2)), `"code":-32602`) {
				t.Fatal("expected a pre-dispatch denial")
			}
			in.Close()
			break
		}
		send(tool)
		if mode == "slow_stdout" {
			if b := reply(2); len(b) < 100<<10 || !strings.Contains(string(b), `"id":"fixture"`) {
				t.Fatalf("missing large synthetic reply: %d bytes", len(b))
			}
			in.Close()
		} else if mode == "client_cancel" {
			await("tool-entered")
			send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}` + "\n")
			await("tool-cancelled")
			if !strings.Contains(string(reply(2)), `"isError":true`) {
				t.Fatal("cancelled tool reported success")
			}
			in.Close()
		}
	}
	if !pipeStartupRefusal(mode) {
		await("owner-stop")
		await("store-quiesced")
	}
	if pipeStderrBlocked(mode) {
		await("stderr-entered")
	}
	if mode == "stderr_blocked_operation" || mode == "stderr_blocked_overflow" || mode == "stderr_closed" {
		await("finalizing-false")
	} else if mode == "stderr_blocked_startup" || mode == "stderr_blocked_drain" || mode == "all_blocked_stdout" || mode == "forced_second_signal" {
		await("finalizing-true")
	}
	requireReleasedLock(t, dir)
	forced := mode == "forced_second_signal"
	if forced {
		if err := sendStop(cmd.Process, "terminate"); err != nil {
			t.Fatal(err)
		}
		await("forced")
	} else {
		await("normal-exit")
	}
	err := cmd.Wait()
	if ctx.Err() != nil || cmd.ProcessState.ExitCode() != want {
		t.Fatalf("child exit=%v code=%d want=%d context=%v", err, cmd.ProcessState.ExitCode(), want, ctx.Err())
	}
	if forced {
		if _, err := os.Stat(filepath.Join(events, "normal-exit")); !os.IsNotExist(err) {
			t.Fatalf("forced callback did not terminate the child: %v", err)
		}
		t.Logf("child_exit=%d expected_exit=%d normal_cleanup=true forced_callback=true", cmd.ProcessState.ExitCode(), want)
		return
	}
	files, err := os.ReadDir(events)
	if err != nil {
		t.Fatal(err)
	}
	var stats string
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "stats-") {
			stats = f.Name()
		}
		if f.Name() == "forced" {
			t.Fatal("forced exit was used")
		}
	}
	var ir, id, ow, od, ew, ed int
	if n, _ := fmt.Sscanf(stats, "stats-%d-%d-%d-%d-%d-%d", &ir, &id, &ow, &od, &ew, &ed); n != 6 {
		t.Fatalf("missing pump counts: %q", stats)
	}
	if pipeStderrBlocked(mode) && (ew != 1 || ed != 0) {
		t.Fatalf("blocked stderr retried or acknowledged: %s", stats)
	}
	if (mode == "blocked_stdout" || mode == "all_blocked_stdout") && (ow != 2 || od != 1 || ir != id+1) {
		t.Fatalf("blocked stdout did not retain one unacknowledged write: %s", stats)
	}
	if mode == "first_signal" && (ir != 1 || id != 0) {
		t.Fatalf("signal case did not leave exactly one blocked stdin read: %s", stats)
	}
	if strings.HasPrefix(mode, "stderr_closed") && (ew != 1 || ed != 0) {
		t.Fatalf("closed stderr retried or acknowledged: %s", stats)
	}
	if mode != "closed_stdout" && mode != "blocked_stdout" && mode != "all_blocked_stdout" {
		if excess, err := reader.ReadByte(); err != io.EOF {
			t.Fatalf("unexpected stdout after expected frames: byte=%d err=%v", excess, err)
		}
	}
	t.Logf("child_exit=%d expected_exit=%d normal_cleanup=true %s (stdin started/completed, stdout started/completed, stderr started/completed)", cmd.ProcessState.ExitCode(), want, stats)
}
