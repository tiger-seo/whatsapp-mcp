package wa

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/walog"
	"go.mau.fi/whatsmeow"
	waWa6 "go.mau.fi/whatsmeow/proto/waWa6"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types/events"
)

// Session is one adapter generation around the pinned client: guarded device load, reviewed
// connection options and the immutable handshake callback. The owner drives attempts through the
// narrow handle; the raw client is never handed to consumers.
type Session struct {
	cfg         SessionConfig
	dev         *wastore.Device
	barrier     *store.DeviceBarrier
	client      *whatsmeow.Client
	mediaClient *http.Client
	epoch       uint64
	hasID       bool
	pendingRep  bool
	handlerID   uint32
	retired     bool

	mu sync.Mutex
}

var (
	_ SessionHandle   = (*Session)(nil)
	_ QRChannelSource = (*Session)(nil)
)

var (
	ErrSessionNoStore    = errors.New("session requires a store")
	ErrSessionNoVersions = errors.New("session requires a version provider")
	ErrSessionNoDispatch = errors.New("session requires a dispatch callback")
	ErrSessionNoLifetime = errors.New("session requires a lifetime context")
	ErrSessionRetired    = errors.New("the session generation is retired")
)

// defaultSession builds the production adapter for one generation.
func defaultSession(cfg SessionConfig) (SessionHandle, error) {
	return OpenSession(cfg)
}

// OpenSession builds one adapter generation: guarded device load, pending-repair admission before
// the client exists, the reviewed connection options, shared logging, bounded app-state HTTP and
// the immutable version/hash callback.
func OpenSession(cfg SessionConfig) (*Session, error) {
	switch {
	case cfg.Store == nil:
		return nil, ErrSessionNoStore
	case cfg.Versions == nil:
		return nil, ErrSessionNoVersions
	case cfg.Dispatch == nil:
		return nil, ErrSessionNoDispatch
	case cfg.Lifetime == nil:
		return nil, ErrSessionNoLifetime
	}
	dev, barrier, err := cfg.Store.LoadDevice(cfg.Lifetime)
	if err != nil {
		return nil, err
	}
	// Pending-repair admission is configured before any client can start syncing.
	if err := barrier.ConfigureRecovery(cfg.Lifetime); err != nil {
		_ = barrier.Retire(context.Background())
		return nil, err
	}
	pending, err := barrier.PendingRepairs(cfg.Lifetime)
	if err != nil {
		_ = barrier.Retire(context.Background())
		return nil, err
	}
	s := &Session{
		cfg:        cfg,
		dev:        dev,
		barrier:    barrier,
		epoch:      cfg.Epoch,
		hasID:      dev.ID != nil,
		pendingRep: len(pending) > 0,
	}
	client := whatsmeow.NewClient(dev, walog.New(cfg.Log))
	// The three reviewed reconnect flags: ordinary drops reconnect with the library's own backoff,
	// the initial attempt and 515 reconnects stay with the owner.
	client.EnableAutoReconnect = true
	client.InitialAutoReconnect = false
	client.DisableLoginAutoReconnect = true
	client.BackgroundEventCtx = cfg.Lifetime
	// Disable unneeded buffering: no automatic history blob transfer, no decrypted app-state event
	// buffering and no cross-restart retry message store.
	client.ManualHistorySyncDownload = true
	s.mediaClient = newAppStateHTTPClient()
	client.SetMediaHTTPClient(s.mediaClient)
	if cfg.PreLoginHTTP != nil {
		client.SetPreLoginHTTPClient(cfg.PreLoginHTTP)
	}
	if cfg.WebsocketHTTP != nil {
		client.SetWebsocketHTTPClient(cfg.WebsocketHTTP)
	}
	client.GetClientPayload = s.handshakePayload
	s.client = client
	s.handlerID = client.AddEventHandler(s.handleEvent)
	return s, nil
}

func (s *Session) Epoch() uint64         { return s.epoch }
func (s *Session) HasIdentity() bool     { return s.hasID }
func (s *Session) RecoveryPending() bool { return s.pendingRep }
func (s *Session) StorageFaulted() <-chan struct{} {
	if s.barrier == nil {
		return nil
	}
	return s.barrier.StorageFaulted()
}

// Connect performs one connection attempt under the caller's watchdog context.
func (s *Session) Connect(ctx context.Context) error {
	s.mu.Lock()
	retired := s.retired
	s.mu.Unlock()
	if retired {
		return ErrSessionRetired
	}
	return s.client.ConnectContext(ctx)
}

// Disconnect stops the socket after an attempt was cancelled.
func (s *Session) Disconnect() {
	s.client.Disconnect()
}

// Retire rejects new guarded access, stops the socket and waits for the generation to drain.
func (s *Session) Retire(ctx context.Context) error {
	s.mu.Lock()
	if s.retired {
		s.mu.Unlock()
		return nil
	}
	s.retired = true
	s.mu.Unlock()
	s.client.RemoveEventHandler(s.handlerID)
	s.client.Disconnect()
	return s.barrier.Retire(ctx)
}

// GetQRChannel acquires the upstream pairing channel; it must be called before Connect.
func (s *Session) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return s.client.GetQRChannel(ctx)
}

// handshakePayload clones a fresh default device payload and freezes one atomic candidate version
// into it: the user-agent app version and, for a registration handshake, the matching build hash.
// Library globals are never mutated.
func (s *Session) handshakePayload() *waWa6.ClientPayload {
	v := wastore.WAVersionContainer(s.cfg.Versions.Current())
	payload := s.dev.GetClientPayload()
	payload.UserAgent.AppVersion = v.ProtoAppVersion()
	if payload.DevicePairingData != nil {
		hash := v.Hash()
		payload.DevicePairingData.BuildHash = hash[:]
	}
	return payload
}

// handleEvent maps pinned upstream events to the owner's admitted dispatch.
func (s *Session) handleEvent(raw any) {
	switch raw.(type) {
	case *events.Connected:
		s.cfg.Dispatch(s.epoch, SessionConnected)
	case *events.Disconnected:
		s.cfg.Dispatch(s.epoch, SessionDisconnected)
	case *events.PairSuccess:
		s.cfg.Dispatch(s.epoch, SessionPairSuccess)
	case *events.PairError:
		s.cfg.Dispatch(s.epoch, SessionPairError)
	case *events.LoggedOut:
		s.cfg.Dispatch(s.epoch, SessionLoggedOut)
	case *events.ClientOutdated:
		s.cfg.Dispatch(s.epoch, SessionClientOutdated)
	case *events.ManualLoginReconnect:
		s.cfg.Dispatch(s.epoch, SessionManualLoginReconnect)
	case *events.StreamReplaced:
		s.cfg.Dispatch(s.epoch, SessionDisconnected)
	}
}
