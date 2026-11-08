package wa

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// errFenceClosed is what a fenced dial, write or request returns once the generation's network is blocked.
var errFenceClosed = errors.New("network fence is closed")

// fence is one generation's network block. Every connection the generation dials goes through it. Closing it
// closes the connections it wrapped and from then on refuses, and counts, every dial and every write. It is
// never shared with another generation.
type fence struct {
	dialer net.Dialer

	mu     sync.Mutex
	closed bool
	conns  map[*fenceConn]struct{}

	refusedDials  atomic.Uint64
	refusedWrites atomic.Uint64
}

func newFence() *fence {
	return &fence{
		dialer: net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second},
		conns:  map[*fenceConn]struct{}{},
	}
}

// fenceConn refuses and counts writes once its fence is closed. Reads are left alone: a read that fails because
// the fence closed the connection is the fence's own doing, not a late call.
type fenceConn struct {
	net.Conn
	f *fence
}

func (c *fenceConn) Write(p []byte) (int, error) {
	if c.f.isClosed() {
		c.f.refusedWrites.Add(1)
		return 0, errFenceClosed
	}
	return c.Conn.Write(p)
}

func (c *fenceConn) Close() error {
	c.f.forget(c)
	return c.Conn.Close()
}

func (f *fence) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fence) forget(c *fenceConn) {
	f.mu.Lock()
	delete(f.conns, c)
	f.mu.Unlock()
}

// track registers a freshly dialled connection, or closes it and refuses when the fence closed meanwhile.
func (f *fence) track(raw net.Conn) (net.Conn, error) {
	c := &fenceConn{Conn: raw, f: f}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		_ = raw.Close()
		f.refusedDials.Add(1)
		return nil, errFenceClosed
	}
	f.conns[c] = struct{}{}
	f.mu.Unlock()
	return c, nil
}

// DialContext dials through the fence. After Close it dials nothing and counts a refused dial.
func (f *fence) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if f.isClosed() {
		f.refusedDials.Add(1)
		return nil, errFenceClosed
	}
	raw, err := f.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return f.track(raw)
}

// Close closes every wrapped connection and blocks later dials and writes. It is idempotent.
func (f *fence) Close() {
	f.mu.Lock()
	f.closed = true
	conns := make([]*fenceConn, 0, len(f.conns))
	for c := range f.conns {
		conns = append(conns, c)
	}
	f.conns = map[*fenceConn]struct{}{}
	f.mu.Unlock()
	for _, c := range conns {
		_ = c.Conn.Close()
	}
}

// RefusedDials is the number of dials attempted after Close.
func (f *fence) RefusedDials() uint64 { return f.refusedDials.Load() }

// RefusedWrites is the number of writes attempted on a wrapped connection after Close.
func (f *fence) RefusedWrites() uint64 { return f.refusedWrites.Load() }

// gateTransport refuses a request once the fence is closed and otherwise passes it to a transport this package
// did not build (a test-injected one), which the fence cannot reach through a dial hook.
type gateTransport struct {
	f    *fence
	next http.RoundTripper
}

func (g *gateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if g.f.isClosed() {
		g.f.refusedDials.Add(1)
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, errFenceClosed
	}
	return g.next.RoundTrip(req)
}

// HTTPClient returns a clone of base whose connections go through the fence. A nil base, or a base without a
// transport, gets the library's own default (a clone of http.DefaultTransport). A transport that is an
// *http.Transport is cloned and its dial hooks are wrapped, so an injected dial target is kept; any other
// transport is wrapped in a gate that refuses requests after Close.
func (f *fence) HTTPClient(base *http.Client) *http.Client {
	var c http.Client
	if base != nil {
		c = *base
	}
	c.Transport = f.RoundTripper(c.Transport)
	return &c
}

// RoundTripper returns base carried through the fence. A nil base gets the library's own default (a clone of
// http.DefaultTransport); an *http.Transport is cloned with its dial hooks wrapped, so an injected dial target
// is kept; any other transport is wrapped in a gate that refuses requests after Close.
func (f *fence) RoundTripper(base http.RoundTripper) http.RoundTripper {
	switch t := base.(type) {
	case nil:
		return f.fenceTransport(http.DefaultTransport.(*http.Transport).Clone())
	case *http.Transport:
		return f.fenceTransport(t.Clone())
	default:
		return &gateTransport{f: f, next: t}
	}
}

func (f *fence) fenceTransport(t *http.Transport) http.RoundTripper {
	dial := t.DialContext
	if dial == nil {
		dial = f.dialer.DialContext
	}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if f.isClosed() {
			f.refusedDials.Add(1)
			return nil, errFenceClosed
		}
		raw, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return f.track(raw)
	}
	if tls := t.DialTLSContext; tls != nil {
		t.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if f.isClosed() {
				f.refusedDials.Add(1)
				return nil, errFenceClosed
			}
			raw, err := tls(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return f.track(raw)
		}
	}
	// A request already queued on an idle connection must not slip past the fence, so the gate also checks.
	return &gateTransport{f: f, next: t}
}
