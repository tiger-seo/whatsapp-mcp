package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFramingSerializedFrameBoundary(t *testing.T) {
	base, err := jsonrpc.EncodeMessage(&jsonrpc.Response{Result: []byte(`""`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, total := range []int{maxOutputFrame - 1, maxOutputFrame, maxOutputFrame + 1} {
		message := &jsonrpc.Response{Result: []byte(`"` + strings.Repeat("x", total-len(base)-1) + `"`)}
		if preflightFrame(message) {
			t.Fatalf("preflight unexpectedly accepted %d bytes", total)
		}
		encoded, err := jsonrpc.EncodeMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded)+1 != total {
			t.Fatalf("encoded frame = %d, want %d", len(encoded)+1, total)
		}
		if fitsOutputFrame(len(encoded)) != (total <= maxOutputFrame) {
			t.Fatalf("incorrect limit at %d", total)
		}
	}
}

func TestFramingInheritedPipes(t *testing.T) {
	if mode := os.Getenv("WA_INHERITED_PIPE_CHILD"); mode != "" {
		fail := func(s string) { os.Stderr.WriteString(s + "\n"); os.Exit(1) }
		tr := newFramedTransport(os.Stdin, os.Stdout)
		c, err := tr.Connect(context.Background())
		if err != nil {
			fail("connect failed")
		}
		readDone := make(chan error, 1)
		readStart := time.Now()
		go func() { _, err := c.Read(context.Background()); readDone <- err }()
		select {
		case <-readDone:
			fail("input did not block")
		case <-time.After(200 * time.Millisecond):
		}
		if mode == "input" {
			c.Close()
			select {
			case err := <-readDone:
				if err == nil {
					fail("read succeeded")
				}
			case <-time.After(time.Second):
				fail("read did not stop")
			}
		} else if mode == "partial" {
			select {
			case err := <-readDone:
				if err == nil || time.Since(readStart) < 9*time.Second {
					fail("partial frame deadline failed")
				}
			case <-time.After(11 * time.Second):
				fail("partial frame deadline failed")
			}
		} else {
			msg := &jsonrpc.Response{Result: []byte(`"` + strings.Repeat("x", 1<<20) + `"`)}
			start := time.Now()
			err = c.Write(context.WithoutCancel(context.Background()), msg)
			elapsed := time.Since(start)
			if !errors.Is(err, context.DeadlineExceeded) || elapsed < 4500*time.Millisecond || elapsed > 6*time.Second {
				fail("output deadline failed")
			}
			select {
			case err := <-readDone:
				if err == nil {
					fail("read succeeded")
				}
			case <-time.After(time.Second):
				fail("read did not stop")
			}
		}
		if _, err = tr.Connect(context.Background()); err == nil {
			fail("reconnected")
		}
		os.Stderr.WriteString("pipe child passed\n")
		os.Exit(0)
	}
	if runtime.GOOS != "windows" {
		t.Skip("native Windows inherited-pipe test")
	}
	for _, mode := range []string{"input", "partial", "output"} {
		t.Run(mode, func(t *testing.T) {
			inR, inW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer inW.Close()
			outR, outW, err := os.Pipe()
			if err != nil {
				inR.Close()
				t.Fatal(err)
			}
			defer outR.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFramingInheritedPipes$", "-test.timeout=14s")
			cmd.Env = append(os.Environ(), "WA_INHERITED_PIPE_CHILD="+mode)
			cmd.Stdin = inR
			cmd.Stdout = outW
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err = cmd.Start(); err != nil {
				inR.Close()
				outW.Close()
				t.Fatal(err)
			}
			inR.Close()
			outW.Close()
			if mode == "partial" {
				if _, err := inW.Write([]byte("{")); err != nil {
					t.Fatal(err)
				}
			}
			if err = cmd.Wait(); err != nil {
				t.Fatalf("child exit: %v; stderr: %s", err, stderr.String())
			}
			if !strings.Contains(stderr.String(), "pipe child passed") {
				t.Fatalf("missing child confirmation: %s", stderr.String())
			}
		})
	}
}

func TestFramingSingleBufferHandoff(t *testing.T) {
	frame := `{"jsonrpc":"2.0","method":"ping","id":1}` + "\n"
	r := &countingInput{r: strings.NewReader(strings.Repeat(frame, 3))}
	out := make(chan frameEvent)
	activity := make(chan frameActivity, 6)
	done := make(chan struct{})
	defer close(done)
	go readFrames(r, out, activity, done)
	first := <-out
	if len(first.data) != len(frame) || cap(first.data) != maxInputFrame {
		t.Fatal("wrong frame buffer")
	}
	addr := &first.data[0]
	time.Sleep(50 * time.Millisecond)
	if n := r.n.Load(); n != int64(len(frame)) {
		t.Fatalf("read %d bytes before release, want %d", n, len(frame))
	}
	close(first.release)
	second := <-out
	if &second.data[0] != addr {
		t.Fatal("reader allocated a replacement frame buffer")
	}
	close(second.release)
}

func TestFramingDecodedParamsSurviveBufferReuse(t *testing.T) {
	first := `{"jsonrpc":"2.0","method":"ping","params":{"value":"first"},"id":1}` + "\n"
	second := `{"jsonrpc":"2.0","method":"ping","params":{"value":"other"},"id":2}` + "\n"
	c := framingConnection(t, first+second)
	m, err := c.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := m.(*jsonrpc.Request)
	if _, err = c.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if string(request.Params) != `{"value":"first"}` {
		t.Fatalf("decoded params changed: %s", request.Params)
	}
}

func TestFramingPreflightBoundary(t *testing.T) {
	limit := maxOutputFrame - 1024
	for _, tc := range []struct {
		size   int
		accept bool
	}{{limit, true}, {limit + 1, false}} {
		raw := []byte(`"` + strings.Repeat("x", tc.size-2) + `"`)
		msg := &jsonrpc.Response{Result: raw}
		if preflightFrame(msg) != tc.accept {
			t.Fatalf("preflight size %d", tc.size)
		}
		r, w := io.Pipe()
		c, err := newFramedTransport(r, io.Discard).Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		err = c.Write(context.Background(), msg)
		c.Close()
		r.Close()
		w.Close()
		if (err == nil) != tc.accept {
			t.Fatalf("write size %d: %v", tc.size, err)
		}
	}
}

func TestFramingPreflightRawEscapes(t *testing.T) {
	for _, escaped := range []struct {
		literal string
		growth  int
	}{{"<&>", 15}, {"\u2028\u2029", 6}} {
		for _, extra := range []int{0, 1} {
			n := maxOutputFrame - 1024 - 2 - len(escaped.literal) - escaped.growth + extra
			raw := []byte(`"` + escaped.literal + strings.Repeat("x", n) + `"`)
			if got := preflightFrame(&jsonrpc.Response{Result: raw}); got != (extra == 0) {
				t.Fatalf("raw escape charge for %q, extra %d: %t", escaped.literal, extra, got)
			}
		}
	}
}

type neverStream struct{}

func (neverStream) Read([]byte) (int, error)  { select {} }
func (neverStream) Write([]byte) (int, error) { select {} }

func TestFramingNonCooperativeSubprocess(t *testing.T) {
	if os.Getenv("WA_FRAME_BLOCK_CHILD") == "1" {
		tr := newFramedTransport(neverStream{}, neverStream{})
		c, err := tr.Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		msg, err := jsonrpc.DecodeMessage([]byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		err = c.Write(context.WithoutCancel(context.Background()), msg)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 6*time.Second {
			t.Fatalf("write %v after %v", err, time.Since(start))
		}
		if _, err = c.Read(context.Background()); err == nil {
			t.Fatal("read succeeded after failure")
		}
		if _, err = tr.Connect(context.Background()); err == nil {
			t.Fatal("transport reconnected")
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFramingNonCooperativeSubprocess$", "-test.timeout=8s")
	cmd.Env = append(os.Environ(), "WA_FRAME_BLOCK_CHILD=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v: %s", err, output)
	}
}

type countingInput struct {
	r *strings.Reader
	n atomic.Int64
}

func (r *countingInput) Read(b []byte) (int, error) {
	n, err := r.r.Read(b)
	r.n.Add(int64(n))
	return n, err
}

func TestFramingIdleAndReadAhead(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		r, w := io.Pipe()
		defer r.Close()
		defer w.Close()
		c, err := newFramedTransport(r, io.Discard).Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		select {
		case <-c.(*framedConnection).done:
			t.Fatal("idle connection stopped")
		case <-time.After(10200 * time.Millisecond):
		}
	})
	t.Run("read ahead", func(t *testing.T) {
		frame := `{"jsonrpc":"2.0","method":"ping","id":1}` + "\n"
		r := &countingInput{r: strings.NewReader(strings.Repeat(frame, 3))}
		c, err := newFramedTransport(r, io.Discard).Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err = c.Read(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		if n := r.n.Load(); n > int64(2*len(frame)) {
			t.Fatalf("read ahead %d bytes", n)
		}
	})
}

func TestFramingDuplicateKeys(t *testing.T) {
	for _, input := range []string{
		`{"jsonrpc":"2.0","method":"ping","id":1,"id":2}`,
		`{"jsonrpc":"2.0","method":"ping","params":{"x":1,"\u0078":2},"id":1}`,
		`{"jsonrpc":"2.0","method":"ping","ID":9007199254740992}`,
	} {
		c := framingConnection(t, input+"\n")
		if _, err := c.Read(context.Background()); err == nil {
			t.Errorf("accepted duplicate in %s", input)
		}
	}
}

func TestFramingPartialDeadlineWithoutRead(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	c, err := newFramedTransport(r, io.Discard).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = w.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.(*framedConnection).done:
	case <-time.After(11 * time.Second):
		t.Fatal("partial frame did not stop without Read")
	}
}

func TestFramingPartialProgressDoesNotResetDeadline(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	c, err := newFramedTransport(r, io.Discard).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	if _, err = w.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(6 * time.Second)
	if _, err = w.Write([]byte(`"`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.(*framedConnection).done:
		if elapsed := time.Since(start); elapsed < 9*time.Second || elapsed > 11*time.Second {
			t.Fatalf("deadline after %v", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("partial progress reset deadline")
	}
}

type unsafeWireError struct{}

func (unsafeWireError) Error() string { panic("must not call untrusted Error") }

func TestFramingRejectsUnsafeError(t *testing.T) {
	c := framingConnection(t, "")
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("unsafe error invoked: %v", p)
		}
	}()
	err := c.Write(context.Background(), &jsonrpc.Response{Error: unsafeWireError{}})
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("unsafe error accepted: %v", err)
	}
}

func TestFramingTokenLimit(t *testing.T) {
	for _, tc := range []struct {
		n  int
		ok bool
	}{{4085, true}, {4086, false}} {
		input := `{"jsonrpc":"2.0","method":"ping","params":[` + strings.Repeat("0,", tc.n-1) + `0],"id":1}` + "\n"
		c := framingConnection(t, input)
		_, err := c.Read(context.Background())
		if (err == nil) != tc.ok {
			t.Errorf("%d array values: %v", tc.n, err)
		}
	}
}

type smallWriter struct{ bytes.Buffer }

func (w *smallWriter) Write(b []byte) (int, error) {
	if len(b) > 3 {
		b = b[:3]
	}
	return w.Buffer.Write(b)
}

func TestFramingPartialWritesAndTimeout(t *testing.T) {
	msg, err := jsonrpc.DecodeMessage([]byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("partial", func(t *testing.T) {
		out := new(smallWriter)
		r, w := io.Pipe()
		defer r.Close()
		defer w.Close()
		c, err := newFramedTransport(r, out).Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err = c.Write(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(out.String(), "\n") {
			t.Fatalf("incomplete write %q", out.String())
		}
	})
	t.Run("blocked", func(t *testing.T) {
		r, w := io.Pipe()
		defer r.Close()
		defer w.Close()
		out := &blockedWriter{make(chan struct{}), make(chan struct{})}
		defer close(out.release)
		c, err := newFramedTransport(r, out).Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		start := time.Now()
		err = c.Write(context.WithoutCancel(context.Background()), msg)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 6*time.Second {
			t.Fatalf("blocked write: %v after %v", err, time.Since(start))
		}
	})
}

func TestFramingOutputPreflight(t *testing.T) {
	if !fitsOutputFrame(maxOutputFrame-1) || fitsOutputFrame(maxOutputFrame) {
		t.Fatal("frame limit must include newline")
	}
	if preflightFrame(&jsonrpc.Response{Result: make([]byte, maxOutputFrame)}) {
		t.Fatal("oversized raw response accepted")
	}
	if preflightFrame(&jsonrpc.Response{Error: &jsonrpc.Error{Message: strings.Repeat("x", maxOutputFrame)}}) {
		t.Fatal("oversized error accepted")
	}
}

func framingConnection(t *testing.T, input string) sdk.Connection {
	t.Helper()
	c, err := newFramedTransport(strings.NewReader(input), &bytes.Buffer{}).Connect(context.Background())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestFramingShapeAndIDs(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		accept      bool
	}{
		{"depth16", `{"jsonrpc":"2.0","method":"ping","params":` + strings.Repeat("[", 15) + `0` + strings.Repeat("]", 15) + `,"id":1}` + "\n", true},
		{"depth17", `{"jsonrpc":"2.0","method":"ping","params":` + strings.Repeat("[", 16) + `0` + strings.Repeat("]", 16) + `,"id":1}` + "\n", false},
		{"fraction", `{"jsonrpc":"2.0","method":"ping","id":1.0}` + "\n", false},
		{"safe", `{"jsonrpc":"2.0","method":"ping","id":9007199254740991}` + "\n", true},
		{"unsafe", `{"jsonrpc":"2.0","method":"ping","id":9007199254740992}` + "\n", false},
		{"null", `{"jsonrpc":"2.0","method":"ping","id":null}` + "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := framingConnection(t, tc.input)
			_, err := c.Read(context.Background())
			if (err == nil) != tc.accept {
				t.Fatalf("Read = %v", err)
			}
		})
	}
}

type blockedWriter struct {
	entered chan struct{}
	release chan struct{}
}

type slowFrameWriter struct {
	calls atomic.Int32
	stop  chan struct{}
}

func (w *slowFrameWriter) Write([]byte) (int, error) {
	w.calls.Add(1)
	select {
	case <-time.After(100 * time.Millisecond):
		return 1, nil
	case <-w.stop:
		return 0, io.ErrClosedPipe
	}
}

func TestFramingSlowConsumerDeadline(t *testing.T) {
	r, input := io.Pipe()
	defer r.Close()
	defer input.Close()
	out := &slowFrameWriter{stop: make(chan struct{})}
	defer close(out.stop)
	tr := newFramedTransport(r, out)
	c, err := tr.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = c.Write(context.Background(), &jsonrpc.Response{Result: []byte(`"` + strings.Repeat("x", 1024) + `"`)})
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed < 4500*time.Millisecond || elapsed > 6*time.Second {
		t.Fatalf("slow write: %v after %v", err, elapsed)
	}
	if calls := out.calls.Load(); calls < 2 || calls >= 1024 {
		t.Fatalf("partial write calls = %d", calls)
	}
	if _, err = tr.Connect(context.Background()); !errors.Is(err, sdk.ErrConnectionClosed) {
		t.Fatalf("reconnect after timeout: %v", err)
	}
}

func TestFramingBlockedPumpHandoff(t *testing.T) {
	c := &framedConnection{done: make(chan struct{}), read: make(chan frameEvent), write: make(chan writeFrame), lock: make(chan struct{}, 1)}
	c.lock <- struct{}{}
	start := time.Now()
	err := c.Write(context.WithoutCancel(context.Background()), &jsonrpc.Response{Result: []byte(`"ok"`)})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) < 4500*time.Millisecond || time.Since(start) > 6*time.Second {
		t.Fatalf("handoff: %v after %v", err, time.Since(start))
	}
	select {
	case <-c.done:
	default:
		t.Fatal("handoff failure did not stop connection")
	}
}

func TestFramingCloseInterruptsUncancellableResponse(t *testing.T) {
	r, input := io.Pipe()
	defer r.Close()
	defer input.Close()
	out := &partialAfterCloseWriter{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(out.release)
	tr := newFramedTransport(r, out)
	c, err := tr.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- c.Write(context.WithoutCancel(parent), &jsonrpc.Response{Result: []byte(`"ok"`)}) }()
	<-out.entered
	cancel()
	select {
	case err := <-result:
		t.Fatalf("parent cancellation interrupted uncancellable write: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	c.Close()
	select {
	case err := <-result:
		if !errors.Is(err, sdk.ErrConnectionClosed) {
			t.Fatalf("write after close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("connection close did not interrupt write")
	}
	for i := 0; i < 3; i++ {
		if _, err = tr.Connect(context.Background()); !errors.Is(err, sdk.ErrConnectionClosed) {
			t.Fatalf("replacement connection: %v", err)
		}
		if err = c.Write(context.Background(), &jsonrpc.Response{Result: []byte(`"other"`)}); !errors.Is(err, sdk.ErrConnectionClosed) {
			t.Fatalf("replacement write: %v", err)
		}
	}
	if calls := out.calls.Load(); calls != 1 {
		t.Fatalf("writer calls while original blocked = %d", calls)
	}
}

type partialAfterCloseWriter struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (w *partialAfterCloseWriter) Write(b []byte) (int, error) {
	w.calls.Add(1)
	select {
	case <-w.entered:
	default:
		close(w.entered)
		<-w.release
	}
	return 1, nil
}

func TestFramingNoFurtherWritesAfterClose(t *testing.T) {
	r, input := io.Pipe()
	defer r.Close()
	defer input.Close()
	out := &partialAfterCloseWriter{entered: make(chan struct{}), release: make(chan struct{})}
	c, err := newFramedTransport(r, out).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- c.Write(context.Background(), &jsonrpc.Response{Result: []byte(`"ok"`)}) }()
	<-out.entered
	c.Close()
	if err := <-result; err == nil {
		t.Fatal("write succeeded after close")
	}
	close(out.release)
	time.Sleep(100 * time.Millisecond)
	if n := out.calls.Load(); n != 1 {
		t.Fatalf("writer called %d times after close, want 1", n)
	}
}

func TestFramingCancelledWriteNeverStarts(t *testing.T) {
	for i := 0; i < 100; i++ {
		r, input := io.Pipe()
		var out bytes.Buffer
		c, err := newFramedTransport(r, &out).Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = c.Write(ctx, &jsonrpc.Response{Result: []byte(`"ok"`)})
		c.Close()
		r.Close()
		input.Close()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled write: %v", err)
		}
	}
}

func TestFramingWriteCancellationAndFailure(t *testing.T) {
	t.Run("cancel blocked write", func(t *testing.T) {
		r, input := io.Pipe()
		defer r.Close()
		defer input.Close()
		out := &blockedWriter{make(chan struct{}), make(chan struct{})}
		defer close(out.release)
		c, err := newFramedTransport(r, out).Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- c.Write(ctx, &jsonrpc.Response{Result: []byte(`"ok"`)}) }()
		<-out.entered
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("write: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation did not interrupt write")
		}
		if _, err := c.Read(context.Background()); !errors.Is(err, sdk.ErrConnectionClosed) {
			t.Fatalf("read after cancellation: %v", err)
		}
	})
	t.Run("closed output", func(t *testing.T) {
		r, input := io.Pipe()
		defer r.Close()
		defer input.Close()
		output, w := io.Pipe()
		output.Close()
		defer w.Close()
		tr := newFramedTransport(r, w)
		c, err := tr.Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err = c.Write(context.Background(), &jsonrpc.Response{Result: []byte(`"ok"`)}); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("write: %v", err)
		}
		if _, err = tr.Connect(context.Background()); !errors.Is(err, sdk.ErrConnectionClosed) {
			t.Fatalf("reconnect: %v", err)
		}
	})
}

func TestFramingWriteTimeoutVisibleToGuardReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in, input := io.Pipe()
		defer in.Close()
		defer input.Close()
		out := &blockedWriter{make(chan struct{}), make(chan struct{})}
		defer close(out.release)
		c, err := newFramedTransport(in, out).Connect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		g := newGuardedConnection(c)
		defer g.Close()
		// Keep the writer from notifying the guard: the reader alone must
		// see the failure even before the guarded write can return.
		if err := c.Write(t.Context(), &jsonrpc.Response{Result: []byte(`"ok"`)}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked write: %v", err)
		}
		if _, err := g.Read(t.Context()); !errors.Is(err, errOutputFrame) || !g.closeFailed {
			t.Fatalf("reader lost terminal write failure: err=%v failed=%v", err, g.closeFailed)
		}
	})
}

func (w *blockedWriter) Write(b []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(b), nil
}

func TestFramingCloseInterruptsWrites(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	out := &blockedWriter{make(chan struct{}), make(chan struct{})}
	defer close(out.release)
	tr := newFramedTransport(r, out)
	c, err := tr.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	msg, err := jsonrpc.DecodeMessage([]byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { results <- c.Write(context.Background(), msg) }()
	<-out.entered
	go func() { results <- c.Write(context.Background(), msg) }()
	c.Close()
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err == nil {
				t.Fatal("write succeeded after close")
			}
		case <-time.After(time.Second):
			t.Fatal("write did not unblock")
		}
	}
	if _, err = tr.Connect(context.Background()); err == nil {
		t.Fatal("transport reconnected")
	}
}

func TestFramingInput(t *testing.T) {
	valid := `{"jsonrpc":"2.0","method":"ping","id":1}`
	for _, tc := range []struct {
		name, input string
		accept      bool
	}{
		{"valid", valid + "\n", true},
		{"limit", valid + strings.Repeat(" ", 65536-len(valid)-1) + "\n", true},
		{"CRLF limit", valid + strings.Repeat(" ", 65536-len(valid)-2) + "\r\n", true},
		{"CRLF over limit", valid + strings.Repeat(" ", 65536-len(valid)-1) + "\r\n", false},
		{"over limit", valid + strings.Repeat(" ", 65536-len(valid)) + "\n", false},
		{"batch", "[" + valid + "]\n", false},
		{"truncated", valid, false},
		{"malformed", "{\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := framingConnection(t, tc.input)
			_, err := c.Read(context.Background())
			if (err == nil) != tc.accept {
				t.Fatalf("Read error = %v, accept = %v", err, tc.accept)
			}
		})
	}
}
