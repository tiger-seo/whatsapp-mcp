package wa

import (
	"context"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow"
)

// Compile against the exact pinned client signatures without constructing a client.
var (
	_ Sender        = (*whatsmeow.Client)(nil)
	_ Uploader      = (*whatsmeow.Client)(nil)
	_ ContactLookup = (*whatsmeow.Client)(nil)
	_ GroupLookup   = (*whatsmeow.Client)(nil)
	_ Downloader    = downloadStub{}
	_ Owner         = ownerStub{}
)

type downloadStub struct{}

func (downloadStub) DownloadMedia(_ context.Context, _ whatsmeow.MediaType, m store.MediaMeta) ([]byte, error) {
	if m.FileLength == nil {
		return nil, nil
	}
	return []byte{byte(*m.FileLength)}, nil
}

type ownerStub struct{}

func (ownerStub) Start(context.Context) error                          { return nil }
func (ownerStub) BeginStop()                                           {}
func (ownerStub) Done() <-chan struct{}                                { return nil }
func (ownerStub) Snapshot() Status                                     { return Status{} }
func (ownerStub) RequestPairing(context.Context) (PairingState, error) { return PairingState{}, nil }
func (ownerStub) CancelPairing()                                       {}
func (ownerStub) Subscribe(func(Event)) func()                         { return func() {} }
func (ownerStub) Health() Health                                       { return Health{} }

func TestContractValues(t *testing.T) {
	length := int64(7)
	got, err := (downloadStub{}).DownloadMedia(context.Background(), whatsmeow.MediaImage, store.MediaMeta{FileLength: &length})
	if err != nil || len(got) != 1 || got[0] != 7 {
		t.Fatalf("download with persisted length = %v, %v", got, err)
	}
	got, err = (downloadStub{}).DownloadMedia(context.Background(), whatsmeow.MediaImage, store.MediaMeta{})
	if err != nil || got != nil {
		t.Fatalf("download with absent length = %v, %v", got, err)
	}
	status := Status{Connected: true, WAVersion: "2.3000.0"}
	copy := status
	copy.Connected = false
	if !status.Connected {
		t.Fatal("status value was not copied")
	}
	pair := PairingState{Code: "synthetic", ExpiresAt: time.Unix(1, 0)}
	other := pair
	other.Code = "replacement"
	if pair.Code != "synthetic" {
		t.Fatal("pairing state value was not copied")
	}
}
