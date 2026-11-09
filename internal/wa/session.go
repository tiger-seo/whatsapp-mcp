package wa

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/walog"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
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
	pendingRep      atomic.Bool
	repair          *repairController
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

	// fence is this generation's network block; closing it is retirement step 7.
	fence *fence
	// droppedEvents counts owner-relevant events the handler dropped after the retire bit; lateCalls counts the
	// program's own calls to the session after it (the retirement sequence's own Disconnect excepted).
	droppedEvents atomic.Uint64
	lateCalls     atomic.Uint64
	retireStarted time.Time // guarded by mu
	handlerOnce   sync.Once
	// observer builds the quiet-stop observation; tests replace it, production uses the goroutine dump.
	observer func() quietObserver

	mu sync.Mutex
}

// errAttemptAborted is the cause of an attempt context cancelled through the owner's recorded abort. A Connect
// that finds the session retired with this cause never dialled and is not a late call.
var errAttemptAborted = errors.New("connection attempt aborted before it began")

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
		buildPayload:    dev.GetClientPayload,
		decodeProps:     proto.Unmarshal,
		encodeProps:     proto.Marshal,
		diagnosticWake:  make(chan struct{}, 1),
		diagnosticStop:  make(chan struct{}),
		diagnosticDone:  make(chan struct{}),
		retireDone:      make(chan struct{}),
		callbackDrained: make(chan struct{}, 1),
		fence:           newFence(),
		observer:        productionObserver,
	}
	s.pendingRep.Store(len(pending) > 0)
	client := whatsmeow.NewClient(dev, walog.New(cfg.Log))
	client.QRClientType = whatsmeow.PairClientChrome
	// Every automatic reconnect is denied: work detached by an earlier connection cannot be
	// excluded, so the library may not start a Connect of its own. The login-time flag only
	// routes the post-pair 515 to the owner, which answers it with a fresh client.
	client.EnableAutoReconnect = false
	client.InitialAutoReconnect = false
	client.DisableLoginAutoReconnect = true
	client.BackgroundEventCtx = cfg.Lifetime
	if cfg.Library != nil {
		client.BackgroundEventCtx = cfg.Library
	}
	// Disable unneeded buffering: no automatic history blob transfer, no decrypted app-state event
	// buffering and no cross-restart retry message store.
	client.ManualHistorySyncDownload = true
	// All three HTTP clients dial through this generation's fence, so none can reach the network after it closes.
	s.mediaClient = newBoundedHTTPClient(10*time.Second, 16<<20, s.fence.RoundTripper(nil))
	client.SetMediaHTTPClient(s.mediaClient)
	client.SetPreLoginHTTPClient(s.fence.HTTPClient(cfg.PreLoginHTTP))
	client.SetWebsocketHTTPClient(s.fence.HTTPClient(cfg.WebsocketHTTP))
	client.GetClientPayload = s.handshakePayload
	s.client = client
	s.repair = newRepairController(barrier, s.fetchAppState, cfg.RepairSlot, cfg.Log)
	s.handlerID = client.AddEventHandler(s.handleEvent)
	go s.consumeDiagnostics()
	return s, nil
}

func (s *Session) Epoch() uint64         { return s.epoch }
func (s *Session) HasIdentity() bool     { return s.hasID }
func (s *Session) RecoveryPending() bool { return s.pendingRep.Load() }
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
		if !errors.Is(context.Cause(ctx), errAttemptAborted) {
			s.lateCalls.Add(1)
		}
		return ErrSessionRetired
	}
	return s.client.ConnectContext(ctx)
}

// Disconnect stops the socket after an attempt was cancelled. After the retire bit it is a late call and is
// counted; the retirement sequence uses its own uncounted disconnect.
func (s *Session) Disconnect() {
	if s.isRetired() {
		s.lateCalls.Add(1)
	}
	s.client.Disconnect()
}

func (s *Session) isRetired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retired
}

// MarkRetiring starts the store's refusal count without retiring anything. The owner calls it before it
// cancels any of the generation's own work, so a guarded write that fails because of that is counted.
func (s *Session) MarkRetiring() { s.barrier.MarkRetiring() }

// BeginRetire sets the retire bit and starts the store's refusal count. From then on events the handler would
// have forwarded are dropped and counted, and the program's own calls to the session are refused and counted.
// It is idempotent and does not stop anything.
func (s *Session) BeginRetire() {
	s.mu.Lock()
	if s.retireStarted.IsZero() {
		s.retireStarted = time.Now()
	}
	s.retired = true
	s.mu.Unlock()
	s.callbackState.Or(1 << 63)
	s.barrier.MarkRetiring()
}

// Retire rejects new guarded access, stops the socket and waits for the generation to drain: the library's own
// Disconnect, then the network fence, then the store barrier. The event handler stays registered until
// QuietStop removes it.
func (s *Session) Retire(ctx context.Context) error {
	s.BeginRetire()
	s.retireOnce.Do(func() { go s.finishRetire() })
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
	s.client.Disconnect()
	s.step("disconnect")
	s.fence.Close()
	s.step("fence_closed")
	if err := s.barrier.Retire(context.Background()); err != nil {
		s.retireErr = err
		return
	}
	s.step("barrier_retired")
	for s.callbackState.Load() != 1<<63 {
		<-s.callbackDrained
	}
	close(s.diagnosticStop)
	<-s.diagnosticDone
}

// GetQRChannel acquires the upstream pairing channel; it must be called before Connect.
func (s *Session) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	if s.isRetired() {
		s.lateCalls.Add(1)
		return nil, ErrSessionRetired
	}
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
			s.countDropped(raw)
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

// countDropped counts an event that would have reached the owner and was dropped after the retire bit. A
// disconnect is expected after retirement (the server closes after the 515, and the library's own Disconnect
// emits none); receipts, presence and the like carry no owner state. StreamReplaced is not counted because the
// session maps it to a disconnect: it is the server ending this connection, not work started by this process.
func (s *Session) countDropped(raw any) {
	switch raw.(type) {
	case *events.Connected, *events.PairSuccess, *events.PairError, *events.LoggedOut,
		*events.ClientOutdated, *events.ManualLoginReconnect:
		s.droppedEvents.Add(1)
	}
}

// QuietStop decides whether this retired generation stopped quietly. It runs after Retire returned nil. ctx
// carries the stop bound as its deadline; deadline is the approved observation time, capped at what is left of
// it. A generation that never attempted Connect returns not_checked and passed. The event handler is removed
// when the check ends.
func (s *Session) QuietStop(ctx context.Context, deadline time.Duration) QuietStopResult {
	defer s.removeHandler()
	s.mu.Lock()
	attempted, began := s.accountWork, s.retireStarted
	s.mu.Unlock()
	if !attempted {
		return QuietStopResult{Passed: true, Reason: QuietNotChecked}
	}
	if until, ok := ctx.Deadline(); ok {
		deadline = min(deadline, time.Until(until))
	}
	deadline = max(deadline, 0)
	s.step("observe")
	count, reason := s.observer().observeQuiet(ctx, deadline)
	if began.IsZero() {
		began = time.Now()
	}
	return decideQuiet(quietInputs{
		Observation:   reason,
		Goroutines:    count,
		Network:       s.fence.RefusedDials(),
		Storage:       s.barrier.Refused(),
		Calls:         s.lateCalls.Load(),
		Events:        s.droppedEvents.Load(),
		BoundExceeded: errors.Is(ctx.Err(), context.DeadlineExceeded),
		Duration:      time.Since(began),
	})
}

func (s *Session) removeHandler() {
	s.handlerOnce.Do(func() {
		s.client.RemoveEventHandler(s.handlerID)
		s.step("handler_removed")
	})
}

func (s *Session) step(name string) {
	if s.cfg.trace != nil {
		s.cfg.trace(name)
	}
}

// fetchAppState is the one real app-state call the repair controller makes.
func (s *Session) fetchAppState(ctx context.Context, name string, full bool) error {
	if s.isRetired() {
		s.lateCalls.Add(1)
		return ErrSessionRetired
	}
	return s.client.FetchAppState(ctx, appstate.WAPatchName(name), full, false)
}

// RunRepairs recovers the pending collections of this generation and refreshes the pending flag.
// The owner calls it once after a confirmed connection.
func (s *Session) RunRepairs(ctx context.Context) error {
	if s.isRetired() {
		s.lateCalls.Add(1)
		return ErrSessionRetired
	}
	err := s.repair.run(ctx)
	if remaining, perr := s.barrier.PendingRepairs(ctx); perr == nil {
		s.pendingRep.Store(len(remaining) > 0)
	}
	return err
}
