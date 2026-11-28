package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/media"
	"github.com/tiger-seo/whatsapp-mcp/internal/media/sendapi"
	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
	"go.mau.fi/whatsmeow/proto/waAdv"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

const (
	sendOwnPhone = "490000000001"
	sendPeer     = "491511000001"
	sendPeerJID  = sendPeer + "@s.whatsapp.net"
	sendPeerLID  = "100000000001@lid"
	sendGroupJID = "120363000000000001@g.us"
)

// storingPort is the fake send port with the local record written to the real store, as the owner writes it.
type storingPort struct {
	*wa.FakeSendPort
	st *store.Store
}

func (p storingPort) Record(ctx context.Context, sent wa.SentMessage) error {
	if err := p.FakeSendPort.Record(ctx, sent); err != nil {
		return err
	}
	m := store.Message{ChatJID: sent.To.String(), ID: sent.ID, SenderJID: sendOwnPhone + "@s.whatsapp.net", Time: sent.Time, FromMe: true,
		Text: sent.Message.GetConversation()}
	switch x := sent.Message; {
	case x.ImageMessage != nil:
		m.MediaType, m.Caption = "image", x.ImageMessage.GetCaption()
	case x.VideoMessage != nil:
		m.MediaType, m.Caption = "video", x.VideoMessage.GetCaption()
	case x.AudioMessage != nil:
		m.MediaType = "audio"
	case x.DocumentMessage != nil:
		m.MediaType, m.Caption = "document", x.DocumentMessage.GetCaption()
	}
	return p.st.StoreMessage(ctx, m)
}

type sendOptions struct {
	recipients []string
	clock      sendapi.Clock
	ffmpeg     string
	service    func(SendService) SendService
	history    func(media.History) media.History
}

// sendRig is the send tools on the real send service and store, with the fake send port.
type sendRig struct {
	t            *testing.T
	st           *store.Store
	port         *wa.FakeSendPort
	limits       *media.Limits
	server       *Server
	diag         *diagnosticWriter
	data, outbox string
}

func newSendRig(t *testing.T, adjust ...func(*sendOptions)) *sendRig {
	t.Helper()
	options := sendOptions{recipients: []string{"*"}, clock: sendapi.SystemClock{}}
	for _, a := range adjust {
		a(&options)
	}
	ctx := context.Background()
	r := &sendRig{t: t, data: filepath.Join(t.TempDir(), "data")}
	r.outbox = filepath.Join(r.data, "outbox")
	for _, dir := range []string{r.data, r.outbox} {
		if err := private.MkdirAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(ctx, r.data, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.st = st
	t.Cleanup(func() { _ = st.Close(ctx) })
	seedSendStore(t, st)
	resolved, err := filepath.EvalSymlinks(r.outbox)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(resolved)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	r.port = wa.NewFakeSendPort(wa.SendIdentity{Phone: types.NewJID(sendOwnPhone, types.DefaultUserServer)})
	r.limits = media.NewLimits(options.clock)
	var service SendService
	history := media.History(st)
	if options.history != nil {
		history = options.history(history)
	}
	service, err = media.NewService(media.Options{Port: storingPort{r.port, st}, Store: history, Limits: r.limits, Clock: options.clock,
		Recipients: options.recipients, Dirs: []media.AllowedDir{{Path: resolved, Root: root}}, DataDir: r.data, FFmpeg: options.ffmpeg})
	if err != nil {
		t.Fatal(err)
	}
	if options.service != nil {
		service = options.service(service)
	}
	r.diag = &diagnosticWriter{}
	d := NewDiagnostics(r.diag, slog.LevelInfo)
	t.Cleanup(func() { d.Finish() })
	r.server = NewServer(NewSDKErrorLatch(), d)
	r.server.RegisterReadTools(st.ToolReader())
	r.server.RegisterSendTools(service, options.clock)
	return r
}

// seedSendStore saves a synthetic device, a LID mapping and a stored chat with the peer.
func seedSendStore(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	dev, barrier, err := st.LoadDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = barrier.Retire(ctx) })
	saveDevice(t, dev)
	if err := dev.LIDs.PutLIDMapping(ctx, types.NewJID("100000000001", types.HiddenUserServer), types.NewJID(sendPeer, types.DefaultUserServer)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateChat(ctx, store.ChatUpdate{JID: sendPeerJID, LastActivity: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
}

func saveDevice(t *testing.T, dev *wastore.Device) {
	t.Helper()
	own := types.NewJID(sendOwnPhone, types.DefaultUserServer)
	dev.ID = &own
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("synthetic"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := dev.Save(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// client connects an SDK client through the in-memory transport.
func (r *sendRig) client() *sdk.ClientSession {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	r.t.Cleanup(cancel)
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	session, err := r.server.Connect(ctx, serverTransport)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = session.Close() })
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = client.Close() })
	return client
}

// toolReply is a tool result as the client sees it.
type toolReply struct {
	isError    bool
	structured map[string]any
	text       string
}

func (reply toolReply) code() string {
	if body, ok := reply.structured["error"].(map[string]any); ok {
		code, _ := body["code"].(string)
		return code
	}
	return ""
}

func (reply toolReply) errorField(name string) any {
	body, _ := reply.structured["error"].(map[string]any)
	return body[name]
}

// call runs a tool. args is a value to encode, or a json.RawMessage to send as written. A client can read a reply
// just before the guard releases the one tool credit, so a call the guard refuses for that reason is made again.
func call(t *testing.T, client *sdk.ClientSession, tool string, args any) toolReply {
	t.Helper()
	for attempt := 0; ; attempt++ {
		reply := callOnce(t, client, tool, args)
		if reply.errorField("message") != "Server is busy; retry later" || attempt == 100 {
			return reply
		}
		time.Sleep(time.Millisecond)
	}
}

func callOnce(t *testing.T, client *sdk.ClientSession, tool string, args any) toolReply {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := client.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	reply := toolReply{isError: result.IsError}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &reply.structured); err != nil {
		t.Fatalf("%s: structured content %s: %v", tool, encoded, err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("%s: %d content blocks", tool, len(result.Content))
	}
	reply.text = result.Content[0].(*sdk.TextContent).Text
	return reply
}

// stdio is a raw newline-delimited session, which shows every reply and lets a test send any frame.
type stdio struct {
	t       *testing.T
	session *ServerSession
	in      *io.PipeWriter
	replies chan map[string]any
}

func (r *sendRig) stdio() *stdio {
	r.t.Helper()
	inRead, inWrite := io.Pipe()
	outRead, outWrite := io.Pipe()
	session, err := r.server.ConnectStdio(context.Background(), inRead, outWrite)
	if err != nil {
		r.t.Fatal(err)
	}
	s := &stdio{t: r.t, session: session, in: inWrite, replies: make(chan map[string]any, 16)}
	go func() {
		lines := bufio.NewReader(outRead)
		for {
			line, err := lines.ReadBytes('\n')
			if err != nil {
				close(s.replies)
				return
			}
			var reply map[string]any
			if json.Unmarshal(line, &reply) == nil {
				s.replies <- reply
			}
		}
	}()
	var once sync.Once
	r.t.Cleanup(func() {
		once.Do(func() {
			_ = inWrite.Close()
			_ = session.Close()
			_ = outWrite.Close()
		})
	})
	s.write(`{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw","version":"1"}}}`)
	s.reply("init")
	s.write(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	return s
}

// write sends one frame. A frame the server refuses can leave the write unfinished, so it does not block the test.
func (s *stdio) write(frame string) {
	s.t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.WriteString(s.in, frame+"\n")
	}()
	select {
	case <-done:
	case <-s.session.Stopped():
	case <-time.After(10 * time.Second):
		s.t.Fatal("the server stopped reading")
	}
}

func (s *stdio) call(id, tool string, args any) {
	s.t.Helper()
	frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err != nil {
		s.t.Fatal(err)
	}
	s.write(string(frame))
}

func (s *stdio) cancel(id string) {
	s.write(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"` + id + `"}}`)
}

// reply returns the next reply, which must answer id.
func (s *stdio) reply(id string) toolReply {
	s.t.Helper()
	select {
	case m, ok := <-s.replies:
		if !ok {
			s.t.Fatal("the session closed")
		}
		if m["id"] != id {
			s.t.Fatalf("reply %v, want id %q", m, id)
		}
		out := toolReply{}
		if result, ok := m["result"].(map[string]any); ok {
			out.isError, _ = result["isError"].(bool)
			out.structured, _ = result["structuredContent"].(map[string]any)
		} else {
			out.isError, out.structured = true, m
		}
		return out
	case <-time.After(10 * time.Second):
		s.t.Fatalf("no reply to %q", id)
	}
	return toolReply{}
}

// alive checks that the session still answers.
func (s *stdio) alive() {
	s.t.Helper()
	select {
	case <-s.session.Stopped():
		s.t.Fatal("the session closed")
	default:
	}
	s.write(`{"jsonrpc":"2.0","id":"alive","method":"ping"}`)
	s.reply("alive")
}
