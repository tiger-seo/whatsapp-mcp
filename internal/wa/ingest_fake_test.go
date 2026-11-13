package wa

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// fakeIngestClient is the ingest component's client in unit tests. Each call is counted; a nil function gives a
// harmless default. It never reaches a network and has no send, pairing or connect method at all.
type fakeIngestClient struct {
	own types.JID

	parse    func(types.JID, *waWeb.WebMessageInfo) (*events.Message, error)
	download func(context.Context, *waE2E.HistorySyncNotification, bool) (*waHistorySync.HistorySync, error)
	group    func(context.Context, types.JID) (*types.GroupInfo, error)

	mu        sync.Mutex
	downloads []bool // the synchronousStorage argument of each call
	groups    []types.JID
	parses    atomic.Int64
}

var errFakeUnset = errors.New("fake: no response configured")

func (f *fakeIngestClient) OwnJID() types.JID { return f.own }

func (f *fakeIngestClient) ParseWebMessage(chat types.JID, web *waWeb.WebMessageInfo) (*events.Message, error) {
	f.parses.Add(1)
	if f.parse != nil {
		return f.parse(chat, web)
	}
	return nil, errFakeUnset
}

func (f *fakeIngestClient) DownloadHistorySync(ctx context.Context, n *waE2E.HistorySyncNotification, sync bool) (*waHistorySync.HistorySync, error) {
	f.mu.Lock()
	f.downloads = append(f.downloads, sync)
	f.mu.Unlock()
	if f.download != nil {
		return f.download(ctx, n, sync)
	}
	return nil, errFakeUnset
}

func (f *fakeIngestClient) GetGroupInfo(ctx context.Context, jid types.JID) (*types.GroupInfo, error) {
	f.mu.Lock()
	f.groups = append(f.groups, jid)
	f.mu.Unlock()
	if f.group != nil {
		return f.group(ctx, jid)
	}
	return nil, errFakeUnset
}

func (f *fakeIngestClient) groupCalls() []types.JID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]types.JID(nil), f.groups...)
}

const (
	testPeer  = "491511234567"
	testGroup = "120363000000000001"
)

func ownJID() types.JID   { return types.NewJID(testOwnPhone, types.DefaultUserServer) }
func peerJID() types.JID  { return types.NewJID(testPeer, types.DefaultUserServer) }
func groupJID() types.JID { return types.NewJID(testGroup, types.GroupServer) }

// libMarker marks the rig's library context, so a fake can check which context a library call received.
type libMarker struct{}

func isLibContext(ctx context.Context) bool { v, _ := ctx.Value(libMarker{}).(bool); return v }

// ingestRig is one ingest component on a temporary store with the fake client.
type ingestRig struct {
	in     *ingest
	st     *store.Store
	client *fakeIngestClient
	logs   *recordedLogs
	failed atomic.Int64
	live   []store.Message
	liveMu sync.Mutex
}

func newIngestRig(t *testing.T, opts ...func(*IngestConfig)) *ingestRig {
	t.Helper()
	r := &ingestRig{st: openWADir(t), client: &fakeIngestClient{own: ownJID()}}
	var logger *slog.Logger
	logger, r.logs = newRecordedLogger()
	lib, cancel := context.WithCancel(context.WithValue(context.Background(), libMarker{}, true))
	t.Cleanup(cancel)
	cfg := IngestConfig{
		Store:  r.st,
		Client: r.client,
		Log:    logger,
		Lib:    lib,
		Failed: func() { r.failed.Add(1) },
		Live: func(m store.Message) {
			r.liveMu.Lock()
			r.live = append(r.live, m)
			r.liveMu.Unlock()
		},
	}
	for _, o := range opts {
		o(&cfg)
	}
	r.in = newIngest(cfg)
	t.Cleanup(func() {
		r.in.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), ingestWorkerWait)
		defer cancel()
		_ = r.in.Wait(ctx)
	})
	return r
}

func (r *ingestRig) liveMessages() []store.Message {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	return append([]store.Message(nil), r.live...)
}

// recordedLogs captures structured records for assertions on events and their attributes.
type recordedLogs struct {
	mu   sync.Mutex
	recs []slog.Record
}

func newRecordedLogger() (*slog.Logger, *recordedLogs) {
	l := &recordedLogs{}
	return slog.New(l), l
}

func (l *recordedLogs) Enabled(context.Context, slog.Level) bool { return true }
func (l *recordedLogs) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *recordedLogs) WithGroup(string) slog.Handler            { return l }
func (l *recordedLogs) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	l.recs = append(l.recs, r.Clone())
	l.mu.Unlock()
	return nil
}

// events returns the attributes of every record whose event key is name.
func (l *recordedLogs) events(name string) []map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]string
	for _, r := range l.recs {
		attrs := map[string]string{"msg": r.Message, "level": r.Level.String()}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		if attrs["event"] == name {
			out = append(out, attrs)
		}
	}
	return out
}

// text returns every record rendered as message and attributes, for secrecy checks.
func (l *recordedLogs) text(minLevel slog.Level) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b []byte
	for _, r := range l.recs {
		if r.Level < minLevel {
			continue
		}
		b = append(b, r.Message...)
		r.Attrs(func(a slog.Attr) bool {
			b = append(b, ' ')
			b = append(b, a.Key...)
			b = append(b, '=')
			b = append(b, a.Value.String()...)
			return true
		})
		b = append(b, '\n')
	}
	return string(b)
}

// historyNotificationEvent is a history-sync notification as the library delivers it: a protocol message,
// from the account's own phone when fromMe is set.
func historyNotificationEvent(fromMe bool) *events.Message {
	sender := ownJID()
	if !fromMe {
		sender = peerJID()
	}
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: ownJID(), Sender: sender, IsFromMe: fromMe},
			ID:            "HIST", Timestamp: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		},
		Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Type:                    waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum(),
			HistorySyncNotification: &waE2E.HistorySyncNotification{ChunkOrder: proto.Uint32(1), SyncType: waE2E.HistorySyncType_RECENT.Enum()},
		}},
	}
}
