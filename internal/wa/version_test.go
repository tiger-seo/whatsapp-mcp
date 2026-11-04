package wa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func responseTransport(body string, status int, length int64) http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: make(http.Header), ContentLength: length, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}

func TestVersionDiscoveryValidationAndFallback(t *testing.T) {
	for _, tc := range []struct {
		body     string
		status   int
		ok       bool
		revision uint32
	}{
		{`before "client_revision":0, after`, 200, true, 0},
		{`"client_revision":4294967295,`, 200, true, ^uint32(0)},
		{`"client_revision":4294967296,`, 200, false, 0},
		{`"client_revision":-1,`, 200, false, 0},
		{`"client_revision":123`, 200, false, 0},
		{`"client_revision":123,`, 503, false, 0},
	} {
		t.Run(tc.body, func(t *testing.T) {
			client := newBoundedHTTPClient(time.Second, 1<<20, responseTransport(tc.body, tc.status, int64(len(tc.body))))
			provider := newVersionProvider(context.Background(), client, "https://example.invalid/")
			bundled := provider.Current()
			err := provider.Refresh(context.Background())
			if (err == nil) != tc.ok {
				t.Fatalf("refresh = %v, want success %v", err, tc.ok)
			}
			if tc.ok && provider.Current() != (Version{2, 3000, tc.revision}) {
				t.Errorf("selected = %v", provider.Current())
			}
			if !tc.ok && provider.Current() != bundled {
				t.Error("invalid response replaced bundled candidate")
			}
			if err != nil && strings.Contains(err.Error(), tc.body) {
				t.Error("error contains response body")
			}
		})
	}
}

func TestVersionRejectsTruncatedValidPrefix(t *testing.T) {
	prefix := `"client_revision":123,`
	body := prefix + strings.Repeat("x", (1<<20)-len(prefix)+1)
	client := newBoundedHTTPClient(time.Second, 1<<20, responseTransport(body, 200, -1))
	provider := newVersionProvider(context.Background(), client, "https://example.invalid/")
	before := provider.Current()
	if err := provider.Refresh(context.Background()); err == nil {
		t.Fatal("oversize response accepted")
	}
	if provider.Current() != before {
		t.Fatal("truncated prefix changed candidate")
	}
}

func TestVersionRefreshSharesCallAndWaiterCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var count atomic.Int32
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if count.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		body := `"client_revision":42,`
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	provider := newVersionProvider(context.Background(), newBoundedHTTPClient(time.Second, 1<<20, transport), "https://example.invalid/")
	first := make(chan error, 1)
	waitCtx, cancel := context.WithCancel(context.Background())
	go func() { first <- provider.Refresh(waitCtx) }()
	<-entered
	second := make(chan error, 1)
	go func() { second <- provider.Refresh(context.Background()) }()
	deadline := time.After(time.Second)
	for {
		provider.mu.Lock()
		joined := provider.flight != nil && provider.flight.waiters == 2
		provider.mu.Unlock()
		if joined {
			break
		}
		select {
		case <-deadline:
			t.Fatal("second waiter did not join flight")
		default:
			runtime.Gosched()
		}
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter = %v", err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 || provider.Current() != (Version{2, 3000, 42}) {
		t.Fatalf("requests=%d candidate=%v", count.Load(), provider.Current())
	}
}

func TestVersionOwnerCancellationStopsRefresh(t *testing.T) {
	owner, stop := context.WithCancel(context.Background())
	entered := make(chan struct{})
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	provider := newVersionProvider(owner, newBoundedHTTPClient(time.Second, 1<<20, transport), "https://example.invalid/")
	done := make(chan error, 1)
	go func() { done <- provider.Refresh(context.Background()) }()
	<-entered
	stop()
	if err := <-done; err == nil {
		t.Fatal("owner cancellation accepted")
	}
}

func TestVersionLatestSuccessSurvivesLaterFailure(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `"client_revision":37,`
		status := 200
		if calls.Add(1) > 1 {
			body, status = "SECRET-body", 503
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	provider := newVersionProvider(context.Background(), newBoundedHTTPClient(time.Second, 1<<20, transport), "https://example.invalid/")
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.Refresh(context.Background()); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("second refresh = %v", err)
	}
	if got := provider.Current(); got != (Version{2, 3000, 37}) {
		t.Fatalf("latest candidate = %v", got)
	}
}

func TestVersionDiscoveryDoesNotExposeTransportError(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("SECRET-upstream-response") })
	provider := newVersionProvider(context.Background(), newBoundedHTTPClient(time.Second, 1<<20, transport), "https://example.invalid/")
	err := provider.Refresh(context.Background())
	if !errors.Is(err, ErrVersionFetch) || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("refresh error = %v", err)
	}
}

func TestVersionProviderUsesPinnedSecureProfile(t *testing.T) {
	provider := NewVersionProvider(context.Background())
	if provider.origin != discoveryOrigin || provider.client.Timeout != 15*time.Second {
		t.Fatalf("discovery profile = %q, %v", provider.origin, provider.client.Timeout)
	}
	transport, ok := provider.client.Transport.(*boundedTransport)
	if !ok || transport.limit != 1<<20 {
		t.Fatalf("discovery transport = %#v", provider.client.Transport)
	}
	if got := provider.Current(); got != BundledVersion() {
		t.Fatalf("bundled candidate = %v", got)
	}
}
