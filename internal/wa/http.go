package wa

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var (
	ErrHTTPPolicy    = errors.New("HTTP request rejected by policy")
	ErrHTTPBodyLimit = errors.New("HTTP response exceeded body budget")
)

type boundedTransport struct {
	base  http.RoundTripper
	limit int64
}

func (t *boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Scheme != "https" || req.URL.Host == "" || req.URL.User != nil {
		return nil, ErrHTTPPolicy
	}
	for name := range req.Header {
		if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization") || strings.EqualFold(name, "Cookie") {
			return nil, ErrHTTPPolicy
		}
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, ErrHTTPPolicy
	}
	if resp.ContentLength > t.limit {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, ErrHTTPBodyLimit
	}
	if resp.Body != nil {
		resp.Body = &boundedBody{source: resp.Body, limit: t.limit, expected: resp.ContentLength}
	}
	return resp, nil
}

type boundedBody struct {
	source   io.ReadCloser
	limit    int64
	expected int64
	consumed int64
	complete bool
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.complete {
		return 0, io.EOF
	}
	if b.expected >= 0 && b.consumed == b.expected {
		b.complete = true
		return 0, io.EOF
	}
	remaining := b.limit - b.consumed
	if remaining <= 0 {
		return 0, ErrHTTPBodyLimit
	}
	if b.expected >= 0 && b.expected-b.consumed < remaining {
		remaining = b.expected - b.consumed
	}
	if int64(len(p)) > remaining {
		p = p[:int(remaining)]
	}
	n, err := b.source.Read(p)
	b.consumed += int64(n)
	if errors.Is(err, io.EOF) {
		if b.expected >= 0 && b.consumed != b.expected {
			return n, io.ErrUnexpectedEOF
		}
		b.complete = true
	}
	return n, err
}
func (b *boundedBody) Close() error { return b.source.Close() }

func newBoundedHTTPClient(timeout time.Duration, limit int64, base http.RoundTripper) *http.Client {
	if base == nil {
		base = &http.Transport{
			Proxy:             nil,
			DialContext:       (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true, DisableCompression: true, MaxIdleConns: 100,
			IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	}
	return &http.Client{
		Transport: &boundedTransport{base: base, limit: limit}, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrHTTPPolicy },
	}
}

func newAppStateHTTPClient() *http.Client { return newBoundedHTTPClient(10*time.Second, 16<<20, nil) }
