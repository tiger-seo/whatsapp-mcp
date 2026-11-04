package wa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	wastore "go.mau.fi/whatsmeow/store"
)

const discoveryOrigin = "https://web.whatsapp.com"

var (
	ErrVersionFetch    = errors.New("version discovery failed")
	ErrVersionResponse = errors.New("version response invalid")
	revisionPattern    = regexp.MustCompile(`"client_revision":([0-9]+),`)
)

// Version is an immutable value suitable for freezing into one handshake.
type Version [3]uint32

func (v Version) String() string {
	return strconv.FormatUint(uint64(v[0]), 10) + "." + strconv.FormatUint(uint64(v[1]), 10) + "." + strconv.FormatUint(uint64(v[2]), 10)
}

// BundledVersion reads the pinned SDK version without starting discovery.
func BundledVersion() Version { return Version(wastore.GetWAVersion()) }

type versionFlight struct {
	done    chan struct{}
	err     error
	waiters int
}

// VersionProvider keeps the selected value independent of the SDK's global version.
type VersionProvider struct {
	owner    context.Context
	client   *http.Client
	origin   string
	selected atomic.Pointer[Version]
	mu       sync.Mutex
	flight   *versionFlight
}

// NewVersionProvider uses direct, verified HTTPS at the pinned discovery origin.
func NewVersionProvider(owner context.Context) *VersionProvider {
	return newVersionProvider(owner, newBoundedHTTPClient(15*time.Second, 1<<20, nil), discoveryOrigin)
}

// NewVersionProviderWithTransport builds the provider on the given base transport, for tests that
// substitute discovery. The bounded profile is unchanged.
func NewVersionProviderWithTransport(owner context.Context, base http.RoundTripper) *VersionProvider {
	return newVersionProvider(owner, newBoundedHTTPClient(15*time.Second, 1<<20, base), discoveryOrigin)
}

func newVersionProvider(owner context.Context, client *http.Client, origin string) *VersionProvider {
	p := &VersionProvider{owner: owner, client: client, origin: origin}
	bundled := BundledVersion()
	p.selected.Store(&bundled)
	return p
}

// Current takes a lock-free snapshot and performs no network or SDK-global mutation.
func (p *VersionProvider) Current() Version { return *p.selected.Load() }

// Refresh joins the owner-managed fetch; a waiter's cancellation cannot cancel another waiter.
func (p *VersionProvider) Refresh(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.owner.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	flight := p.flight
	if flight == nil {
		flight = &versionFlight{done: make(chan struct{})}
		p.flight = flight
		go p.refresh(flight)
	}
	flight.waiters++
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return flight.err
	}
}

func (p *VersionProvider) refresh(flight *versionFlight) {
	version, err := p.fetch()
	p.mu.Lock()
	if err == nil {
		if p.owner.Err() != nil {
			err = ErrVersionFetch
		} else {
			p.selected.Store(&version)
		}
	}
	flight.err = err
	p.flight = nil
	close(flight.done)
	p.mu.Unlock()
}

func (p *VersionProvider) fetch() (Version, error) {
	req, err := http.NewRequestWithContext(p.owner, http.MethodGet, p.origin, nil)
	if err != nil {
		return Version{}, ErrVersionFetch
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36")
	resp, err := p.client.Do(req)
	if err != nil {
		return Version{}, ErrVersionFetch
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Version{}, ErrVersionResponse
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Version{}, ErrVersionFetch
	}
	match := revisionPattern.FindSubmatch(body)
	if len(match) != 2 {
		return Version{}, ErrVersionResponse
	}
	revision, err := strconv.ParseUint(string(match[1]), 10, 32)
	if err != nil {
		return Version{}, ErrVersionResponse
	}
	return Version{2, 3000, uint32(revision)}, nil
}
