package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxInputFrame = 64 << 10
const maxOutputFrame = 8 << 20

var errFrame = errors.New("invalid protocol frame")

type framedTransport struct {
	r    io.Reader
	w    io.Writer
	mu   sync.Mutex
	used bool
}

func newFramedTransport(r io.Reader, w io.Writer) sdk.Transport { return &framedTransport{r: r, w: w} }

func (t *framedTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.used {
		return nil, sdk.ErrConnectionClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.used = true
	c := &framedConnection{done: make(chan struct{}), read: make(chan frameEvent), write: make(chan writeFrame), lock: make(chan struct{}, 1)}
	c.lock <- struct{}{}
	activity := make(chan frameActivity, 2)
	go c.watchFrames(activity)
	go readFrames(t.r, c.read, activity, c.done)
	go writeFrames(t.w, c.write, c.done)
	return c, nil
}

type frameEvent struct {
	data    []byte
	err     error
	release chan struct{}
}
type frameActivity struct {
	start bool
	at    time.Time
}
type writeFrame struct {
	data []byte
	ack  chan error
}
type framedConnection struct {
	done  chan struct{}
	once  sync.Once
	read  chan frameEvent
	write chan writeFrame
	lock  chan struct{}
}

func (c *framedConnection) watchFrames(activity <-chan frameActivity) {
	var timer *time.Timer
	var deadline time.Time
	var expiry <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-c.done:
			return
		case a := <-activity:
			if a.start {
				deadline = a.at.Add(10 * time.Second)
				if timer == nil {
					timer = time.NewTimer(time.Until(deadline))
				} else {
					timer.Reset(time.Until(deadline))
				}
				expiry = timer.C
			} else {
				if a.at.After(deadline) {
					c.Close()
					return
				}
				if timer != nil {
					timer.Stop()
				}
				expiry = nil
			}
		case <-expiry:
			select {
			case a := <-activity:
				if !a.start && !a.at.After(deadline) {
					expiry = nil
					continue
				}
			default:
			}
			c.Close()
			return
		}
	}
}

func readFrames(r io.Reader, out chan<- frameEvent, activity chan<- frameActivity, done <-chan struct{}) {
	b := make([]byte, 0, maxInputFrame)
	var one [1]byte
	for {
		b = b[:0]
		for {
			n, err := r.Read(one[:])
			if n > 0 {
				if len(b) == 0 {
					select {
					case activity <- frameActivity{start: true, at: time.Now()}:
					case <-done:
						return
					}
				}
				b = append(b, one[0])
				if one[0] == '\n' {
					select {
					case activity <- frameActivity{at: time.Now()}:
					case <-done:
						return
					}
					release := make(chan struct{})
					select {
					case out <- frameEvent{data: b, release: release}:
					case <-done:
						return
					}
					select {
					case <-release:
					case <-done:
						return
					}
					break
				}
				if len(b) == maxInputFrame {
					select {
					case out <- frameEvent{err: errFrame}:
					case <-done:
					}
					return
				}
			}
			if err != nil {
				if len(bytes.TrimSpace(b)) != 0 {
					err = errFrame
				}
				select {
				case out <- frameEvent{err: err}:
				case <-done:
				}
				return
			}
			if n == 0 {
				select {
				case out <- frameEvent{err: io.ErrNoProgress}:
				case <-done:
				}
				return
			}
		}
	}
}

func writeFrames(w io.Writer, in <-chan writeFrame, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case f := <-in:
			b := f.data
			var err error
			for len(b) > 0 {
				select {
				case <-done:
					return
				default:
				}
				var n int
				n, err = w.Write(b)
				if n < 0 || n > len(b) {
					err = io.ErrShortWrite
					break
				}
				b = b[n:]
				if err != nil {
					break
				}
				if n == 0 {
					err = io.ErrShortWrite
					break
				}
			}
			f.ack <- err
		}
	}
}

func (c *framedConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	m, release, err := c.readHeld(ctx)
	if release != nil {
		release()
	}
	return m, err
}

func (c *framedConnection) readHeld(ctx context.Context) (jsonrpc.Message, func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			c.Close()
			return nil, nil, err
		}
		select {
		case <-c.done:
			return nil, nil, sdk.ErrConnectionClosed
		default:
		}
		select {
		case <-c.done:
			return nil, nil, sdk.ErrConnectionClosed
		case <-ctx.Done():
			c.Close()
			return nil, nil, ctx.Err()
		case ev := <-c.read:
			if ev.err != nil {
				c.Close()
				return nil, nil, ev.err
			}
			if err := validateFrame(ev.data); err != nil {
				c.Close()
				return nil, nil, err
			}
			msg, err := jsonrpc.DecodeMessage(ev.data)
			if err != nil {
				c.Close()
				return nil, nil, errFrame
			}
			return msg, func() { close(ev.release) }, nil
		}
	}
}

func validateFrame(b []byte) error {
	if len(b) > maxInputFrame || len(b) == 0 || b[len(b)-1] != '\n' {
		return errFrame
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	depth, tokens := 0, 0
	type container struct {
		object bool
		key    bool
		names  map[string]struct{}
	}
	var stack []container
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errFrame
		}
		tokens++
		if tokens > 4096 {
			return errFrame
		}
		if len(stack) > 0 && stack[len(stack)-1].object && stack[len(stack)-1].key {
			if s, ok := tok.(string); ok {
				top := &stack[len(stack)-1]
				if _, exists := top.names[s]; exists {
					return errFrame
				}
				top.names[s] = struct{}{}
				top.key = false
				continue
			}
		}
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].key = true
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
				if depth > 16 {
					return errFrame
				}
				stack = append(stack, container{object: d == '{', key: d == '{', names: make(map[string]struct{})})
			case '}', ']':
				depth--
				stack = stack[:len(stack)-1]
			}
		}
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) != nil || obj == nil {
		return errFrame
	}
	for key := range obj {
		for _, reserved := range []string{"jsonrpc", "method", "id", "params", "result", "error"} {
			if key != reserved && strings.EqualFold(key, reserved) {
				return errFrame
			}
		}
	}
	if id, ok := obj["id"]; ok && !validWireID(id) {
		return errFrame
	}
	var method string
	if json.Unmarshal(obj["method"], &method) != nil {
		return errFrame
	}
	if method == "notifications/cancelled" {
		var p map[string]json.RawMessage
		if json.Unmarshal(obj["params"], &p) != nil || !validWireID(p["requestId"]) {
			return errFrame
		}
	}
	return nil
}

func validWireID(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '"' {
		var s string
		return len(raw) <= 128 && json.Unmarshal(raw, &s) == nil
	}
	s := string(raw)
	if s == "" {
		return false
	}
	for i, c := range s {
		if c == '-' && i == 0 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n >= -9007199254740991 && n <= 9007199254740991
}

func (c *framedConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	fail := func(err error) error { c.Close(); return err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	select {
	case <-c.done:
		return sdk.ErrConnectionClosed
	default:
	}
	select {
	case <-c.done:
		return sdk.ErrConnectionClosed
	case <-ctx.Done():
		return fail(ctx.Err())
	case <-timer.C:
		return fail(context.DeadlineExceeded)
	case <-c.lock:
	}
	defer func() { c.lock <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	select {
	case <-c.done:
		return sdk.ErrConnectionClosed
	default:
	}
	if !preflightFrame(msg) {
		return fail(errFrame)
	}
	b, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return fail(errFrame)
	}
	if !fitsOutputFrame(len(b)) {
		return fail(errFrame)
	}
	b = append(b, '\n')
	f := writeFrame{data: b, ack: make(chan error, 1)}
	select {
	case <-c.done:
		return sdk.ErrConnectionClosed
	case <-ctx.Done():
		return fail(ctx.Err())
	case <-timer.C:
		return fail(context.DeadlineExceeded)
	case c.write <- f:
	}
	select {
	case <-c.done:
		return sdk.ErrConnectionClosed
	case <-ctx.Done():
		return fail(ctx.Err())
	case <-timer.C:
		return fail(context.DeadlineExceeded)
	case err := <-f.ack:
		if err != nil {
			return fail(err)
		}
		return nil
	}
}

func fitsOutputFrame(encodedBytes int) bool {
	return encodedBytes >= 0 && encodedBytes < maxOutputFrame
}

// Bound the SDK's wire encoder before it allocates a frame.
func preflightFrame(msg jsonrpc.Message) bool {
	remaining := maxOutputFrame - 1024
	charge := func(n, factor int) bool {
		if n < 0 || n > remaining/factor {
			return false
		}
		remaining -= n * factor
		return true
	}
	raw := func(b []byte) bool {
		if !charge(len(b), 1) {
			return false
		}
		// Raw JSON is embedded, not quoted. Reserve for any additional HTML
		// and line-separator escaping; existing escapes do not grow.
		for i, c := range b {
			switch {
			case c == '<' || c == '>' || c == '&':
				if !charge(5, 1) {
					return false
				}
			case c == 0xe2 && len(b)-i >= 3 && b[i+1] == 0x80 && (b[i+2] == 0xa8 || b[i+2] == 0xa9):
				if !charge(3, 1) {
					return false
				}
			}
		}
		return true
	}
	id := func(v any) bool {
		switch x := v.(type) {
		case nil, int64:
			return true
		case string:
			return charge(len(x), 6)
		default:
			return false
		}
	}
	switch m := msg.(type) {
	case *jsonrpc.Request:
		return m != nil && id(m.ID.Raw()) && charge(len(m.Method), 6) && raw(m.Params)
	case *jsonrpc.Response:
		if m == nil || !id(m.ID.Raw()) || !raw(m.Result) {
			return false
		}
		if m.Error == nil {
			return true
		}
		e, ok := m.Error.(*jsonrpc.Error)
		return ok && e != nil && charge(len(e.Message), 6) && raw(e.Data)
	default:
		return false
	}
}
func (c *framedConnection) Close() error    { c.once.Do(func() { close(c.done) }); return nil }
func (*framedConnection) SessionID() string { return "" }
