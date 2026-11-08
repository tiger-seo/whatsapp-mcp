package wa

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fenceListener accepts connections and counts the bytes they send.
type fenceListener struct {
	ln       net.Listener
	received atomic.Int64
	ended    chan struct{}
	wg       sync.WaitGroup
}

func newFenceListener(t *testing.T) *fenceListener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	l := &fenceListener{ln: ln, ended: make(chan struct{}, 16)}
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			l.wg.Add(1)
			go func() {
				defer l.wg.Done()
				defer c.Close()
				defer func() { l.ended <- struct{}{} }()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					l.received.Add(int64(n))
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { ln.Close(); l.wg.Wait() })
	return l
}

func (l *fenceListener) addr() string { return l.ln.Addr().String() }

func TestFenceWrapsDialsBeforeCloseAndClosesThemAtClose(t *testing.T) {
	l := newFenceListener(t)
	f := newFence()
	c, err := f.DialContext(context.Background(), "tcp", l.addr())
	if err != nil {
		t.Fatalf("dial before close: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatalf("write before close: %v", err)
	}
	f.Close()
	one := make([]byte, 1)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(one); err == nil {
		t.Fatal("read on a connection the fence closed should fail")
	}
	if n := f.RefusedWrites() + f.RefusedDials(); n != 0 {
		t.Fatalf("a read that fails because the fence closed the connection was counted: %d", n)
	}
}

func TestFenceRefusesLaterDials(t *testing.T) {
	l := newFenceListener(t)
	f := newFence()
	f.Close()
	if c, err := f.DialContext(context.Background(), "tcp", l.addr()); err == nil {
		c.Close()
		t.Fatal("dial after close succeeded")
	}
	if c, err := f.DialContext(context.Background(), "tcp", l.addr()); err == nil {
		c.Close()
		t.Fatal("second dial after close succeeded")
	}
	if got := f.RefusedDials(); got != 2 {
		t.Fatalf("RefusedDials = %d, want 2", got)
	}
}

func TestFenceRefusesLaterWritesAndSendsNothing(t *testing.T) {
	l := newFenceListener(t)
	f := newFence()
	c, err := f.DialContext(context.Background(), "tcp", l.addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	f.Close()
	if n, err := c.Write([]byte("late")); err == nil || n != 0 {
		t.Fatalf("late write = %d, %v; want an error and 0 bytes", n, err)
	}
	if n, err := c.Write([]byte("later")); err == nil || n != 0 {
		t.Fatalf("second late write = %d, %v", n, err)
	}
	if got := f.RefusedWrites(); got != 2 {
		t.Fatalf("RefusedWrites = %d, want 2", got)
	}
	c.Close()
	select { // the server side saw the end of the stream, so everything that was sent has been counted
	case <-l.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the listener never saw the connection end")
	}
	if got := l.received.Load(); got != 0 {
		t.Fatalf("listener received %d bytes after the fence closed", got)
	}
}

func TestFencesAreIndependent(t *testing.T) {
	l := newFenceListener(t)
	a, b := newFence(), newFence()
	ca, err := a.DialContext(context.Background(), "tcp", l.addr())
	if err != nil {
		t.Fatal(err)
	}
	defer ca.Close()
	cb, err := b.DialContext(context.Background(), "tcp", l.addr())
	if err != nil {
		t.Fatal(err)
	}
	defer cb.Close()
	a.Close()
	if _, err := cb.Write([]byte("still open")); err != nil {
		t.Fatalf("closing one fence broke another's connection: %v", err)
	}
	extra, err := b.DialContext(context.Background(), "tcp", l.addr())
	if err != nil {
		t.Fatalf("closing one fence refused another's dial: %v", err)
	}
	defer extra.Close()
	if b.RefusedDials()+b.RefusedWrites() != 0 {
		t.Fatal("the open fence counted a refusal")
	}
}

// echoTransport is a test-injected transport that is not an *http.Transport.
type echoTransport struct{ calls atomic.Int64 }

func (e *echoTransport) RoundTrip(*http.Request) (*http.Response, error) {
	e.calls.Add(1)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(http.NoBody), Header: http.Header{}}, nil
}

func TestFenceHTTPClientCoversEveryClientBuiltFromIt(t *testing.T) {
	l := newFenceListener(t)
	f := newFence()
	base := &http.Client{Transport: &http.Transport{}}
	clients := []*http.Client{f.HTTPClient(base), f.HTTPClient(base), f.HTTPClient(&http.Client{Transport: &http.Transport{}})}
	f.Close()
	for i, c := range clients {
		req, _ := http.NewRequest(http.MethodGet, "http://"+l.addr()+"/", nil)
		if resp, err := c.Do(req); err == nil {
			resp.Body.Close()
			t.Fatalf("client %d reached the network after the fence closed", i)
		}
	}
	if got := f.RefusedDials(); got != 3 {
		t.Fatalf("RefusedDials = %d, want 3", got)
	}
	if l.received.Load() != 0 {
		t.Fatal("the listener received bytes from a fenced client")
	}
}

func TestFenceHTTPClientDialsThroughTheFenceBeforeClose(t *testing.T) {
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()
	f := newFence()
	c := f.HTTPClient(nil)
	resp, err := c.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("request before close: %v", err)
	}
	resp.Body.Close()
	f.Close()
	if resp, err := c.Get("http://" + ln.Addr().String() + "/"); err == nil {
		resp.Body.Close()
		t.Fatal("request after close succeeded")
	}
}

func TestFenceHTTPClientWrapsAnInjectedTransport(t *testing.T) {
	inner := &echoTransport{}
	f := newFence()
	c := f.HTTPClient(&http.Client{Transport: inner})
	resp, err := c.Get("http://example.invalid/")
	if err != nil {
		t.Fatalf("injected transport before close: %v", err)
	}
	resp.Body.Close()
	if inner.calls.Load() != 1 {
		t.Fatalf("injected transport calls = %d, want 1", inner.calls.Load())
	}
	f.Close()
	if resp, err := c.Get("http://example.invalid/"); err == nil {
		resp.Body.Close()
		t.Fatal("injected transport reached after close")
	}
	if inner.calls.Load() != 1 {
		t.Fatal("the injected transport was called after the fence closed")
	}
	if f.RefusedDials() != 1 {
		t.Fatalf("RefusedDials = %d, want 1", f.RefusedDials())
	}
}
