package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

// binary is used only for the version process test.
var binary string

func TestMain(m *testing.M) {
	if os.Getenv("WA_TEST_FORCED_SHUTDOWN_DIR") != "" {
		os.Exit(m.Run())
	}
	if len(os.Args) > 1 && os.Args[1] == "--synthetic-process" {
		newOwner = func(ownerDeps) (*ownerHandle, error) {
			fake := wa.NewFake(wa.FakeFixtures{})
			fake.SetFailure("pair", errors.New("synthetic pairing disabled"))
			return &ownerHandle{owner: fake}, nil
		}
		os.Exit(run(os.Args[2:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
	}
	dir, err := os.MkdirTemp("", "whatsapp-mcp-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := "whatsapp-mcp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary = filepath.Join(dir, name)
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the binary: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// proc is a running whatsapp-mcp process with its output captured.
type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *syncBuffer
	stderr *syncBuffer
	done   chan struct{}
	code   int
}

// childEnv is the test's environment without any whatsapp-mcp variable, plus extra.
func childEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "WHATSAPP_MCP_") {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

// start uses a synthetic owner in a test subprocess, except for the version-only binary test.
// With openStdin the child's stdin stays open until closed; otherwise it reads EOF at once.
func start(t *testing.T, openStdin bool, env []string, args ...string) *proc {
	t.Helper()
	executable := binary
	if len(args) == 0 || args[0] != "version" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		args = append([]string{"--synthetic-process"}, args...)
	}
	p := &proc{cmd: exec.Command(executable, args...), stdout: &syncBuffer{}, stderr: &syncBuffer{}, done: make(chan struct{})}
	p.cmd.Env = env
	p.cmd.Stdout = p.stdout
	p.cmd.Stderr = p.stderr
	startForSignal(p.cmd)
	if openStdin {
		var err error
		if p.stdin, err = p.cmd.StdinPipe(); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		err := p.cmd.Wait()
		p.code = p.cmd.ProcessState.ExitCode()
		if err != nil && p.code == 0 {
			p.code = -1
		}
		close(p.done)
	}()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			p.cmd.Process.Kill()
			<-p.done
		}
		if p.stdin != nil {
			p.stdin.Close()
		}
	})
	return p
}

func (p *proc) waitFor(t *testing.T, line string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !strings.Contains(p.stderr.String(), line) {
		select {
		case <-p.done:
			t.Fatalf("process exited with %d before logging %q\nstderr:\n%s", p.code, line, p.stderr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q within %v\nstderr:\n%s", line, timeout, p.stderr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *proc) wait(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case <-p.done:
		return p.code
	case <-time.After(timeout):
		t.Fatalf("process still running after %v\nstderr:\n%s", timeout, p.stderr)
		return 0
	}
}

func (p *proc) expectExit(t *testing.T, code int, timeout time.Duration) {
	t.Helper()
	if got := p.wait(t, timeout); got != code {
		t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", got, code, p.stdout, p.stderr)
	}
}

func TestProcessVersion(t *testing.T) {
	p := start(t, false, childEnv("WHATSAPP_MCP_LOG_LEVEL=verbose"), "version")
	p.expectExit(t, exitOK, 10*time.Second)
	if !strings.HasPrefix(p.stdout.String(), "whatsapp-mcp dev\ncommit:  ") || p.stderr.String() != "" {
		t.Errorf("stdout = %q, stderr = %q", p.stdout, p.stderr)
	}
}

func TestProcessPairUsesSyntheticOwner(t *testing.T) {
	p := start(t, false, childEnv(), "pair", "--data-dir", privateDir(t))
	p.expectExit(t, exitError, 10*time.Second)
	if !strings.Contains(p.stderr.String(), "synthetic pairing disabled") {
		t.Errorf("pair did not reach the synthetic owner: %s", p.stderr)
	}
	if p.stdout.String() != "" {
		t.Errorf("pair wrote unexpected stdout: %q", p.stdout)
	}
}

func TestProcessSingleInstance(t *testing.T) {
	dir := privateDir(t)
	env := childEnv()
	holder := start(t, false, env, "serve", "--data-dir", dir)
	holder.waitFor(t, "serve: connection owner running", 10*time.Second)
	held := fmt.Sprintf("another whatsapp-mcp (PID %d) is using the data directory %s", holder.cmd.Process.Pid, dir)

	for _, name := range []string{"stdio", "pair"} {
		begin := time.Now()
		p := start(t, false, env, name, "--data-dir", dir)
		p.expectExit(t, exitError, 5*time.Second)
		if elapsed := time.Since(begin); elapsed > time.Second {
			t.Errorf("%s took %v to fail", name, elapsed)
		}
		if !strings.Contains(p.stderr.String(), held) {
			t.Errorf("%s: stderr = %q, want %q", name, p.stderr, held)
		}
	}

	status := start(t, false, env, "status", "--data-dir", dir)
	status.expectExit(t, exitError, 5*time.Second)
	if !strings.Contains(status.stderr.String(), held) {
		t.Errorf("status: stderr = %q, want %q", status.stderr, held)
	}

	other := start(t, true, env, "stdio", "--data-dir", dir+"-b")
	other.waitFor(t, "stdio: connection owner running", 10*time.Second)
	other.stdin.Close()
	other.expectExit(t, exitOK, 10*time.Second)

	if err := holder.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	holder.wait(t, 5*time.Second)
	next := start(t, false, env, "stdio", "--data-dir", dir)
	next.expectExit(t, exitOK, 10*time.Second)
}

func TestProcessStdoutStaysEmpty(t *testing.T) {
	p := start(t, true, childEnv(), "stdio", "--data-dir", privateDir(t), "--log-level", "debug")
	if _, err := io.WriteString(p.stdin, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\"}\n"); err != nil {
		t.Fatal(err)
	}
	if err := p.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	p.expectExit(t, exitOK, 10*time.Second)
	if n := len(p.stdout.String()); n != 0 {
		t.Errorf("stdio wrote %d bytes to stdout: %q", n, p.stdout)
	}
	for _, want := range []string{`reason="stdin closed"`, "level=DEBUG msg=config", `msg="lock acquired"`, "msg=stopped"} {
		if !strings.Contains(p.stderr.String(), want) {
			t.Errorf("stderr does not contain %q:\n%s", want, p.stderr)
		}
	}
}

func testStopSignal(t *testing.T, sig, reason string) {
	dir := privateDir(t)
	p := start(t, false, childEnv(), "serve", "--data-dir", dir)
	p.waitFor(t, "serve: connection owner running", 10*time.Second)
	if err := sendStop(p.cmd.Process, sig); err != nil {
		t.Fatalf("sending %s: %v", sig, err)
	}
	p.expectExit(t, exitOK, shutdownTimeout)
	for _, want := range []string{`msg="shutting down" reason=` + reason, "msg=stopped"} {
		if !strings.Contains(p.stderr.String(), want) {
			t.Errorf("stderr does not contain %q:\n%s", want, p.stderr)
		}
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		var he *lock.HeldError
		t.Fatalf("lock not free after shutdown: %v (held: %v)", err, errors.As(err, &he))
	}
	l.Release()
}

func TestProcessInterrupt(t *testing.T) {
	testStopSignal(t, "interrupt", "interrupt")
}

func TestProcessTerminate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no SIGTERM to send; console close is checked by hand")
	}
	testStopSignal(t, "terminate", "terminated")
}

// --- US1 T029: the shells over isolated synthetic paths in a real process. ---

// countingProxy counts connections routed through HTTPS_PROXY, not direct connections.
type countingProxy struct {
	listener net.Listener
	mu       sync.Mutex
	n        int
}

func startCountingProxy(t *testing.T) *countingProxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cp := &countingProxy{listener: l}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			cp.mu.Lock()
			cp.n++
			cp.mu.Unlock()
			conn.Close()
		}
	}()
	t.Cleanup(func() { l.Close() })
	return cp
}

func (cp *countingProxy) count() int {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.n
}

func (cp *countingProxy) proxyEnv() []string {
	return childEnv("HTTPS_PROXY=http://" + cp.listener.Addr().String())
}

// TestProcessUnpairedShellsStayOffline checks output and proxy-routed traffic for unpaired shells.
func TestProcessUnpairedShellsStayOffline(t *testing.T) {
	proxy := startCountingProxy(t)
	env := proxy.proxyEnv()
	for _, kind := range []string{"stdio", "serve"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateDir(t)
			p := start(t, true, env, kind, "--data-dir", dir)
			p.waitFor(t, kind+": connection owner running", 10*time.Second)
			// Give any unsolicited connection attempt a realistic window to show up.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if n := proxy.count(); n != 0 {
				t.Errorf("unpaired shells made %d proxy-routed connections", n)
			}
			if n := len(p.stdout.String()); n != 0 {
				t.Errorf("%s wrote %d bytes to stdout: %q", kind, n, p.stdout)
			}
			if strings.Contains(strings.ToLower(p.stderr.String()), "qr code") {
				t.Errorf("%s logged an unsolicited QR code: %s", kind, p.stderr)
			}
			select {
			case <-p.done:
				t.Fatalf("%s exited unexpectedly with %d: %s", kind, p.code, p.stderr)
			default:
			}
			if kind == "stdio" {
				p.stdin.Close()
				p.expectExit(t, exitOK, 10*time.Second)
			} else {
				if err := p.cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				p.wait(t, 5*time.Second)
			}
		})
	}
}
