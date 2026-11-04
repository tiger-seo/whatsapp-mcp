// Package wa defines the connection-owner and service boundaries.
package wa

import (
	"context"
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
}

type Event struct {
	Epoch uint64
	Phase Phase
}

type Owner interface {
	Start(context.Context) error
	BeginStop()
	Done() <-chan struct{}
	Snapshot() Status
	RequestPairing(context.Context) (PairingState, error)
	CancelPairing()
	Subscribe(func(Event)) func()
	Health() Health
}
