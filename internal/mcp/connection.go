package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// SDKErrorLatch exposes a payload-free signal for terminal SDK internal errors.
// The session owner must observe Done and stop the connection before waiting.
type SDKErrorLatch struct {
	once sync.Once
	done chan struct{}
}

func NewSDKErrorLatch() *SDKErrorLatch         { return &SDKErrorLatch{done: make(chan struct{})} }
func (l *SDKErrorLatch) Done() <-chan struct{} { return l.done }
func (l *SDKErrorLatch) Logger() *slog.Logger  { return slog.New(sdkErrorHandler{l}) }

type sdkErrorHandler struct{ latch *SDKErrorLatch }

func (sdkErrorHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelError
}
func (h sdkErrorHandler) Handle(context.Context, slog.Record) error {
	h.latch.once.Do(func() { close(h.latch.done) })
	return nil
}
func (h sdkErrorHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h sdkErrorHandler) WithGroup(string) slog.Handler      { return h }

// GuardTransport applies the same admission policy to framed and in-memory transports.
func GuardTransport(inner sdk.Transport) sdk.Transport { return guardedTransport{inner: inner} }

// GuardReporter must return without waiting for diagnostic I/O. False requests
// immediate session shutdown; the reporter is responsible for latching failure.
type GuardReporter interface{ GuardDenied(GuardDenial) bool }

type GuardDenial uint8

const (
	GuardInvalidParams GuardDenial = iota + 1
	GuardBusy
	GuardUnsupported
	GuardSDKToolFailure
)

func GuardTransportWithReporter(inner sdk.Transport, reporter GuardReporter) sdk.Transport {
	return guardedTransport{inner: inner, reporter: reporter}
}

type guardedTransport struct {
	inner    sdk.Transport
	reporter GuardReporter
	attach   func(*guardedConnection)
}

func (t guardedTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	c, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	g := newGuardedConnection(c)
	g.reporter = t.reporter
	if t.attach != nil {
		t.attach(g)
	}
	return g, nil
}

type guardEntry struct {
	tool, cancelled, writing bool
	internal                 jsonrpc.ID
	identity                 DiagnosticTool
	deadline                 time.Time
}

func guardToolIdentity(r *jsonrpc.Request) DiagnosticTool {
	if r.Method != "tools/call" {
		return DiagnosticToolUnknown
	}
	var p struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(r.Params, &p) != nil {
		return DiagnosticToolUnknown
	}
	return diagnosticToolIdentity(p.Name)
}

func diagnosticToolIdentity(name string) DiagnosticTool {
	switch name {
	case "list_chats":
		return DiagnosticListChats
	case "get_chat":
		return DiagnosticGetChat
	case "search_contacts":
		return DiagnosticSearchContacts
	case "get_direct_chat_by_contact":
		return DiagnosticDirectChat
	case "get_contact_chats":
		return DiagnosticContactChats
	default:
		return DiagnosticToolUnknown
	}
}

func (g *guardedConnection) report(denial GuardDenial, identity DiagnosticTool) bool {
	if g.reporter == nil {
		return true
	}
	if reporter, ok := g.reporter.(interface {
		GuardDeniedForTool(GuardDenial, DiagnosticTool) bool
	}); ok {
		return reporter.GuardDeniedForTool(denial, identity)
	}
	return g.reporter.GuardDenied(denial)
}

type guardedConnection struct {
	inner                               sdk.Connection
	reporter                            GuardReporter
	mu                                  sync.Mutex
	active                              map[jsonrpc.ID]guardEntry
	tool, initialized, notified, closed bool
	closeFailed                         bool
	notifications                       int
	nextID                              uint64
	done                                chan struct{}
	closeOnce                           sync.Once
	readerOnce                          sync.Once
	readRequest                         chan struct{}
	readResult                          chan guardReadResult
	reading                             bool
	pending                             *guardReadResult
}

type guardReadResult struct {
	message jsonrpc.Message
	release func()
	err     error
}

func newGuardedConnection(c sdk.Connection) *guardedConnection {
	return &guardedConnection{inner: c, active: make(map[jsonrpc.ID]guardEntry), done: make(chan struct{}), readRequest: make(chan struct{}), readResult: make(chan guardReadResult)}
}

func (g *guardedConnection) SessionID() string { return g.inner.SessionID() }
func (g *guardedConnection) Close() error {
	return g.close(false)
}

func (g *guardedConnection) close(failed bool) error {
	var err error
	g.closeOnce.Do(func() {
		g.mu.Lock()
		g.closed = true
		g.closeFailed = failed
		g.mu.Unlock()
		close(g.done)
		err = g.inner.Close()
	})
	return err
}

func (g *guardedConnection) transportFailed() bool {
	g.mu.Lock()
	failed := g.closeFailed
	g.mu.Unlock()
	// A normal guard stop can win before the guard observes a failure
	// already published by the framed writer.
	framed, ok := g.inner.(*framedConnection)
	return failed || (ok && framed.outputFailed())
}

func (g *guardedConnection) readInput(ctx context.Context) (jsonrpc.Message, bool, error) {
	if g.pending != nil {
		result := *g.pending
		g.pending = nil
		if result.release != nil {
			result.release()
		}
		select {
		case <-g.done:
			return nil, false, sdk.ErrConnectionClosed
		default:
		}
		return result.message, false, result.err
	}
	g.readerOnce.Do(func() {
		go func() {
			for {
				select {
				case <-g.done:
					return
				case <-g.readRequest:
				}
				var result guardReadResult
				if held, ok := g.inner.(interface {
					readHeld(context.Context) (jsonrpc.Message, func(), error)
				}); ok {
					result.message, result.release, result.err = held.readHeld(context.Background())
				} else {
					result.message, result.err = g.inner.Read(context.Background())
				}
				select {
				case g.readResult <- result:
				case <-g.done:
					if result.release != nil {
						result.release()
					}
					return
				}
			}
		}()
	})
	if !g.reading {
		select {
		case g.readRequest <- struct{}{}:
			g.reading = true
		case <-g.done:
			return nil, false, sdk.ErrConnectionClosed
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	for {
		g.mu.Lock()
		var target jsonrpc.ID
		var deadline time.Time
		for _, entry := range g.active {
			if entry.tool && !entry.cancelled {
				target, deadline = entry.internal, entry.deadline
				break
			}
		}
		g.mu.Unlock()
		var timer *time.Timer
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			timer = time.NewTimer(time.Until(deadline))
			timeout = timer.C
		}
		select {
		case <-g.done:
			if timer != nil {
				timer.Stop()
			}
			return nil, false, sdk.ErrConnectionClosed
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return nil, false, ctx.Err()
		case result := <-g.readResult:
			if timer != nil {
				timer.Stop()
			}
			g.reading = false
			if !deadline.IsZero() && !time.Now().Before(deadline) {
				if cancellation := g.deadlineCancellation(target); cancellation != nil {
					g.pending = &result
					return cancellation, true, nil
				}
			}
			if result.release != nil {
				result.release()
			}
			return result.message, false, result.err
		case <-timeout:
			if cancellation := g.deadlineCancellation(target); cancellation != nil {
				return cancellation, true, nil
			}
		}
	}
}

func (g *guardedConnection) deadlineCancellation(target jsonrpc.ID) *jsonrpc.Request {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, entry := range g.active {
		if entry.internal == target && !entry.cancelled && entry.tool {
			entry.cancelled = true
			g.active[id] = entry
			params, _ := json.Marshal(map[string]any{"requestId": target.Raw()})
			return &jsonrpc.Request{Method: "notifications/cancelled", Params: params}
		}
	}
	return nil
}
func (g *guardedConnection) fail() error {
	_ = g.close(true)
	return errors.New("invalid protocol message")
}

func validDecodedID(id jsonrpc.ID) bool {
	switch v := id.Raw().(type) {
	case string:
		b, _ := json.Marshal(v)
		return len(b) <= 128
	case int64:
		return v >= -9007199254740991 && v <= 9007199254740991
	default:
		return false
	}
}

func parseRawID(raw json.RawMessage) (jsonrpc.ID, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return jsonrpc.ID{}, false
	}
	if raw[0] == '"' {
		if len(raw) > 128 {
			return jsonrpc.ID{}, false
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return jsonrpc.ID{}, false
		}
		id, _ := jsonrpc.MakeID(value)
		return id, true
	}
	for i, b := range raw {
		if b == '-' && i == 0 {
			continue
		}
		if b < '0' || b > '9' {
			return jsonrpc.ID{}, false
		}
	}
	if !json.Valid(raw) {
		return jsonrpc.ID{}, false
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || n < -9007199254740991 || n > 9007199254740991 {
		return jsonrpc.ID{}, false
	}
	id, _ := jsonrpc.MakeID(float64(n))
	return id, true
}

func parameterObject(raw json.RawMessage, optional bool) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 && optional {
		return map[string]json.RawMessage{}, true
	}
	if len(raw) > 64<<10 {
		return nil, false
	}
	if !boundedParameterShape(raw) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, false
	}
	values := make(map[string]json.RawMessage)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, false
		}
		name, ok := key.(string)
		if !ok {
			return nil, false
		}
		if _, exists := values[name]; exists {
			return nil, false
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, false
		}
		values[name] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, false
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, false
	}
	return values, true
}

func boundedParameterShape(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	tokens := 0
	var walk func(int, bool) bool
	walk = func(depth int, arguments bool) bool {
		tok, err := d.Token()
		if err != nil {
			return false
		}
		tokens++
		if tokens > 4096 {
			return false
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		if depth >= 16 || (delim != '{' && delim != '[') {
			return false
		}
		seen := make(map[string]struct{})
		for d.More() {
			childArguments := arguments
			if delim == '{' {
				key, err := d.Token()
				if err != nil {
					return false
				}
				tokens++
				name, ok := key.(string)
				if !ok || tokens > 4096 {
					return false
				}
				if _, exists := seen[name]; exists && !arguments {
					return false
				}
				seen[name] = struct{}{}
				if depth == 0 && name == "arguments" {
					childArguments = true
				}
			}
			if !walk(depth+1, childArguments) {
				return false
			}
		}
		end, err := d.Token()
		tokens++
		return err == nil && tokens <= 4096 && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
	}
	if !walk(0, false) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}

func validMetadata(fields map[string]json.RawMessage) bool {
	raw, exists := fields["_meta"]
	if !exists {
		return true
	}
	meta, ok := parameterObject(raw, false)
	if !ok {
		return false
	}
	if token, exists := meta["progressToken"]; exists {
		_, ok = parseRawID(token)
		if !ok {
			return false
		}
	}
	return true
}

func validStringField(fields map[string]json.RawMessage, name string) bool {
	raw, ok := fields[name]
	if !ok {
		return true
	}
	var value string
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &value) == nil
}

func validRequestParams(r *jsonrpc.Request) bool {
	switch r.Method {
	case "initialize", "tools/list", "tools/call", "ping":
		fields, ok := parameterObject(r.Params, r.Method != "initialize")
		if !ok {
			return false
		}
		if !validMetadata(fields) {
			return false
		}
		switch r.Method {
		case "initialize":
			var params sdk.InitializeParams
			return json.Unmarshal(r.Params, &params) == nil && params.Capabilities != nil && params.ClientInfo != nil && params.ProtocolVersion != ""
		case "tools/list":
			var params sdk.ListToolsParams
			return validStringField(fields, "cursor") && (len(r.Params) == 0 || json.Unmarshal(r.Params, &params) == nil)
		case "tools/call":
			var params sdk.CallToolParams
			return validStringField(fields, "name") && json.Unmarshal(r.Params, &params) == nil && params.Name != ""
		}
	}
	return true
}

func (g *guardedConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	for {
		m, synthetic, err := g.readInput(ctx)
		if err != nil {
			_ = g.close(!errors.Is(err, io.EOF) && !errors.Is(err, sdk.ErrConnectionClosed) && !errors.Is(err, context.Canceled))
			return nil, err
		}
		if synthetic {
			return m, nil
		}
		r, ok := m.(*jsonrpc.Request)
		if !ok {
			return nil, g.fail()
		}
		if !r.ID.IsValid() {
			g.mu.Lock()
			if g.closed {
				g.mu.Unlock()
				return nil, sdk.ErrConnectionClosed
			}
			switch r.Method {
			case "notifications/initialized":
				fields, valid := parameterObject(r.Params, true)
				valid = valid && validMetadata(fields)
				if !g.initialized || g.notified || !valid {
					g.mu.Unlock()
					return nil, g.fail()
				}
				g.notified = true
			case "notifications/cancelled":
				fields, valid := parameterObject(r.Params, false)
				if !valid || !validMetadata(fields) || !validStringField(fields, "reason") {
					g.mu.Unlock()
					return nil, g.fail()
				}
				id, valid := parseRawID(fields["requestId"])
				if !valid {
					g.mu.Unlock()
					return nil, g.fail()
				}
				entry, found := g.active[id]
				if !found || entry.cancelled {
					g.mu.Unlock()
					continue
				}
				entry.cancelled = true
				g.active[id] = entry
				if entry.writing {
					g.mu.Unlock()
					_ = g.close(true)
					return nil, errors.New("cancellation during response")
				}
				encoded, _ := json.Marshal(entry.internal.Raw())
				for key := range fields {
					if key != "requestId" && strings.EqualFold(key, "requestId") {
						delete(fields, key)
					}
				}
				fields["requestId"] = encoded
				params, err := json.Marshal(fields)
				if err != nil {
					g.mu.Unlock()
					return nil, g.fail()
				}
				copy := *r
				copy.Params = params
				r = &copy
			default:
				g.mu.Unlock()
				continue
			}
			g.notifications++
			over := g.notifications > 1024
			g.mu.Unlock()
			if over {
				return nil, g.fail()
			}
			return r, nil
		}
		if !validDecodedID(r.ID) {
			return nil, g.fail()
		}
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			return nil, sdk.ErrConnectionClosed
		}
		if _, exists := g.active[r.ID]; exists {
			g.mu.Unlock()
			return nil, g.fail()
		}
		if r.Method == "initialize" {
			if g.initialized {
				g.mu.Unlock()
				return nil, g.fail()
			}
		}
		tool := r.Method == "tools/call"
		identity := guardToolIdentity(r)
		busy := len(g.active) >= 4 || (tool && g.tool)
		invalid := !validRequestParams(r)
		if !busy {
			if g.nextID == math.MaxUint64 {
				g.mu.Unlock()
				return nil, g.fail()
			}
			g.nextID++
			internal, _ := jsonrpc.MakeID(strconv.FormatUint(g.nextID, 10))
			entry := guardEntry{tool: tool, internal: internal, identity: identity}
			if tool {
				entry.deadline = time.Now().Add(10 * time.Second)
			}
			g.active[r.ID] = entry
			if tool {
				g.tool = true
			}
		}
		g.mu.Unlock()
		unsupported := strings.HasPrefix(r.Method, "notifications/") || r.Method == "subscriptions/listen" || r.Method == "resources/subscribe" || r.Method == "resources/unsubscribe"
		if busy || invalid || unsupported {
			code, text := int64(-32000), "server busy"
			denial := GuardBusy
			if invalid {
				code, text = jsonrpc.CodeInvalidParams, "invalid parameters"
				denial = GuardInvalidParams
			} else if unsupported {
				code, text = jsonrpc.CodeMethodNotFound, "method not found"
				denial = GuardUnsupported
			}
			if !g.report(denial, identity) {
				_ = g.Close()
				return nil, errors.New("diagnostic delivery failed")
			}
			g.mu.Lock()
			var deadline time.Time
			for _, entry := range g.active {
				if entry.tool {
					deadline = entry.deadline
					break
				}
			}
			g.mu.Unlock()
			reply := &jsonrpc.Response{ID: r.ID, Error: &jsonrpc.Error{Code: code, Message: text}}
			if denial == GuardBusy && tool {
				result, err := json.Marshal(toolErrorResult("busy"))
				if err != nil {
					_ = g.Close()
					return nil, errors.New("response construction failed")
				}
				reply.Error, reply.Result = nil, result
			}
			writeCtx := ctx
			cancel := func() {}
			var timer *time.Timer
			if !deadline.IsZero() {
				writeCtx, cancel = context.WithDeadline(ctx, deadline)
				timer = time.AfterFunc(time.Until(deadline), func() { _ = g.close(true) })
			}
			err := g.inner.Write(writeCtx, reply)
			if timer != nil {
				timer.Stop()
			}
			cancel()
			if err != nil || (!deadline.IsZero() && !time.Now().Before(deadline)) {
				_ = g.close(true)
				return nil, errors.New("response write failed")
			}
			if !busy {
				g.mu.Lock()
				delete(g.active, r.ID)
				if tool {
					g.tool = false
				}
				g.mu.Unlock()
			}
			continue
		}
		if r.Method == "initialize" {
			g.mu.Lock()
			g.initialized = true
			g.mu.Unlock()
		}
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			return nil, sdk.ErrConnectionClosed
		}
		internal := g.active[r.ID].internal
		g.mu.Unlock()
		copy := *r
		copy.ID = internal
		return &copy, nil
	}
}

func (g *guardedConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	r, ok := msg.(*jsonrpc.Response)
	if !ok {
		_ = g.close(true)
		return errors.New("unexpected outbound message")
	}
	g.mu.Lock()
	var external jsonrpc.ID
	var entry guardEntry
	var exists bool
	for id, candidate := range g.active {
		if candidate.internal == r.ID {
			external, entry, exists = id, candidate, true
			break
		}
	}
	closed := g.closed
	if exists && !entry.writing && !closed {
		entry.writing = true
		g.active[external] = entry
	} else {
		exists = false
	}
	g.mu.Unlock()
	if !exists {
		_ = g.close(true)
		return errors.New("unknown response")
	}
	if entry.tool && entry.cancelled && r.Error == nil {
		var result struct {
			IsError bool `json:"isError"`
		}
		if json.Unmarshal(r.Result, &result) != nil || !result.IsError {
			_ = g.close(true)
			return errors.New("cancelled request returned success")
		}
	}
	if r.Error != nil && !g.report(GuardSDKToolFailure, entry.identity) {
		_ = g.Close()
		return errors.New("diagnostic delivery failed")
	}
	copy := *r
	copy.ID = external
	if r.Error != nil {
		code := int64(jsonrpc.CodeInternalError)
		var wire *jsonrpc.Error
		if errors.As(r.Error, &wire) {
			code = wire.Code
		}
		copy.Error = &jsonrpc.Error{Code: code, Message: "request failed"}
		copy.Result = nil
	}
	writeCtx := ctx
	cancel := func() {}
	var timer *time.Timer
	if entry.tool {
		writeCtx, cancel = context.WithDeadline(ctx, entry.deadline)
		timer = time.AfterFunc(time.Until(entry.deadline), func() { _ = g.close(true) })
	}
	err := g.inner.Write(writeCtx, &copy)
	if timer != nil {
		timer.Stop()
	}
	cancel()
	if err != nil || (entry.tool && !time.Now().Before(entry.deadline)) {
		_ = g.close(true)
		return fmt.Errorf("response write failed")
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return errors.New("response write failed")
	}
	delete(g.active, external)
	if entry.tool {
		g.tool = false
	}
	g.mu.Unlock()
	return nil
}
