package wa

import (
	"context"
	"errors"
	"fmt"
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

// slowUploadServer reads an upload at about 1 MiB/s and answers with the number of bytes it received.
func slowUploadServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 128<<10)
		total := 0
		for {
			n, err := io.ReadFull(r.Body, buf)
			total += n
			if err != nil {
				break
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(125 * time.Millisecond):
			}
		}
		fmt.Fprint(w, total)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestMediaHTTPSendUploadBound(t *testing.T) {
	const size = 12 << 20
	server := slowUploadServer(t)
	upload := func(t *testing.T, bound time.Duration) (string, time.Duration, error) {
		t.Helper()
		client := newMediaHTTPClient(server.Client().Transport)
		ctx, cancel := context.WithTimeout(context.Background(), bound)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, io.LimitReader(zeroReader{}, size))
		if err != nil {
			t.Fatal(err)
		}
		req.ContentLength = size
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			return "", time.Since(start), err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return string(body), time.Since(start), err
	}
	t.Run("completes past the download bound", func(t *testing.T) {
		t.Parallel()
		body, took, err := upload(t, 2*time.Minute)
		if err != nil || body != fmt.Sprint(size) {
			t.Fatalf("upload = %q, %v after %v", body, err, took)
		}
		if took <= mediaRequestLimit {
			t.Errorf("the upload took %v, so it does not show that the 10-second bound is gone", took)
		}
	})
	t.Run("stopped at its deadline", func(t *testing.T) {
		t.Parallel()
		_, took, err := upload(t, 3*time.Second)
		if !errors.Is(err, context.DeadlineExceeded) || took < 2900*time.Millisecond || took > 6*time.Second {
			t.Fatalf("upload ended after %v with %v; want the 3-second deadline", took, err)
		}
	})
}

func TestMediaHTTPSendStalledDownloadStopsAtTenSeconds(t *testing.T) {
	t.Parallel()
	for name, bound := range map[string]time.Duration{"no deadline": 0, "a longer deadline": 2 * time.Minute} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := newMediaHTTPClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: -1, Body: stalledBody{ctx: r.Context()}}, nil
			}))
			ctx := context.Background()
			if bound > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, bound)
				defer cancel()
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid/", nil)
			start := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			took := time.Since(start)
			if !errors.Is(err, context.DeadlineExceeded) || took < 9900*time.Millisecond || took > 15*time.Second {
				t.Fatalf("stalled download ended after %v with %v; want 10 s", took, err)
			}
		})
	}
}

func TestMediaHTTPSendPolicy(t *testing.T) {
	var requests []*http.Request
	body := &countedBody{Reader: strings.NewReader("ok")}
	client := newMediaHTTPClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r)
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: 2, Body: body}, nil
	}))
	// An upload without a deadline is refused before it reaches the network, and its body is closed.
	sent := &countedBody{Reader: strings.NewReader("data")}
	req, _ := http.NewRequest(http.MethodPost, "https://example.invalid/", sent)
	if _, err := client.Do(req); !errors.Is(err, ErrHTTPPolicy) || len(requests) != 0 || !sent.closed.Load() {
		t.Fatalf("upload without a deadline: %v, %d requests, body closed %v", err, len(requests), sent.closed.Load())
	}
	// A download's context is released when its body closes, not before.
	resp, err := client.Get("https://example.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	ctx := requests[0].Context()
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > mediaRequestLimit {
		t.Errorf("download deadline = %v, %v", deadline, ok)
	}
	if ctx.Err() != nil {
		t.Errorf("context ended before the body closed: %v", ctx.Err())
	}
	resp.Body.Close()
	if ctx.Err() == nil || !body.closed.Load() {
		t.Error("closing the body did not release the request")
	}
	// The rest of the profile applies to uploads and downloads alike.
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		deadline, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		req, _ := http.NewRequestWithContext(deadline, method, "http://example.invalid/", nil)
		if _, err := client.Do(req); !errors.Is(err, ErrHTTPPolicy) {
			t.Errorf("%s over plain HTTP: %v", method, err)
		}
	}
	redirect := newMediaHTTPClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"https://example.invalid/next"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}))
	deadline, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	req, _ = http.NewRequestWithContext(deadline, http.MethodPost, "https://example.invalid/", strings.NewReader("x"))
	if _, err := redirect.Do(req); !errors.Is(err, ErrHTTPPolicy) {
		t.Errorf("redirected upload: %v", err)
	}
	oversize := newMediaHTTPClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: mediaBodyLimit + 1, Body: io.NopCloser(zeroReader{})}, nil
	}))
	req, _ = http.NewRequestWithContext(deadline, http.MethodPost, "https://example.invalid/", strings.NewReader("x"))
	if _, err := oversize.Do(req); !errors.Is(err, ErrHTTPBodyLimit) {
		t.Errorf("oversize upload response: %v", err)
	}
}
