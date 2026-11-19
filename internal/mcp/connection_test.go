package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGuardInternalIDExhaustion(t *testing.T) {
	c := &scriptedConnection{input: []jsonrpc.Message{wire(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)}}
	g := newGuardedConnection(c)
	g.nextID = math.MaxUint64
	if _, err := g.Read(context.Background()); err == nil || !g.closed || len(g.active) != 0 {
		t.Fatal("counter exhaustion did not stop before admission")
	}
}

func TestGuardSDKErrorLatch(t *testing.T) {
	latch := NewSDKErrorLatch()
	logger := latch.Logger()
	logger.Info("synthetic secret")
	select {
	case <-latch.Done():
		t.Fatal("informational log latched")
	default:
	}
	logger.With("secret", "synthetic").Error("arbitrary SDK error", slog.String("error", "synthetic secret"))
	select {
	case <-latch.Done():
	default:
		t.Fatal("SDK error not latched")
	}
	logger.Error("second error")
}

func TestGuardSDKResponseConstructionFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	latch := NewSDKErrorLatch()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, &sdk.ServerOptions{Logger: latch.Logger()})
	server.AddTool(&sdk.Tool{Name: "broken", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{StructuredContent: make(chan int)}, nil
	})
	st, ct := sdk.NewInMemoryTransports()
	transport := &guardCaptureTransport{inner: st, writes: make(chan jsonrpc.Message, 4)}
	ss, err := server.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = client.CallTool(ctx, &sdk.CallToolParams{Name: "broken"}) }()
	select {
	case <-latch.Done():
	case <-ctx.Done():
		t.Fatal("SDK response construction failure was not latched")
	}
	transport.guard.mu.Lock()
	retained := transport.guard.tool && len(transport.guard.active) == 1
	transport.guard.mu.Unlock()
	if !retained {
		t.Fatal("marshal failure discarded outstanding credit")
	}
	cancel()
	_ = client.Close()
	_ = ss.Close()
	<-done
}

func TestGuardInternalIDReuseIsolation(t *testing.T) {
	c := &scriptedConnection{input: []jsonrpc.Message{
		wire(t, `{"jsonrpc":"2.0","id":"external","method":"ping"}`),
		wire(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"external","requestid":"other"}}`),
		wire(t, `{"jsonrpc":"2.0","id":"external","method":"ping"}`),
		wire(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"external"}}`),
	}}
	g := newGuardedConnection(c)
	first, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old := first.(*jsonrpc.Request).ID
	cancel, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(cancel.(*jsonrpc.Request).Params, &p); err != nil {
		t.Fatal(err)
	}
	target, ok := parseRawID(p.RequestID)
	if !ok || target != old {
		t.Fatalf("first cancellation target %s, request %v", p.RequestID, old)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(cancel.(*jsonrpc.Request).Params, &fields) != nil || fields["requestid"] != nil {
		t.Fatal("case-insensitive cancellation alias was forwarded")
	}
	if err := g.Write(context.Background(), &jsonrpc.Response{ID: old, Result: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if c.output[0].(*jsonrpc.Response).ID.Raw() != "external" {
		t.Fatalf("response ID: %v", c.output[0])
	}
	second, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	newID := second.(*jsonrpc.Request).ID
	if old == newID {
		t.Fatal("internal ID reused")
	}
	cancel, err = g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(cancel.(*jsonrpc.Request).Params, &p); err != nil {
		t.Fatal(err)
	}
	target, ok = parseRawID(p.RequestID)
	if !ok || target != newID || target == old {
		t.Fatalf("second cancellation target %s", p.RequestID)
	}
	if len(g.active) != 1 {
		t.Fatalf("retained entries: %d", len(g.active))
	}
	if err := g.Write(context.Background(), &jsonrpc.Response{ID: newID, Result: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if len(g.active) != 0 || c.output[1].(*jsonrpc.Response).ID.Raw() != "external" {
		t.Fatal("retirement or response identity failed")
	}
}

var testGuard = func(c sdk.Connection) sdk.Connection { return newGuardedConnection(c) }

type scriptedConnection struct {
	input    []jsonrpc.Message
	output   []jsonrpc.Message
	writeErr error
}

func (c *scriptedConnection) Read(context.Context) (jsonrpc.Message, error) {
	if len(c.input) == 0 {
		return nil, errors.New("end")
	}
	m := c.input[0]
	c.input = c.input[1:]
	return m, nil
}
func (c *scriptedConnection) Write(_ context.Context, m jsonrpc.Message) error {
	c.output = append(c.output, m)
	return c.writeErr
}
func (*scriptedConnection) Close() error      { return nil }
func (*scriptedConnection) SessionID() string { return "" }

func wire(t *testing.T, s string) jsonrpc.Message {
	t.Helper()
	m, err := jsonrpc.DecodeMessage([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestGuardAdmissionAndRetirement(t *testing.T) {
	c := &scriptedConnection{}
	for _, s := range []string{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"test"}}`, `{"jsonrpc":"2.0","id":3,"method":"ping"}`} {
		c.input = append(c.input, wire(t, s))
	}
	g := testGuard(c)
	first, err := g.Read(context.Background())
	if err != nil || first.(*jsonrpc.Request).Method != "tools/call" {
		t.Fatalf("first: %v %v", first, err)
	}
	if m, err := g.Read(context.Background()); err != nil || m.(*jsonrpc.Request).Method != "ping" {
		t.Fatalf("busy request forwarded: %v %v", m, err)
	}
	if len(c.output) != 1 {
		t.Fatalf("overload replies = %d", len(c.output))
	}
	reply := c.output[0].(*jsonrpc.Response)
	want, err := json.Marshal(toolErrorResult("busy"))
	if err != nil || reply.ID.Raw() != int64(2) || reply.Error != nil || string(reply.Result) != string(want) {
		t.Fatalf("tool overload did not return the common busy result: %+v", reply)
	}
	id := first.(*jsonrpc.Request).ID
	if err := g.Write(context.Background(), &jsonrpc.Response{ID: id, Result: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	c.input = append(c.input, wire(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"test"}}`))
	if _, err := g.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGuardDuplicateAndUnsolicited(t *testing.T) {
	for _, frames := range [][]string{
		{`{"jsonrpc":"2.0","id":"a","method":"ping"}`, `{"jsonrpc":"2.0","id":"\u0061","method":"ping"}`},
		{`{"jsonrpc":"2.0","id":-0,"method":"ping"}`, `{"jsonrpc":"2.0","id":0,"method":"ping"}`},
		{`{"jsonrpc":"2.0","id":1,"result":{}}`},
	} {
		c := &scriptedConnection{}
		for _, s := range frames {
			c.input = append(c.input, wire(t, s))
		}
		g := testGuard(c)
		if len(frames) > 1 {
			if _, err := g.Read(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := g.Read(context.Background()); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestGuardSanitizesProtocolErrors(t *testing.T) {
	c := &scriptedConnection{input: []jsonrpc.Message{wire(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)}}
	g := testGuard(c)
	m, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := m.(*jsonrpc.Request).ID
	err = g.Write(context.Background(), &jsonrpc.Response{ID: id, Error: &jsonrpc.Error{Code: -32602, Message: "secret synthetic", Data: []byte(`"secret synthetic"`)}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := jsonrpc.EncodeMessage(c.output[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") {
		t.Fatalf("secret leaked: %s", b)
	}
}

func TestGuardRawIDBounds(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`9007199254740991`, true}, {`-9007199254740991`, true}, {`9007199254740992`, false}, {`-9007199254740992`, false},
		{`0`, true}, {`-0`, true}, {`1.0`, false}, {`1e0`, false}, {`null`, false}, {`"x"`, true},
		{`"` + strings.Repeat("a", 126) + `"`, true}, {`"` + strings.Repeat("a", 127) + `"`, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			_, got := parseRawID([]byte(tc.raw))
			if got != tc.valid {
				t.Fatalf("valid = %v", got)
			}
		})
	}
}

func TestGuardWriteFailureRetainsCredit(t *testing.T) {
	c := &scriptedConnection{input: []jsonrpc.Message{wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`)}, writeErr: errors.New("failed")}
	g := newGuardedConnection(c)
	m, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := m.(*jsonrpc.Request).ID
	if err := g.Write(context.Background(), &jsonrpc.Response{ID: id}); err == nil {
		t.Fatal("write succeeded")
	}
	if !g.closed || !g.tool || len(g.active) != 1 {
		t.Fatal("failed write discarded accounting")
	}
}

func TestGuardMalformedParamsAndNotifications(t *testing.T) {
	for _, s := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":42,"arguments":"synthetic secret"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"cursor":42}}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":42}}`,
	} {
		c := &scriptedConnection{input: []jsonrpc.Message{wire(t, s), wire(t, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)}}
		g := newGuardedConnection(c)
		m, err := g.Read(context.Background())
		if err != nil || m.(*jsonrpc.Request).Method != "ping" {
			t.Fatalf("malformed forwarded: %v %v", m, err)
		}
		if len(c.output) != 1 {
			t.Fatalf("responses: %d", len(c.output))
		}
		b, err := jsonrpc.EncodeMessage(c.output[0])
		if err != nil || strings.Contains(string(b), "secret") {
			t.Fatalf("unsafe error: %s %v", b, err)
		}
	}
	for _, p := range []string{`null`, `[]`, `{"requestId":1,"reason":42}`, `{"requestId":1,"requestId":2}`} {
		c := &scriptedConnection{input: []jsonrpc.Message{wire(t, fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":%s}`, p))}}
		if _, err := newGuardedConnection(c).Read(context.Background()); err == nil {
			t.Fatalf("accepted cancellation %s", p)
		}
	}
}

func TestGuardNotificationLimit(t *testing.T) {
	c := &scriptedConnection{}
	for i := 1; i <= 1025; i++ {
		c.input = append(c.input, wire(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"ping"}`, i)), wire(t, fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%d}}`, i)))
	}
	g := newGuardedConnection(c)
	for i := 1; i <= 1025; i++ {
		m, err := g.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = g.Read(context.Background())
		if i <= 1024 && err != nil {
			t.Fatalf("notification %d: %v", i, err)
		}
		if i == 1025 {
			if err == nil {
				t.Fatal("1025th forwarded")
			}
			break
		}
		id := m.(*jsonrpc.Request).ID
		if err := g.Write(context.Background(), &jsonrpc.Response{ID: id, Result: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
}

type heldConnection struct {
	*scriptedConnection
	started chan struct{}
	release chan struct{}
}

func (c *heldConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	if msg.(*jsonrpc.Response).ID.Raw() == int64(1) {
		close(c.started)
		select {
		case <-c.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return c.scriptedConnection.Write(ctx, msg)
}

func TestGuardHeldWriteKeepsToolCredit(t *testing.T) {
	c := &heldConnection{scriptedConnection: &scriptedConnection{input: []jsonrpc.Message{
		wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
		wire(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"test"}}`),
		wire(t, `{"jsonrpc":"2.0","id":3,"method":"ping"}`),
	}}, started: make(chan struct{}), release: make(chan struct{})}
	g := newGuardedConnection(c)
	m, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := m.(*jsonrpc.Request).ID
	done := make(chan error, 1)
	go func() { done <- g.Write(context.Background(), &jsonrpc.Response{ID: id, Result: []byte(`{}`)}) }()
	select {
	case <-c.started:
	case <-time.After(time.Second * 5):
		t.Fatal("write did not start")
	}
	m, err = g.Read(context.Background())
	if err != nil || m.(*jsonrpc.Request).Method != "ping" {
		t.Fatalf("held credit: %v %v", m, err)
	}
	close(c.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	c.input = append(c.input, wire(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"test"}}`))
	if _, err := g.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGuardOfficialInMemory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	ss, err := server.Connect(ctx, GuardTransport(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if result := cs.InitializeResult(); result == nil || result.ServerInfo.Name != "test" {
		t.Fatalf("initialize: %+v", result)
	}
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "unknown", Arguments: map[string]any{}}); err == nil {
			t.Fatal("unknown tool succeeded")
		}
	}
}

func TestGuardCancellationBeforeSDKHandlerRetiresCredit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		st, ct := sdk.NewInMemoryTransports()
		server := NewServer(NewSDKErrorLatch(), NewDiagnostics(io.Discard, slog.LevelError))
		defer server.diagnostics.Finish()
		blocked, release := make(chan struct{}), make(chan struct{})
		server.sdk.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
			return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
				if method == "notifications/initialized" {
					close(blocked)
					<-release
				}
				return next(ctx, method, req)
			}
		})
		called := make(chan struct{}, 2)
		server.sdk.AddTool(&sdk.Tool{Name: "list_chats", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			called <- struct{}{}
			return &sdk.CallToolResult{}, nil
		})
		session, err := server.Connect(ctx, st)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		client, err := ct.Connect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		send := func(payload string) {
			t.Helper()
			if err := client.Write(ctx, wire(t, payload)); err != nil {
				t.Fatal(err)
			}
		}
		receive := func(id int64, failed bool) {
			t.Helper()
			m, err := client.Read(ctx)
			r, ok := m.(*jsonrpc.Response)
			if err != nil || !ok || r.ID.Raw() != id || (r.Error != nil) != failed {
				t.Fatalf("response for %d: %v, %v", id, m, err)
			}
		}
		send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
		receive(1, false)
		send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		<-blocked
		send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_chats"}}`)
		send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`)
		synctest.Wait() // SDK's cancellation preempter has run; the handler queue remains held.
		close(release)
		receive(2, true)
		synctest.Wait() // The actual response write and guard retirement have now completed.
		if len(called) != 0 {
			t.Fatal("cancelled queued tool reached the handler")
		}
		send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_chats"}}`)
		receive(2, false)
		if len(called) != 1 {
			t.Fatal("subsequent tool was not admitted")
		}
	})
}

func TestGuardFourOutstanding(t *testing.T) {
	c := &scriptedConnection{}
	for i := 1; i <= 6; i++ {
		c.input = append(c.input, wire(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"ping"}`, i)))
	}
	g := newGuardedConnection(c)
	for i := 1; i <= 4; i++ {
		if _, err := g.Read(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.Read(context.Background()); err == nil {
		t.Fatal("requests beyond capacity forwarded")
	}
	if len(c.output) != 2 {
		t.Fatalf("overload responses = %d", len(c.output))
	}
	for _, msg := range c.output {
		reply := msg.(*jsonrpc.Response)
		var rpcErr *jsonrpc.Error
		if !errors.As(reply.Error, &rpcErr) || rpcErr.Code != -32000 || len(reply.Result) != 0 {
			t.Fatalf("non-tool overload did not return a protocol error: %+v", reply)
		}
	}
}

func TestGuardRejectsNotificationMethodsWithRequestIDs(t *testing.T) {
	c := &scriptedConnection{input: []jsonrpc.Message{
		wire(t, `{"jsonrpc":"2.0","id":1,"method":"notifications/cancelled","params":{"requestId":"example"}}`),
		wire(t, `{"jsonrpc":"2.0","id":2,"method":"notifications/initialized"}`),
		wire(t, `{"jsonrpc":"2.0","id":3,"method":"ping"}`),
	}}
	g := newGuardedConnection(c)
	defer g.Close()
	m, err := g.Read(context.Background())
	if err != nil || m.(*jsonrpc.Request).Method != "ping" || len(c.output) != 2 {
		t.Fatalf("notification method dispatched as a request: %v, %v", m, err)
	}
	for i, m := range c.output {
		reply := m.(*jsonrpc.Response)
		var rpcErr *jsonrpc.Error
		if reply.ID.Raw() != int64(i+1) || !errors.As(reply.Error, &rpcErr) || rpcErr.Code != jsonrpc.CodeMethodNotFound {
			t.Fatalf("unexpected refusal: %+v", reply)
		}
	}
}

func TestGuardUnknownNotificationsDoNotConsumeBudget(t *testing.T) {
	c := &scriptedConnection{}
	for i := 0; i < 1100; i++ {
		c.input = append(c.input, wire(t, `{"jsonrpc":"2.0","method":"notifications/unknown","params":{"secret":"synthetic"}}`))
	}
	c.input = append(c.input, wire(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	g := newGuardedConnection(c)
	if _, err := g.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.notifications != 0 {
		t.Fatalf("unknown notifications counted: %d", g.notifications)
	}
}

func TestGuardInitializedOnce(t *testing.T) {
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	for _, second := range []string{
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":null}`,
	} {
		c := &scriptedConnection{input: []jsonrpc.Message{wire(t, init), wire(t, second)}}
		g := newGuardedConnection(c)
		if _, err := g.Read(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := g.Read(context.Background()); err == nil {
			t.Fatal("invalid initialization accepted")
		}
	}
}

func TestGuardNestedMalformedParams(t *testing.T) {
	for _, params := range []string{
		`{"name":"test","_meta":{"progressToken":{}}}`,
		`{"name":"test","_meta":{"x":1,"x":2}}`,
	} {
		c := &scriptedConnection{input: []jsonrpc.Message{wire(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":%s}`, params)), wire(t, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)}}
		g := newGuardedConnection(c)
		m, err := g.Read(context.Background())
		if err != nil || m.(*jsonrpc.Request).Method != "ping" {
			t.Fatalf("forwarded malformed %s: %v %v", params, m, err)
		}
	}
}

type guardTestReporter struct {
	events []GuardDenial
	fail   bool
}

func (r *guardTestReporter) GuardDenied(event GuardDenial) bool {
	r.events = append(r.events, event)
	return !r.fail
}

func TestGuardDiagnosticDenial(t *testing.T) {
	for _, fail := range []bool{false, true} {
		r := &guardTestReporter{fail: fail}
		c := &scriptedConnection{input: []jsonrpc.Message{wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":42}}`), wire(t, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)}}
		g := newGuardedConnection(c)
		g.reporter = r
		_, err := g.Read(context.Background())
		if (err != nil) != fail || len(r.events) != 1 || r.events[0] != GuardInvalidParams {
			t.Fatalf("report: %v %#v", err, r.events)
		}
		if fail && (!g.closed || len(g.active) != 1 || len(c.output) != 0) {
			t.Fatal("report failure lost credit or kept session open")
		}
	}
}

func TestGuardRefusesResourceSubscriptions(t *testing.T) {
	for _, method := range []string{"resources/subscribe", "resources/unsubscribe", "subscriptions/listen"} {
		t.Run(method, func(t *testing.T) {
			c := &scriptedConnection{input: []jsonrpc.Message{
				wire(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":{"uri":"synthetic-secret"}}`, method)),
				wire(t, `{"jsonrpc":"2.0","id":2,"method":"ping"}`),
			}}
			r := &guardTestReporter{}
			g := newGuardedConnection(c)
			g.reporter = r
			m, err := g.Read(context.Background())
			if err != nil || m.(*jsonrpc.Request).Method != "ping" {
				t.Fatalf("subscription forwarded: %v %v", m, err)
			}
			if len(c.output) != 1 || len(r.events) != 1 || r.events[0] != GuardUnsupported {
				t.Fatalf("refusal: %v %v", c.output, r.events)
			}
		})
	}
}

func TestGuardAuditsSDKToolProtocolFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			c := &scriptedConnection{input: []jsonrpc.Message{wire(t, `{"jsonrpc":"2.0","id":"external","method":"tools/call","params":{"name":"secret"}}`)}}
			r := &guardTestReporter{fail: fail}
			g := newGuardedConnection(c)
			g.reporter = r
			m, err := g.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			err = g.Write(context.Background(), &jsonrpc.Response{ID: m.(*jsonrpc.Request).ID, Error: &jsonrpc.Error{Code: -32602, Message: "secret"}})
			if len(r.events) != 1 || r.events[0] != GuardSDKToolFailure {
				t.Fatalf("events: %v", r.events)
			}
			if fail && (err == nil || !g.closed || len(g.active) != 1 || len(c.output) != 0) {
				t.Fatal("reporting failure did not retain credit and stop")
			}
			if !fail && (err != nil || len(c.output) != 1 || len(g.active) != 0) {
				t.Fatalf("write: %v", err)
			}
		})
	}
}

func TestGuardFramingPendingMessageAllowsNextFrameRead(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	c, err := newFramedTransport(r, io.Discard).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	wroteNext := make(chan error, 1)
	go func() {
		_, err := io.WriteString(w, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n")
		if err == nil {
			_, err = io.WriteString(w, "{")
		}
		wroteNext <- err
	}()
	pending, err := c.Read(context.Background())
	if err != nil || pending == nil {
		t.Fatalf("first frame: %v %v", pending, err)
	}
	select {
	case err := <-wroteNext:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second frame was not read while first decoded message was retained")
	}
}

func TestGuardBusyReplyBlocksNextRead(t *testing.T) {
	c := &heldConnection{scriptedConnection: &scriptedConnection{input: []jsonrpc.Message{
		wire(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"test"}}`),
		wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
		wire(t, `{"jsonrpc":"2.0","id":3,"method":"ping"}`),
	}}, started: make(chan struct{}), release: make(chan struct{})}
	g := newGuardedConnection(c)
	if _, err := g.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := g.Read(context.Background()); done <- err }()
	select {
	case <-c.started:
	case <-time.After(5 * time.Second):
		t.Fatal("busy response did not start")
	}
	select {
	case err := <-done:
		t.Fatalf("read returned during held busy write: %v", err)
	default:
	}
	close(c.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestGuardToolDeadlineCancelsSDKHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 13*time.Second)
	defer cancel()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, &sdk.ServerOptions{Logger: NewSDKErrorLatch().Logger()})
	server.AddTool(&sdk.Tool{Name: "held", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	st, ct := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, GuardTransport(st), nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = client.CallTool(ctx, &sdk.CallToolParams{Name: "held"}) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("tool did not start")
	}
	var failure bool
	select {
	case <-cancelled:
	case <-time.After(11 * time.Second):
		failure = true
	}
	cancel()
	_ = client.Close()
	_ = ss.Close()
	<-done
	if failure {
		t.Fatal("admitted tool was not cancelled by its deadline")
	}
}

func TestGuardSDKInMemoryWriteDoesNotHonorDeadline(t *testing.T) {
	server, client := sdk.NewInMemoryTransports()
	c, err := server.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := client.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	id, _ := jsonrpc.MakeID("test")
	if err := c.Write(context.Background(), &jsonrpc.Response{ID: id, Result: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Write(ctx, &jsonrpc.Response{ID: id, Result: []byte(`{}`)}) }()
	<-ctx.Done()
	select {
	case err := <-done:
		_ = c.Close()
		t.Fatalf("write unexpectedly returned before close: %v", err)
	default:
	}
	_ = c.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("closing in-memory transport did not interrupt write")
	}
}

func TestGuardDeadlineRetainsPendingAndCredit(t *testing.T) {
	c := &scriptedConnection{input: []jsonrpc.Message{
		wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
		wire(t, `{"jsonrpc":"2.0","id":2,"method":"ping"}`),
	}}
	g := newGuardedConnection(c)
	first, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	external, _ := jsonrpc.MakeID(float64(1))
	g.mu.Lock()
	entry := g.active[external]
	entry.deadline = time.Now().Add(-time.Second)
	g.active[external] = entry
	g.mu.Unlock()
	m, err := g.Read(context.Background())
	if err != nil || m.(*jsonrpc.Request).Method != "notifications/cancelled" {
		t.Fatalf("deadline: %v %v", m, err)
	}
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(m.(*jsonrpc.Request).Params, &p) != nil {
		t.Fatal("bad cancellation")
	}
	id, ok := parseRawID(p.RequestID)
	if !ok || id != first.(*jsonrpc.Request).ID {
		t.Fatal("incorrect cancellation target")
	}
	if len(g.active) != 1 || !g.tool || g.notifications != 0 {
		t.Fatal("premature credit retirement or notification charge")
	}
	m, err = g.Read(context.Background())
	if err != nil || m.(*jsonrpc.Request).Method != "ping" {
		t.Fatalf("pending: %v %v", m, err)
	}
	_ = g.Close()
}

func TestGuardBusyWriteDeadlineRetainsCredit(t *testing.T) {
	c := &heldConnection{scriptedConnection: &scriptedConnection{input: []jsonrpc.Message{
		wire(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"test"}}`),
		wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
	}}, started: make(chan struct{}), release: make(chan struct{})}
	g := newGuardedConnection(c)
	if _, err := g.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, _ := jsonrpc.MakeID(float64(2))
	g.mu.Lock()
	entry := g.active[id]
	entry.deadline = time.Now().Add(50 * time.Millisecond)
	g.active[id] = entry
	g.mu.Unlock()
	if _, err := g.Read(context.Background()); err == nil {
		t.Fatal("blocked reply succeeded")
	}
	if !g.closed || !g.tool || len(g.active) != 1 {
		t.Fatal("deadline lost accounting")
	}
}

func TestGuardFramingHeldMessagePreventsReadAhead(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	connection, err := newFramedTransport(r, io.Discard).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	c := connection.(*framedConnection)
	attempt := make(chan struct{})
	wrote := make(chan error, 1)
	go func() {
		_, err := io.WriteString(w, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n")
		close(attempt)
		if err == nil {
			_, err = io.WriteString(w, "{")
		}
		wrote <- err
	}()
	_, release, err := c.readHeld(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	<-attempt
	select {
	case err := <-wrote:
		t.Fatalf("read ahead before release: %v", err)
	default:
	}
	release()
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not resume")
	}
}

func TestGuardSDKBlockedOutputDeadlineRetainsCredit(t *testing.T) {
	st, ct := sdk.NewInMemoryTransports()
	inner, err := st.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := ct.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	id, _ := jsonrpc.MakeID("id")
	if err := inner.Write(context.Background(), &jsonrpc.Response{ID: id, Result: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	g := newGuardedConnection(inner)
	external, _ := jsonrpc.MakeID("external")
	g.active[external] = guardEntry{tool: true, internal: id, deadline: time.Now().Add(50 * time.Millisecond)}
	g.tool = true
	if err := g.Write(context.Background(), &jsonrpc.Response{ID: id, Result: []byte(`{}`)}); err == nil {
		t.Fatal("blocked write succeeded")
	}
	if !g.closed || !g.tool || len(g.active) != 1 {
		t.Fatal("blocked write released credit")
	}
}

func TestGuardCancelledToolCannotSucceed(t *testing.T) {
	c := &scriptedConnection{input: []jsonrpc.Message{
		wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
		wire(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`),
	}}
	g := newGuardedConnection(c)
	m, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := g.Write(context.Background(), &jsonrpc.Response{ID: m.(*jsonrpc.Request).ID, Result: []byte(`{"content":[],"isError":false}`)}); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if !g.closed || len(c.output) != 0 || !g.tool || len(g.active) != 1 {
		t.Fatal("cancelled success was emitted or credit released")
	}
}

func TestGuardCancelledToolMayReturnError(t *testing.T) {
	c := &scriptedConnection{input: []jsonrpc.Message{
		wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
		wire(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`),
	}}
	g := newGuardedConnection(c)
	m, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := g.Write(context.Background(), &jsonrpc.Response{ID: m.(*jsonrpc.Request).ID, Result: []byte(`{"content":[],"isError":true}`)}); err != nil {
		t.Fatal(err)
	}
	if g.closed || len(c.output) != 1 || g.tool || len(g.active) != 0 {
		t.Fatal("cancelled error did not retire")
	}
}

func TestGuardCancellationDuringResponseWrite(t *testing.T) {
	c := &heldConnection{scriptedConnection: &scriptedConnection{input: []jsonrpc.Message{
		wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
		wire(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`),
	}}, started: make(chan struct{}), release: make(chan struct{})}
	g := newGuardedConnection(c)
	m, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- g.Write(context.Background(), &jsonrpc.Response{ID: m.(*jsonrpc.Request).ID, Result: []byte(`{}`)})
	}()
	select {
	case <-c.started:
	case <-time.After(5 * time.Second):
		t.Fatal("response write not started")
	}
	if _, err := g.Read(context.Background()); err == nil {
		t.Fatal("cancellation during write kept session open")
	}
	close(c.release)
	if err := <-done; err == nil {
		t.Fatal("write reported success after cancellation")
	}
	if !g.closed || !g.tool || len(g.active) != 1 {
		t.Fatal("in-flight credit retired")
	}
}

type guardCaptureTransport struct {
	inner  sdk.Transport
	guard  *guardedConnection
	writes chan jsonrpc.Message
}

type guardCaptureConnection struct {
	sdk.Connection
	writes chan jsonrpc.Message
}

func (c *guardCaptureConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	err := c.Connection.Write(ctx, msg)
	select {
	case c.writes <- msg:
	default:
	}
	return err
}

func (t *guardCaptureTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	c, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.guard = newGuardedConnection(&guardCaptureConnection{Connection: c, writes: t.writes})
	return t.guard, nil
}

func TestGuardSDKCancelledHeldHandlerKeepsCredit(t *testing.T) {
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, &sdk.ServerOptions{Logger: NewSDKErrorLatch().Logger()})
	server.AddTool(&sdk.Tool{Name: "held", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return nil, ctx.Err()
	})
	st, ct := sdk.NewInMemoryTransports()
	transport := &guardCaptureTransport{inner: st, writes: make(chan jsonrpc.Message, 4)}
	ss, err := server.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-transport.writes // initialization response
	callCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _, _ = client.CallTool(callCtx, &sdk.CallToolParams{Name: "held"}) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("handler not started")
	}
	cancel()
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("handler not cancelled")
	}
	transport.guard.mu.Lock()
	retained := transport.guard.tool && len(transport.guard.active) == 1
	transport.guard.mu.Unlock()
	if !retained {
		t.Fatal("cancelled held handler lost credit")
	}
	close(release)
	select {
	case <-transport.writes:
	case <-ctx.Done():
		t.Fatal("response did not reach transport")
	}
	// Wait for guard retirement, not merely the underlying write acknowledgment.
	for {
		transport.guard.mu.Lock()
		retained = transport.guard.tool
		transport.guard.mu.Unlock()
		if !retained {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("credit did not retire")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	_ = client.Close()
	_ = ss.Close()
	<-done
}
