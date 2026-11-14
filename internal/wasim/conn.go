package wasim

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/types"
)

// ErrClosed reports an operation on a closed connection.
var ErrClosed = errors.New("wasim: connection closed")

// Conn is one accepted client connection after its Noise handshake.
type Conn struct {
	// ClientPayload is the login or registration payload the client sent in its finish message.
	ClientPayload *waWa6.ClientPayload

	server *Server
	ws     *websocket.Conn
	frames *frames

	write, read           cipher.AEAD
	writeCount, readCount uint32
	writeMu               sync.Mutex

	in      chan *waBinary.Node
	done    chan struct{}
	once    sync.Once
	autoAck atomic.Bool
	handler atomic.Pointer[IQHandler]
}

// IQHandler may answer a client IQ. Returning a node sends it instead of the automatic empty result;
// returning nil leaves the IQ to the automatic answer.
type IQHandler func(*waBinary.Node) *waBinary.Node

// SetIQHandler installs a handler that sees every client IQ before the automatic answer.
func (c *Conn) SetIQHandler(h IQHandler) { c.handler.Store(&h) }

// SetAutoAck controls whether client IQs of type get or set are answered with an empty result.
// It is on by default; the client waits for such answers during connection setup.
func (c *Conn) SetAutoAck(on bool) { c.autoAck.Store(on) }

// Done closes when the connection is gone, from either side.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Close drops the connection immediately.
func (c *Conn) Close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.ws.CloseNow()
	})
}

// Send encrypts and writes one node to the client.
func (c *Conn) Send(ctx context.Context, node waBinary.Node) error {
	data, err := waBinary.Marshal(node)
	if err != nil {
		return fmt.Errorf("wasim: encode %s: %w", node.Tag, err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	ciphertext := c.write.Seal(nil, iv(c.writeCount), data, nil)
	c.writeCount++
	if err := c.ws.Write(ctx, websocket.MessageBinary, frame(ciphertext)); err != nil {
		return err
	}
	return nil
}

// Recv returns the next node the client sent.
func (c *Conn) Recv(ctx context.Context) (*waBinary.Node, error) {
	select {
	case n := <-c.in:
		return n, nil
	case <-c.done:
		select {
		case n := <-c.in:
			return n, nil
		default:
		}
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Expect reads nodes until one satisfies match and returns it; earlier nodes are discarded.
func (c *Conn) Expect(ctx context.Context, match func(*waBinary.Node) bool) (*waBinary.Node, error) {
	for {
		n, err := c.Recv(ctx)
		if err != nil {
			return nil, err
		}
		if match(n) {
			return n, nil
		}
	}
}

func (c *Conn) readLoop(ctx context.Context) {
	defer c.Close()
	for {
		raw, err := c.frames.next(ctx)
		if err != nil {
			return
		}
		plain, err := c.read.Open(nil, iv(c.readCount), raw, nil)
		c.readCount++
		if err != nil {
			c.server.fail(fmt.Errorf("decrypt client frame: %w", err))
			return
		}
		plain, err = waBinary.Unpack(plain)
		if err != nil {
			c.server.fail(fmt.Errorf("unpack client frame: %w", err))
			return
		}
		node, err := waBinary.Unmarshal(plain)
		if err != nil {
			c.server.fail(fmt.Errorf("decode client node: %w", err))
			return
		}
		answered := false
		if h := c.handler.Load(); h != nil && node.Tag == "iq" {
			if reply := (*h)(node); reply != nil {
				answered = true
				_ = c.Send(ctx, *reply)
			}
		}
		if reply := mediaConnReply(node); !answered && reply != nil {
			answered = true
			_ = c.Send(ctx, *reply)
		}
		if !answered && c.autoAck.Load() && node.Tag == "iq" {
			if kind, _ := node.Attrs["type"].(string); kind == "get" || kind == "set" {
				if id, ok := node.Attrs["id"].(string); ok {
					_ = c.Send(ctx, waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{
						"from": types.ServerJID, "id": id, "type": "result",
					}})
				}
			}
		}
		select {
		case c.in <- node:
		case <-c.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// SendPairDevice offers QR references, as the server does to an unregistered client.
func (c *Conn) SendPairDevice(ctx context.Context, refs ...string) error {
	children := make([]waBinary.Node, 0, len(refs))
	for _, ref := range refs {
		children = append(children, waBinary.Node{Tag: "ref", Content: []byte(ref)})
	}
	return c.Send(ctx, waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{
		"from": types.ServerJID, "type": "set", "id": randomID(), "xmlns": "md",
	}, Content: []waBinary.Node{{Tag: "pair-device", Content: children}}})
}

// SendStreamError sends a stream:error with the given code, for example "515" or "503".
func (c *Conn) SendStreamError(ctx context.Context, code string) error {
	return c.Send(ctx, waBinary.Node{Tag: "stream:error", Attrs: waBinary.Attrs{"code": code}})
}

// SendDeviceRemoved sends the stream error that tells a client its device was unlinked.
func (c *Conn) SendDeviceRemoved(ctx context.Context) error {
	return c.Send(ctx, waBinary.Node{Tag: "stream:error", Attrs: waBinary.Attrs{"code": "401"},
		Content: []waBinary.Node{{Tag: "conflict", Attrs: waBinary.Attrs{"type": "device_removed"}}}})
}

// SendSuccess completes a login, which makes the client report Connected once its setup IQs are
// answered. lid is the account's LID, which the client stores if it has none.
func (c *Conn) SendSuccess(ctx context.Context, lid types.JID) error {
	attrs := waBinary.Attrs{"t": strconv.FormatInt(time.Now().Unix(), 10), "location": "sim"}
	if !lid.IsEmpty() {
		attrs["lid"] = lid
	}
	return c.Send(ctx, waBinary.Node{Tag: "success", Attrs: attrs})
}
