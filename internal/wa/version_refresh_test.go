package wa

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These cover the discovery failure surface that a periodic refresh reuses: every failure keeps
// the selected candidate, and nothing from the response reaches the error.

func refreshProvider(t *testing.T, transport http.RoundTripper, timeout time.Duration) *VersionProvider {
	t.Helper()
	p := newVersionProvider(context.Background(), newBoundedHTTPClient(timeout, 1<<20, transport), "https://example.invalid/")
	p.selected.Store(&Version{2, 3000, 77})
	return p
}

// An unknown-length body that fills the budget is rejected by design (see TestBoundedHTTPConsumption),
// so only the declared-length boundary is asserted here.
func TestVersionBodyBoundary(t *testing.T) {
	const limit = 1 << 20
	prefix := `"client_revision":123,`
	atLimit := prefix + strings.Repeat("x", limit-len(prefix))
	over := atLimit + "x"
	for _, tc := range []struct {
		name   string
		body   string
		length int64
		ok     bool
	}{
		{"exact_limit_declared", atLimit, int64(len(atLimit)), true},
		{"one_over_declared", over, int64(len(over)), false},
		{"one_over_unknown_length", over, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := refreshProvider(t, responseTransport(tc.body, 200, tc.length), time.Second)
			err := p.Refresh(context.Background())
			if (err == nil) != tc.ok {
				t.Fatalf("refresh = %v, want success %v", err, tc.ok)
			}
			want := Version{2, 3000, 77}
			if tc.ok {
				want = Version{2, 3000, 123}
			}
			if p.Current() != want {
				t.Errorf("selected = %v, want %v", p.Current(), want)
			}
		})
	}
}

func TestVersionRefreshFailuresKeepCandidate(t *testing.T) {
	redirect := roundTripFunc(func(*http.Request) (*http.Response, error) {
		h := http.Header{}
		h.Set("Location", "https://other.invalid/")
		return &http.Response{StatusCode: http.StatusFound, Header: h, Body: io.NopCloser(strings.NewReader(`"client_revision":999,`))}, nil
	})
	stall := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	for _, tc := range []struct {
		name      string
		transport http.RoundTripper
	}{
		{"not_found", responseTransport("SECRET-body", 404, 11)},
		{"server_error", responseTransport("SECRET-body", 500, 11)},
		{"redirect_with_valid_looking_body", redirect},
		{"stalled_until_timeout", stall},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := refreshProvider(t, tc.transport, 50*time.Millisecond)
			err := p.Refresh(context.Background())
			if err == nil {
				t.Fatal("failure accepted")
			}
			if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "999") {
				t.Errorf("error exposes the response: %v", err)
			}
			if p.Current() != (Version{2, 3000, 77}) {
				t.Errorf("failure replaced the candidate: %v", p.Current())
			}
		})
	}
}

// A server with an untrusted certificate must fail verification; the candidate is kept and the
// server's body never selects a version.
func TestVersionRefreshRejectsUntrustedTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `"client_revision":999,`)
	}))
	defer srv.Close()
	p := newVersionProvider(context.Background(), newBoundedHTTPClient(2*time.Second, 1<<20, nil), srv.URL)
	p.selected.Store(&Version{2, 3000, 77})
	if err := p.Refresh(context.Background()); err == nil {
		t.Fatal("an untrusted certificate was accepted")
	}
	if p.Current() != (Version{2, 3000, 77}) {
		t.Errorf("candidate = %v", p.Current())
	}
}

// A failed refresh must not disturb what a healthy session already uses: the provider only swaps
// an immutable snapshot, so a value read earlier stays as read.
func TestVersionFailedRefreshLeavesEarlierSnapshotIntact(t *testing.T) {
	p := refreshProvider(t, responseTransport("", 503, 0), time.Second)
	snapshot := p.Current()
	if err := p.Refresh(context.Background()); err == nil {
		t.Fatal("failure accepted")
	}
	if snapshot != (Version{2, 3000, 77}) || p.Current() != snapshot {
		t.Errorf("snapshot %v, current %v", snapshot, p.Current())
	}
}

// WhatsApp serves the page that carries the client revision only to a request that looks like a
// browser navigation. Without these headers it returns a page without the revision (observed on a
// real run, where every refresh failed with an invalid response).
func TestVersionRequestLooksLikeANavigation(t *testing.T) {
	var got http.Header
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Clone()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`"client_revision":123,`)), ContentLength: -1}, nil
	})
	p := refreshProvider(t, transport, time.Second)
	if err := p.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "none",
		"Sec-Fetch-User": "?1", "Accept-Language": "en-US,en;q=0.9",
	} {
		if got.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, got.Get(k), want)
		}
	}
	if !strings.Contains(got.Get("Accept"), "text/html") || !strings.Contains(got.Get("User-Agent"), "Mozilla/5.0") {
		t.Errorf("Accept %q, User-Agent %q", got.Get("Accept"), got.Get("User-Agent"))
	}
}

func TestVersionFailureReasonNamesTheCategoryOnly(t *testing.T) {
	for err, want := range map[error]string{
		ErrVersionResponse: "response", ErrVersionFetch: "fetch", context.Canceled: "other",
	} {
		if got := versionFailureReason(err); got != want {
			t.Errorf("%v: reason %q, want %q", err, got, want)
		}
	}
}
