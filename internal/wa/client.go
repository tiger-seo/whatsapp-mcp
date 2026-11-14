// Package wa defines the connection-owner and service boundaries.
package wa

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

type Sender interface {
	SendMessage(context.Context, types.JID, *waE2E.Message, ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
}

type Uploader interface {
	Upload(context.Context, []byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
}

type Downloader interface {
	DownloadMedia(context.Context, whatsmeow.MediaType, store.MediaMeta) ([]byte, error)
}

type ContactLookup interface {
	GetUserInfo(context.Context, []types.JID) (map[types.JID]types.UserInfo, error)
}

type GroupLookup interface {
	GetGroupInfo(context.Context, types.JID) (*types.GroupInfo, error)
}

type Status struct {
	Connected       bool
	LoggedIn        bool
	PairingRequired bool
	WAVersion       string
}

type PairingResult string

const (
	PairingActive    PairingResult = "active"
	PairingSucceeded PairingResult = "succeeded"
	PairingTimedOut  PairingResult = "timed_out"
	PairingCancelled PairingResult = "cancelled"
	PairingFailed    PairingResult = "failed"
)

type PairingState struct {
	Code      string
	ExpiresAt time.Time
	Result    PairingResult
}

type Phase string

const (
	PhaseIdle          Phase = "idle"
	PhaseConnecting    Phase = "connecting"
	PhasePairing       Phase = "pairing"
	PhaseConnected     Phase = "connected"
	PhaseDisconnected  Phase = "disconnected"
	PhaseLoggedOut     Phase = "logged_out"
	PhaseRetiring      Phase = "retiring"
	PhaseStorageFailed Phase = "storage_failed"
	PhaseStopping      Phase = "stopping"
	PhaseStopped       Phase = "stopped"
)

type Health struct {
	Ready           bool
	RecoveryPending bool
	StorageFault    bool
	// QuietReason is the category of the first failed quiet-stop check of this run; empty when none failed.
	QuietReason string
	// QuietPassed records that at least one generation's quiet-stop check passed and none failed.
	QuietPassed bool
	// IngestFailed records that message storage failed; the command must stop and the operator restart.
	IngestFailed bool
	// ReceiveOverload is the library's reason for the last connection it closed at a receive limit; empty when
	// none. Messages may be missing.
	ReceiveOverload string
}

type Event struct {
	Epoch uint64
	Phase Phase
}

type Owner interface {
	Start(context.Context) error
	BeginStop()
	Done() <-chan struct{}
	// StopError is final after Done closes and reports an unsuccessful shutdown.
	StopError() error
	Snapshot() Status
	RequestPairing(context.Context) (PairingState, error)
	// PairingSnapshot returns the current pairing window's copied state; ok is false when no window is
	// active. Callers follow updates through Subscribe rather than reading any upstream channel.
	PairingSnapshot() (PairingState, bool)
	CancelPairing()
	Subscribe(func(Event)) func()
	Health() Health
	// Terminal closes when the running command must stop because message storage failed.
	Terminal() <-chan struct{}
}

// SessionEvent is one admitted upstream event the adapter forwards to the owner.
type SessionEvent int

const (
	SessionConnected SessionEvent = iota
	SessionDisconnected
	SessionPairSuccess
	SessionPairError
	SessionLoggedOut
	SessionClientOutdated
	SessionManualLoginReconnect
	// SessionReceiveOverload precedes the SessionDisconnected of a connection the library closed at a receive
	// limit.
	SessionReceiveOverload
)

// SessionHandle is the owner's narrow view of one client generation. It is created through a
// SessionFactory, so tests can substitute the pinned adapter without a network.
type SessionHandle interface {
	// Epoch returns the owner epoch this generation is bound to.
	Epoch() uint64
	// Connect performs one connection attempt under the caller's watchdog context.
	Connect(context.Context) error
	// Disconnect stops the socket after an attempt was cancelled.
	Disconnect()
	// HasIdentity reports whether the generation carries a locally saved identity.
	HasIdentity() bool
	// Retire rejects new guarded access and waits for the generation to drain.
	Retire(context.Context) error
	// RecoveryPending reports pending durable repair markers observed at open.
	RecoveryPending() bool
	// StorageFaulted closes when a guarded device write requires an owner restart.
	StorageFaulted() <-chan struct{}
}

// RepairRunner is implemented by sessions that can recover pending app-state collections after a
// confirmed connection. It returns when the single pass is over; it never retries.
type RepairRunner interface {
	RunRepairs(context.Context) error
}

// QRChannelSource is implemented by sessions that can open an upstream pairing window. The channel
// must be acquired before Connect.
type QRChannelSource interface {
	GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error)
}

// SessionConfig is the construction input of one adapter generation.
type SessionConfig struct {
	// Store is the shared store; only LoadDevice is used.
	Store *store.Store
	// Log is the process logger; the shared bridge carries upstream output.
	Log *slog.Logger
	// Versions is the selected-version source for the immutable handshake callback.
	Versions *VersionProvider
	// Epoch is the owner epoch this generation belongs to.
	Epoch uint64
	// Lifetime is the generation's work context: the program's own work (device load, repair pass, QR channel).
	// The owner cancels it on a stop signal and at the start of retirement.
	Lifetime context.Context
	// Library is the context the client library gives its detached handlers (BackgroundEventCtx) and the
	// parent of every Connect attempt. Nothing cancels it before the generation's network and storage are
	// blocked; nil falls back to Lifetime (tests that do not separate the two).
	Library context.Context
	// trace receives the name of each retirement step as it happens; tests only.
	trace func(step string)
	// wrapIngestStore, when set, wraps the store ingest writes to; tests only.
	wrapIngestStore func(ingestStore) ingestStore
	// Dispatch receives this generation's admitted upstream events.
	Dispatch func(epoch uint64, event SessionEvent)
	// Test-only transport injections; nil uses the approved defaults.
	PreLoginHTTP  *http.Client
	WebsocketHTTP *http.Client
	// MediaHTTP, when set, supplies the transport of the media and history download client; tests only.
	MediaHTTP *http.Client
	// ReconcileFrom is the cursor of an unfinished startup LID reconciliation, for the first generation only.
	ReconcileFrom *LIDCursor
	// RepairSlot is the owner-wide slot for owner-invoked app-state calls; nil gives the session its own.
	RepairSlot chan struct{}
}

// SessionFactory creates one adapter generation; the owner uses exactly one at a time.
type SessionFactory func(SessionConfig) (SessionHandle, error)
