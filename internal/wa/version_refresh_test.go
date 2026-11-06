package wa

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
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

// The six-hour scheduler (fake time). The bundled value is the fallback; each scheduled success
// publishes a new immutable candidate and nothing else.

func counting(revisions *atomic.Int64, calls *atomic.Int64) http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		body := `"client_revision":` + strconv.FormatInt(revisions.Add(1), 10) + `,`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
	})
}

func TestVersionSchedulerRefreshesEverySixHours(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var rev, calls atomic.Int64
		rev.Store(100)
		owner, stop := context.WithCancel(context.Background())
		p := newVersionProvider(owner, newBoundedHTTPClient(15*time.Second, 1<<20, counting(&rev, &calls)), "https://example.invalid/")
		done := make(chan struct{})
		go func() { p.RunRefresh(nil); close(done) }()
		synctest.Sleep(versionRefreshInterval - time.Minute)
		synctest.Wait()
		if calls.Load() != 0 {
			t.Fatalf("%d fetches before the first interval; the initial fetch belongs to the owner", calls.Load())
		}
		synctest.Sleep(time.Minute)
		synctest.Wait()
		if calls.Load() != 1 || p.Current() != (Version{2, 3000, 101}) {
			t.Fatalf("after 6h: %d fetches, candidate %v", calls.Load(), p.Current())
		}
		synctest.Sleep(versionRefreshInterval)
		synctest.Wait()
		if calls.Load() != 2 || p.Current() != (Version{2, 3000, 102}) {
			t.Fatalf("after 12h: %d fetches, candidate %v", calls.Load(), p.Current())
		}
		stop()
		synctest.Wait()
		<-done
		synctest.Sleep(3 * versionRefreshInterval)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Errorf("%d fetches after the owner stopped", calls.Load())
		}
	})
}

// A scheduled failure keeps the earlier candidate, reports a category only, and the next interval
// still fires.
func TestVersionSchedulerFailureKeepsCandidateAndContinues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			n := calls.Add(1)
			body, status := "SECRET-body", 503
			if n == 2 {
				body, status = `"client_revision":55,`, 200
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
		})
		owner, stop := context.WithCancel(context.Background())
		p := newVersionProvider(owner, newBoundedHTTPClient(15*time.Second, 1<<20, transport), "https://example.invalid/")
		bundled := p.Current()
		var reasons []string
		done := make(chan struct{})
		go func() {
			p.RunRefresh(func(err error) { reasons = append(reasons, versionFailureReason(err)); _ = err })
			close(done)
		}()
		synctest.Sleep(versionRefreshInterval)
		synctest.Wait()
		if p.Current() != bundled || len(reasons) != 1 || reasons[0] != "response" {
			t.Fatalf("after a failure: candidate %v, reasons %v", p.Current(), reasons)
		}
		synctest.Sleep(versionRefreshInterval)
		synctest.Wait()
		if p.Current() != (Version{2, 3000, 55}) {
			t.Errorf("the next interval did not refresh: %v", p.Current())
		}
		stop()
		synctest.Wait()
		<-done
	})
}

// A scheduled tick and an explicit refresh share one fetch, and a waiter that gives up cannot
// cancel the shared fetch.
func TestVersionSchedulerSharesOneFetchWithExplicitRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		release := make(chan struct{})
		transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			body := `"client_revision":9,`
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
		})
		owner, stop := context.WithCancel(context.Background())
		p := newVersionProvider(owner, newBoundedHTTPClient(time.Hour, 1<<20, transport), "https://example.invalid/")
		go p.RunRefresh(nil)
		synctest.Sleep(versionRefreshInterval)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("%d scheduled fetches in flight at the interval, want 1", calls.Load())
		}
		waiter, giveUp := context.WithCancel(context.Background())
		errc := make(chan error, 1)
		go func() { errc <- p.Refresh(waiter) }()
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("%d fetches in flight; the explicit refresh must join the scheduled one", calls.Load())
		}
		giveUp()
		synctest.Wait()
		if err := <-errc; err == nil {
			t.Fatal("a cancelled waiter reported success")
		}
		close(release)
		synctest.Wait()
		if p.Current() != (Version{2, 3000, 9}) {
			t.Errorf("the waiter's cancellation cancelled the shared fetch: %v", p.Current())
		}
		stop()
		synctest.Wait()
	})
}

// Publishing a new candidate never touches a healthy connection: no disconnect, no new connect,
// the phase stays Connected.
func TestVersionSchedulerLeavesHealthyConnectionAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ot := newOwnerTest(t, true, true, errScript(map[int64]error{}))
		var rev, calls atomic.Int64
		rev.Store(500)
		ot.o.cfg.Versions.client = newBoundedHTTPClient(15*time.Second, 1<<20, counting(&rev, &calls))
		providerOwner, stopProvider := context.WithCancel(context.Background())
		ot.o.cfg.Versions.owner = providerOwner
		if err := ot.o.Start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		synctest.Wait()
		ot.sess.send(SessionConnected)
		synctest.Wait()
		if !ot.o.Snapshot().Connected {
			t.Fatalf("not connected before the refresh: %+v", ot.o.Snapshot())
		}
		connects, disconnects := ot.sess.connectN.Load(), ot.sess.disconnectN.Load()
		go ot.o.cfg.Versions.RunRefresh(nil)
		synctest.Sleep(versionRefreshInterval + time.Minute)
		synctest.Wait()
		if ot.o.cfg.Versions.Current()[2] <= 500 {
			t.Fatalf("no scheduled refresh happened: %v", ot.o.cfg.Versions.Current())
		}
		if ot.sess.disconnectN.Load() != disconnects || ot.sess.connectN.Load() != connects {
			t.Errorf("a refresh disturbed the session: disconnects %d (was %d), connects %d (was %d)", ot.sess.disconnectN.Load(), disconnects, ot.sess.connectN.Load(), connects)
		}
		if !ot.o.Snapshot().Connected {
			t.Errorf("the refresh ended the connection: %+v", ot.o.Snapshot())
		}
		stopProvider()
		stopInBubble(t, ot)
	})
}
