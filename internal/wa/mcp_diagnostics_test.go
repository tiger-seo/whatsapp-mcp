package wa

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

type mcpDiagnosticBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func TestMCPActualMessageDebugRedaction(t *testing.T) {
	b := &mcpDiagnosticBuffer{}
	d := mcp.NewDiagnostics(b, slog.LevelDebug)
	defer d.Finish()
	in := &ingest{log: slog.New(d.Handler())}
	in.logReceived(store.Message{ChatJID: "synthetic-secret-chat", ID: "synthetic-secret-id", Text: "synthetic-secret-body", Caption: "synthetic-secret-caption"})
	waitMCPText(t, b, "event=message_received")
	if got := b.String(); strings.Contains(got, "synthetic-secret") || !strings.Contains(got, "level=DEBUG") {
		t.Fatalf("unsafe or missing debug diagnostic: %s", got)
	}
}

func (b *mcpDiagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *mcpDiagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitMCPDiagnostic(t *testing.T, b *mcpDiagnosticBuffer, event string) {
	waitMCPText(t, b, "event="+event)
}

func waitMCPText(t *testing.T, b *mcpDiagnosticBuffer, text string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !strings.Contains(b.String(), text) {
		select {
		case <-deadline:
			t.Fatalf("missing %s: %s", text, b.String())
		case <-time.After(time.Millisecond):
		}
	}
}

func TestMCPActualHistoryIncompleteAtLogLevels(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		t.Run(level.String(), func(t *testing.T) {
			b := &mcpDiagnosticBuffer{}
			d := mcp.NewDiagnostics(b, level)
			t.Cleanup(func() { d.Finish() })
			logger := slog.New(d.Handler())
			in := &ingest{log: logger}
			logger.Info("ingest stopped")
			logger.Debug("runtime sample")
			in.reportHistoryIncomplete("queue_full", &waE2E.HistorySyncNotification{})
			waitMCPDiagnostic(t, b, "history_incomplete")
			if level <= slog.LevelInfo {
				waitMCPText(t, b, "ingest stopped")
			}
			if level <= slog.LevelDebug {
				waitMCPText(t, b, "runtime sample")
			}
			got := b.String()
			if strings.Contains(got, "ingest stopped") != (level <= slog.LevelInfo) || strings.Contains(got, "runtime sample") != (level <= slog.LevelDebug) {
				t.Fatalf("ordinary filtering changed: %s", got)
			}
		})
	}
}

func TestMCPActualReceiveOverloadAtLogLevels(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		t.Run(level.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			b := &mcpDiagnosticBuffer{}
			d := mcp.NewDiagnostics(b, level)
			t.Cleanup(func() { d.Finish() })
			logger := slog.New(d.Handler())
			logger.Info("identity reconciliation")
			logger.Debug("runtime sample")
			block := &blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
			r, sessions := newRecordedRig(t, func(cfg *OwnerConfig) {
				cfg.Log = logger
				inner := cfg.Sessions
				cfg.Sessions = func(sc SessionConfig) (SessionHandle, error) {
					sc.wrapIngestStore = func(s ingestStore) ingestStore { block.ingestStore = s; return block }
					return inner(sc)
				}
			})
			rig := &ingestSimRig{ownerRig: r, sessions: sessions}
			rig.conn = r.pairAndConnect(ctx)
			rig.nodes = recordNodes(ctx, rig.conn)
			var err error
			rig.peer, err = wasim.NewPeer(peerJID())
			if err != nil {
				t.Fatal(err)
			}
			rig.keys = wasim.DeviceKeysOf(rig.session().dev)
			released := false
			defer func() {
				if !released {
					close(block.release)
				}
			}()
			rig.sendMessage(ctx, "MCP-BLOCK", &waE2E.Message{Conversation: proto.String("synthetic")})
			select {
			case <-block.entered:
			case <-ctx.Done():
				t.Fatal("store was not entered")
			}
			for i := 0; i < 2100; i++ {
				if err := rig.conn.Send(ctx, waBinary.Node{Tag: "receipt", Attrs: waBinary.Attrs{"from": peerJID(), "id": fmt.Sprintf("MCP-FILL-%d", i), "t": fmt.Sprint(time.Now().Unix())}}); err != nil {
					break
				}
			}
			select {
			case <-rig.conn.Done():
			case <-ctx.Done():
				t.Fatal("receive queue did not overflow")
			}
			waitMCPDiagnostic(t, b, "receive_overload")
			if level <= slog.LevelInfo {
				waitMCPText(t, b, "identity reconciliation")
			}
			if level <= slog.LevelDebug {
				waitMCPText(t, b, "runtime sample")
			}
			got := b.String()
			if strings.Contains(got, "identity reconciliation") != (level <= slog.LevelInfo) || strings.Contains(got, "runtime sample") != (level <= slog.LevelDebug) {
				t.Fatalf("ordinary filtering changed: %s", got)
			}
			close(block.release)
			released = true
			if err := rig.stop(ctx); err != nil {
				t.Fatalf("stop: %v", err)
			}
		})
	}
}
