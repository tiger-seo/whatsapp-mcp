package wa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countedBody struct {
	io.Reader
	read   atomic.Int64
	closed atomic.Bool
}

func (b *countedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read.Add(int64(n))
	return n, err
}
func (b *countedBody) Close() error { b.closed.Store(true); return nil }

func TestBoundedHTTPConsumption(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		length     int64
		wantErr    bool
		maxRead    int64
	}{
		{"complete", "abc", 3, false, 3},
		{"advertised oversize", "abcde", 5, true, 0},
		{"unknown oversize", "abcde", -1, true, 4},
		{"unknown exact budget", "abcd", -1, true, 4},
		{"known exact budget", "abcd", 4, false, 4},
		{"truncated known length", "ab", 3, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &countedBody{Reader: strings.NewReader(tc.body)}
			client := newBoundedHTTPClient(time.Second, 4, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: tc.length, Body: body}, nil
			}))
			req, _ := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
			resp, err := client.Do(req)
			if err == nil {
				_, err = io.ReadAll(resp.Body)
				resp.Body.Close()
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if got := body.read.Load(); got > tc.maxRead {
				t.Errorf("consumed %d, maximum %d", got, tc.maxRead)
			}
			if !body.closed.Load() {
				t.Error("response body was not closed")
			}
		})
	}
}

func TestBoundedHTTPRejectsTLSRedirectAndCredentials(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	untrusted := newBoundedHTTPClient(time.Second, 100, nil)
	if _, err := untrusted.Get(server.URL); err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
	trusted := newBoundedHTTPClient(time.Second, 100, server.Client().Transport)
	resp, err := trusted.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, err := trusted.Get(server.URL + "/redirect"); err == nil {
		t.Fatal("redirect accepted")
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	req.Header.Set("Authorization", "SECRET")
	if _, err := trusted.Do(req); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("credential header result = %v", err)
	}
	if _, err := trusted.Get("http://example.invalid/"); err == nil {
		t.Fatal("plaintext HTTP accepted")
	}
	req, _ = http.NewRequest(http.MethodGet, server.URL, nil)
	req.Header["authorization"] = []string{"", "SECRET"}
	if _, err := trusted.Do(req); err == nil {
		t.Fatal("noncanonical credential header accepted")
	}
}

type stalledBody struct{ ctx context.Context }

func (s stalledBody) Read([]byte) (int, error) { <-s.ctx.Done(); return 0, s.ctx.Err() }
func (s stalledBody) Close() error             { return nil }

func TestBoundedHTTPDeadlineCoversBody(t *testing.T) {
	client := newBoundedHTTPClient(50*time.Millisecond, 100, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: -1, Body: stalledBody{ctx: r.Context()}}, nil
	}))
	resp, err := client.Get("https://example.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "timeout")) {
		t.Fatalf("stalled body: %v", err)
	}
}

func TestAppStateHTTPProfile(t *testing.T) {
	client := newAppStateHTTPClient()
	if client.Timeout != 10*time.Second {
		t.Errorf("timeout = %v", client.Timeout)
	}
	transport, ok := client.Transport.(*boundedTransport)
	if !ok || transport.limit != 16<<20 {
		t.Fatalf("app-state transport = %#v", client.Transport)
	}
	base, ok := transport.base.(*http.Transport)
	if !ok || base.Proxy != nil || (base.TLSClientConfig != nil && base.TLSClientConfig.InsecureSkipVerify) {
		t.Fatal("app-state transport permits a proxy or unverified TLS")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestAppStateHTTPEnforcesConsumedBudget(t *testing.T) {
	client := newAppStateHTTPClient()
	body := &countedBody{Reader: io.LimitReader(zeroReader{}, (16<<20)+1)}
	client.Transport.(*boundedTransport).base = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: -1, Body: body}, nil
	})
	resp, err := client.Get("https://example.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if !errors.Is(err, ErrHTTPBodyLimit) || body.read.Load() != 16<<20 {
		t.Fatalf("consumed=%d, err=%v; want 16 MiB and body limit", body.read.Load(), err)
	}
}
