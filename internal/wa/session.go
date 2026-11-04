package wa

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/walog"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	waWa6 "go.mau.fi/whatsmeow/proto/waWa6"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// Session is one adapter generation around the pinned client: guarded device load, reviewed
// connection options and the immutable handshake callback. The owner drives attempts through the
// narrow handle; the raw client is never handed to consumers.
type Session struct {
	cfg             SessionConfig
	dev             *wastore.Device
	barrier         *store.DeviceBarrier
	client          *whatsmeow.Client
	mediaClient     *http.Client
	epoch           uint64
	hasID           bool
	pendingRep      bool
	handlerID       uint32
	retired         bool
	buildPayload    func() *waWa6.ClientPayload
	decodeProps     func([]byte, proto.Message) error
	encodeProps     func(proto.Message) ([]byte, error)
	diagnosticBits  atomic.Uint32
	diagnosticWake  chan struct{}
	diagnosticStop  chan struct{}
	diagnosticDone  chan struct{}
	retireOnce      sync.Once
	retireDone      chan struct{}
	retireErr       error
	callbackState   atomic.Uint64
	callbackDrained chan struct{}
	accountWork     bool // guarded by mu

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
		cfg:             cfg,
		dev:             dev,
		barrier:         barrier,
		epoch:           cfg.Epoch,
		hasID:           dev.ID != nil,
		pendingRep:      len(pending) > 0,
		buildPayload:    dev.GetClientPayload,
		decodeProps:     proto.Unmarshal,
		encodeProps:     proto.Marshal,
		diagnosticWake:  make(chan struct{}, 1),
		diagnosticStop:  make(chan struct{}),
		diagnosticDone:  make(chan struct{}),
		retireDone:      make(chan struct{}),
		callbackDrained: make(chan struct{}, 1),
	}
	client := whatsmeow.NewClient(dev, walog.New(cfg.Log))
	client.QRClientType = whatsmeow.PairClientChrome
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
	go s.consumeDiagnostics()
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
	if !retired {
		s.accountWork = true
	}
	s.mu.Unlock()
	if retired {
		return ErrSessionRetired
	}
	return s.client.ConnectContext(ctx)
}

// UpstreamDrainUnproven reports whether the pinned client could have created
// detached node handlers that Disconnect and the device barrier do not join.
func (s *Session) UpstreamDrainUnproven() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accountWork
}

// Disconnect stops the socket after an attempt was cancelled.
func (s *Session) Disconnect() {
	s.client.Disconnect()
}

// Retire rejects new guarded access, stops the socket and waits for the generation to drain.
func (s *Session) Retire(ctx context.Context) error {
	s.retireOnce.Do(func() {
		s.mu.Lock()
		s.retired = true
		s.mu.Unlock()
		s.callbackState.Or(1 << 63)
		go s.finishRetire()
	})
	select {
	case <-s.retireDone:
		return s.retireErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FinishRevocation checks deferred deletion and completes a confirmed logout
// through the store's normal durable admission after all callbacks drain.
func (s *Session) FinishRevocation(ctx context.Context, confirmed bool) error {
	select {
	case <-s.retireDone:
		if s.retireErr != nil {
			return s.retireErr
		}
		return s.barrier.FinishRevocation(ctx, confirmed)
	default:
		return store.ErrNotQuiescent
	}
}

func (s *Session) finishRetire() {
	defer close(s.retireDone)
	s.client.RemoveEventHandler(s.handlerID)
	s.client.Disconnect()
	if err := s.barrier.Retire(context.Background()); err != nil {
		s.retireErr = err
		return
	}
	for s.callbackState.Load() != 1<<63 {
		<-s.callbackDrained
	}
	close(s.diagnosticStop)
	<-s.diagnosticDone
}

// GetQRChannel acquires the upstream pairing channel; it must be called before Connect.
func (s *Session) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return s.client.GetQRChannel(ctx)
}

// handshakePayload clones a fresh default device payload and freezes one atomic candidate version
// into it: the user-agent app version and, for a registration handshake, the matching build hash.
// Library globals are never mutated.
func (s *Session) handshakePayload() *waWa6.ClientPayload {
	for {
		state := s.callbackState.Load()
		if state&(1<<63) != 0 {
			return nil
		}
		if s.callbackState.CompareAndSwap(state, state+1) {
			break
		}
	}
	defer func() {
		if s.callbackState.Add(^uint64(0)) == 1<<63 {
			select {
			case s.callbackDrained <- struct{}{}:
			default:
			}
		}
	}()
	v := wastore.WAVersionContainer(s.cfg.Versions.Current())
	payload := s.buildPayload()
	if payload.UserAgent.AppVersion == nil {
		payload.UserAgent.AppVersion = &waWa6.ClientPayload_UserAgent_AppVersion{}
	}
	app := payload.UserAgent.AppVersion
	app.Primary, app.Secondary, app.Tertiary = proto.Uint32(v[0]), proto.Uint32(v[1]), proto.Uint32(v[2])
	version := v.String()
	payload.UserAgent.OsVersion, payload.UserAgent.OsBuildNumber = proto.String(version), proto.String(version)
	if payload.DevicePairingData != nil {
		props := &waCompanionReg.DeviceProps{}
		if err := s.decodeProps(payload.DevicePairingData.DeviceProps, props); err != nil {
			s.notifyDiagnostic(1)
			return nil
		}
		props.Os = proto.String("Linux")
		props.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
		if props.Version == nil {
			props.Version = &waCompanionReg.DeviceProps_AppVersion{}
		}
		props.Version.Primary, props.Version.Secondary, props.Version.Tertiary = proto.Uint32(v[0]), proto.Uint32(v[1]), proto.Uint32(v[2])
		encoded, err := s.encodeProps(props)
		if err != nil {
			s.notifyDiagnostic(2)
			return nil
		}
		payload.DevicePairingData.DeviceProps = encoded
		hash := v.Hash()
		payload.DevicePairingData.BuildHash = hash[:]
	}
	return payload
}

func (s *Session) notifyDiagnostic(bit uint32) {
	s.diagnosticBits.Or(bit)
	select {
	case s.diagnosticWake <- struct{}{}:
	default:
	}
}

func (s *Session) consumeDiagnostics() {
	defer close(s.diagnosticDone)
	log := s.cfg.Log
	if log == nil {
		log = slog.Default()
	}
	drain := func() {
		bits := s.diagnosticBits.Swap(0)
		if bits&1 != 0 {
			log.Warn("registration properties decode failed", "event", "registration_decode_failed")
		}
		if bits&2 != 0 {
			log.Warn("registration properties encode failed", "event", "registration_encode_failed")
		}
	}
	for {
		select {
		case <-s.diagnosticWake:
			drain()
		case <-s.diagnosticStop:
			drain()
			return
		}
	}
}

// handleEvent maps pinned upstream events to the owner's admitted dispatch.
func (s *Session) handleEvent(raw any) {
	for {
		state := s.callbackState.Load()
		if state&(1<<63) != 0 {
			return
		}
		if s.callbackState.CompareAndSwap(state, state+1) {
			break
		}
	}
	defer func() {
		if s.callbackState.Add(^uint64(0)) == 1<<63 {
			select {
			case s.callbackDrained <- struct{}{}:
			default:
			}
		}
	}()
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
