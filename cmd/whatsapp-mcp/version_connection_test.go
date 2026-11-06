package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

type discoveryStub struct {
	calls  *atomic.Int64
	status int
	body   string
}

func (d discoveryStub) RoundTrip(*http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return &http.Response{StatusCode: d.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(d.body)), ContentLength: int64(len(d.body))}, nil
}

// orderedSession records how many discovery fetches had happened when Connect was called.
type orderedSession struct {
	*connectingSession
	fetches   *atomic.Int64
	atConnect *atomic.Int64
}

func (o *orderedSession) Connect(ctx context.Context) error {
	o.atConnect.Store(o.fetches.Load())
	return o.connectingSession.Connect(ctx)
}

func runPairWithDiscovery(t *testing.T, stub discoveryStub, atConnect *atomic.Int64) (code int, stdout, stderr string) {
	t.Helper()
	dir := privateDir(t)
	seedSavedIdentity(t, dir)
	prevOwner, prevSeam := newOwner, ownerSeam
	t.Cleanup(func() { newOwner, ownerSeam = prevOwner, prevSeam })
	newOwner = productionOwner
	ownerSeam = &ownerSeamConfig{
		discoveryRoundTripper: stub,
		sessionFactory: func(cfg wa.SessionConfig) (wa.SessionHandle, error) {
			s, err := wa.OpenSession(cfg)
			if err != nil {
				return nil, err
			}
			return &orderedSession{
				connectingSession: &connectingSession{Session: s, dispatch: cfg.Dispatch, epoch: cfg.Epoch},
				fetches:           stub.calls, atConnect: atConnect,
			}, nil
		},
	}
	var out, diag bytes.Buffer
	code = run([]string{"pair", "--data-dir", dir, "--log-level", "debug"}, strings.NewReader(""), &out, &diag, nil)
	return code, out.String(), diag.String()
}

// Discovery runs once, before the Connect, with no second hidden fetch, and nothing from the
// response reaches the output.
func TestVersionDiscoveryRunsOnceBeforeConnect(t *testing.T) {
	var calls, atConnect atomic.Int64
	code, stdout, stderr := runPairWithDiscovery(t, discoveryStub{calls: &calls, status: 200, body: `"client_revision":777,`}, &atConnect)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if calls.Load() != 1 {
		t.Errorf("%d discovery fetches in one command, want exactly 1", calls.Load())
	}
	if atConnect.Load() != 1 {
		t.Errorf("%d fetches had completed when Connect ran, want 1 (discovery before Connect)", atConnect.Load())
	}
	if strings.Contains(stdout, "777") || strings.Contains(stderr, "client_revision") {
		t.Errorf("response content leaked:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// A failed discovery keeps the bundled version, the connection still proceeds, and the diagnostic
// carries a category only, never the response body.
func TestVersionDiscoveryFailureFallsBackWithCategoryOnly(t *testing.T) {
	var calls, atConnect atomic.Int64
	code, stdout, stderr := runPairWithDiscovery(t, discoveryStub{calls: &calls, status: 503, body: "SECRET-upstream-body"}, &atConnect)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "version_refresh_failed") || !strings.Contains(stderr, "reason=response") {
		t.Errorf("no category-only fallback diagnostic:\n%s", stderr)
	}
	if strings.Contains(stderr, "SECRET") || strings.Contains(stdout, "SECRET") {
		t.Errorf("the upstream body leaked:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if calls.Load() != 1 || atConnect.Load() != 1 {
		t.Errorf("fetches %d, at Connect %d; want 1 and 1", calls.Load(), atConnect.Load())
	}
}
