// Package wasim is a simulated WhatsApp web server for tests.
//
// It speaks enough of the real protocol for the pinned whatsmeow client to dial it, complete the
// Noise handshake, pair a synthetic device, receive stream errors and reach Connected, so tests can
// drive the production adapter end to end without a network or an account. It simulates the
// server and the primary phone only; it proves behaviour against this model of the protocol, not
// against WhatsApp.
//
// The package is for tests. While a Server runs it replaces the pinned WhatsApp certificate root
// (a library global), so only one Server may be active at a time: Start blocks until the previous
// Server has stopped. Nothing under cmd/ may import it; TestNotLinkedIntoTheBinary enforces that.
package wasim

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/util/keys"
	"google.golang.org/protobuf/proto"
)

// rootMu serializes Servers because the certificate root is a process-wide variable.
var rootMu sync.Mutex

// Server is one simulated WhatsApp endpoint on a loopback listener.
type Server struct {
	t        testing.TB
	listener net.Listener
	http     *http.Server
	static   *keys.KeyPair
	chain    *signingChain
	restore  func()

	accepted chan *Conn

	dials      atomic.Int64
	handshakes atomic.Int64
	refuseWith atomic.Int64

	mu       sync.Mutex
	conns    []*Conn
	failures []error
	closed   bool
}

// Start launches a Server and stops it when the test ends.
func Start(t testing.TB) *Server {
	t.Helper()
	rootMu.Lock()
	s := &Server{t: t, accepted: make(chan *Conn, 64), static: keys.NewKeyPair()}
	unlocked := false
	unlock := func() {
		if !unlocked {
			unlocked = true
			rootMu.Unlock()
		}
	}
	chain, err := newSigningChain(*s.static.Pub)
	if err != nil {
		unlock()
		t.Fatalf("wasim: certificate chain: %v", err)
	}
	s.chain = chain
	previous := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = chain.rootPub
	s.restore = func() {
		whatsmeow.WACertPubKey = previous
		unlock()
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.restore()
		t.Fatalf("wasim: listen: %v", err)
	}
	s.listener = listener
	s.http = &http.Server{Handler: http.HandlerFunc(s.serveWebsocket), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.http.Serve(listener) }()
	t.Cleanup(s.Close)
	return s
}

// Client returns an HTTP client for whatsmeow's SetWebsocketHTTPClient (and the pre-login client):
// whatever host and scheme the library dials, the connection goes to this Server.
func (s *Server) Client() *http.Client {
	addr := s.listener.Addr().String()
	return &http.Client{Transport: &http.Transport{
		// A plain connection returned from the TLS dial hook is used as is, so the library's
		// wss:// URL reaches the plain loopback listener without certificates.
		DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
	}}
}

// Accept waits for the next connection whose handshake completed.
func (s *Server) Accept(ctx context.Context) (*Conn, error) {
	select {
	case c := <-s.accepted:
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Dials counts websocket upgrade attempts, including refused ones. Handshakes counts completed
// Noise handshakes. Tests assert these at the transport boundary instead of trusting that no
// connect was made.
func (s *Server) Dials() int      { return int(s.dials.Load()) }
func (s *Server) Handshakes() int { return int(s.handshakes.Load()) }

// RefuseDials makes the next upgrades fail with the given HTTP status; 0 accepts again.
func (s *Server) RefuseDials(status int) { s.refuseWith.Store(int64(status)) }

// Failures returns handshake errors, which mean the client or the simulator diverged from the
// protocol rather than a deliberate test action.
func (s *Server) Failures() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]error(nil), s.failures...)
}

// Close stops the listener, closes every connection and releases the certificate root.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conns := append([]*Conn(nil), s.conns...)
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.http.Shutdown(ctx)
	_ = s.http.Close()
	s.restore()
}

func (s *Server) fail(err error) {
	s.mu.Lock()
	s.failures = append(s.failures, err)
	s.mu.Unlock()
}

func (s *Server) serveWebsocket(w http.ResponseWriter, r *http.Request) {
	s.dials.Add(1)
	if status := int(s.refuseWith.Load()); status != 0 {
		http.Error(w, "refused by the simulator", status)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	ws.SetReadLimit(socket.FrameMaxSize)
	c := &Conn{
		server: s,
		ws:     ws,
		in:     make(chan *waBinary.Node, 1024),
		done:   make(chan struct{}),
	}
	c.autoAck.Store(true)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ws.CloseNow()
		return
	}
	s.conns = append(s.conns, c)
	s.mu.Unlock()
	if err := c.handshake(r.Context()); err != nil {
		s.fail(err)
		c.Close()
		return
	}
	s.handshakes.Add(1)
	select {
	case s.accepted <- c:
	default:
		s.fail(errors.New("accept queue full"))
	}
	c.readLoop(r.Context())
}

// frames reassembles the 3-byte-length frames that the websocket messages carry.
type frames struct {
	ws       *websocket.Conn
	buf      []byte
	gotIntro bool
}

func (f *frames) next(ctx context.Context) ([]byte, error) {
	for {
		if !f.gotIntro && len(f.buf) >= len(socket.WAConnHeader) {
			if string(f.buf[:len(socket.WAConnHeader)]) != string(socket.WAConnHeader) {
				return nil, fmt.Errorf("unexpected connection header % x", f.buf[:len(socket.WAConnHeader)])
			}
			f.buf = f.buf[len(socket.WAConnHeader):]
			f.gotIntro = true
		}
		if f.gotIntro && len(f.buf) >= socket.FrameLengthSize {
			n := int(f.buf[0])<<16 | int(f.buf[1])<<8 | int(f.buf[2])
			if len(f.buf) >= socket.FrameLengthSize+n {
				frame := append([]byte(nil), f.buf[socket.FrameLengthSize:socket.FrameLengthSize+n]...)
				f.buf = f.buf[socket.FrameLengthSize+n:]
				return frame, nil
			}
		}
		kind, data, err := f.ws.Read(ctx)
		if err != nil {
			return nil, err
		}
		if kind != websocket.MessageBinary {
			return nil, fmt.Errorf("unexpected websocket message type %v", kind)
		}
		f.buf = append(f.buf, data...)
	}
}

func frame(data []byte) []byte {
	out := make([]byte, socket.FrameLengthSize+len(data))
	out[0], out[1], out[2] = byte(len(data)>>16), byte(len(data)>>8), byte(len(data))
	copy(out[socket.FrameLengthSize:], data)
	return out
}

// handshake completes the server side of the Noise XX exchange and records the client payload.
func (c *Conn) handshake(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c.frames = &frames{ws: c.ws}
	raw, err := c.frames.next(ctx)
	if err != nil {
		return fmt.Errorf("read client hello: %w", err)
	}
	var hello waWa6.HandshakeMessage
	if err := proto.Unmarshal(raw, &hello); err != nil {
		return fmt.Errorf("decode client hello: %w", err)
	}
	clientEphemeral := hello.GetClientHello().GetEphemeral()
	if len(clientEphemeral) != 32 {
		return fmt.Errorf("client ephemeral has %d bytes", len(clientEphemeral))
	}
	clientEphemeralArr := [32]byte(clientEphemeral)

	n := newNoiseState(socket.WAConnHeader)
	n.authenticate(clientEphemeral)
	ephemeral := keys.NewKeyPair()
	n.authenticate(ephemeral.Pub[:])
	if err := n.mix(*ephemeral.Priv, clientEphemeralArr); err != nil {
		return err
	}
	staticCiphertext := n.encrypt(c.server.static.Pub[:])
	if err := n.mix(*c.server.static.Priv, clientEphemeralArr); err != nil {
		return err
	}
	certCiphertext := n.encrypt(c.server.chain.encoded)
	reply, err := proto.Marshal(&waWa6.HandshakeMessage{ServerHello: &waWa6.HandshakeMessage_ServerHello{
		Ephemeral: ephemeral.Pub[:],
		Static:    staticCiphertext,
		Payload:   certCiphertext,
	}})
	if err != nil {
		return err
	}
	if err := c.ws.Write(ctx, websocket.MessageBinary, frame(reply)); err != nil {
		return fmt.Errorf("write server hello: %w", err)
	}

	raw, err = c.frames.next(ctx)
	if err != nil {
		return fmt.Errorf("read client finish: %w", err)
	}
	var finish waWa6.HandshakeMessage
	if err := proto.Unmarshal(raw, &finish); err != nil {
		return fmt.Errorf("decode client finish: %w", err)
	}
	clientStatic, err := n.decrypt(finish.GetClientFinish().GetStatic())
	if err != nil || len(clientStatic) != 32 {
		return fmt.Errorf("decrypt client static: %v", err)
	}
	if err := n.mix(*ephemeral.Priv, [32]byte(clientStatic)); err != nil {
		return err
	}
	payload, err := n.decrypt(finish.GetClientFinish().GetPayload())
	if err != nil {
		return fmt.Errorf("decrypt client payload: %w", err)
	}
	var clientPayload waWa6.ClientPayload
	if err := proto.Unmarshal(payload, &clientPayload); err != nil {
		return fmt.Errorf("decode client payload: %w", err)
	}
	c.ClientPayload = &clientPayload
	c.write, c.read, err = n.transportKeys()
	return err
}

// randomID returns a short unique node id.
func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x", b)
}
