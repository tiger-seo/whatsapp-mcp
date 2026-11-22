package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/buildinfo"
)

func TestServerConstruction(t *testing.T) {
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	defer d.Finish()
	server := NewServer(NewSDKErrorLatch(), d)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	init := client.InitializeResult()
	if init.ServerInfo.Name != "whatsapp-mcp" || init.ServerInfo.Version != buildinfo.Get().Version {
		t.Errorf("server identity = %+v", init.ServerInfo)
	}
	caps := init.Capabilities
	if caps.Tools == nil || caps.Tools.ListChanged {
		t.Errorf("tools capability = %+v, want explicitly enabled without list changes", caps.Tools)
	}
	if caps.Resources != nil || caps.Prompts != nil || caps.Logging != nil {
		t.Errorf("unexpected capabilities: %+v", caps)
	}
	list, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 0 {
		t.Errorf("registered %d tools before story implementation", len(list.Tools))
	}
	resources, err := client.ListResources(ctx, nil)
	if err == nil && len(resources.Resources) != 0 {
		t.Error("resources unexpectedly exposed")
	}
	prompts, err := client.ListPrompts(ctx, nil)
	if err == nil && len(prompts.Prompts) != 0 {
		t.Error("prompts unexpectedly exposed")
	}
	if err := client.Subscribe(ctx, &sdk.SubscribeParams{URI: "test://synthetic"}); err != nil {
		t.Fatal(err)
	}
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "event=denied") })
	if _, err := client.ListTools(ctx, nil); err != nil {
		t.Fatalf("session failed after rejected subscription: %v", err)
	}
}

func TestServerSDKExplicitSchemasAndDispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	defer d.Finish()
	server := NewServer(NewSDKErrorLatch(), d)
	in, out := toolSchemas("list_chats")
	accepted := make(chan toolArguments, 2)
	server.addReadTool(sdk.Tool{Name: "list_chats", InputSchema: in, OutputSchema: out}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		a, invalid := validateArguments("list_chats", req.Params.Arguments)
		if invalid != nil {
			return argumentErrorResult(invalid), nil
		}
		accepted <- a
		return encodeResult(ChatPage{Source: "local_store", UntrustedData: true, Limit: a.Limit, Page: a.Page}), nil
	})
	st, ct := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "schema-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	list, err := client.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 || list.Tools[0].Name != "list_chats" {
		t.Fatalf("tool discovery: %+v, %v", list, err)
	}
	decode := func(value any) any {
		t.Helper()
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	tool := list.Tools[0]
	if a := tool.Annotations; a == nil || !a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint || a.OpenWorldHint == nil || *a.OpenWorldHint {
		t.Fatalf("read tool annotations: %+v", a)
	}
	if !reflect.DeepEqual(decode(tool.InputSchema), decode(in)) || !reflect.DeepEqual(decode(tool.OutputSchema), decode(out)) {
		t.Fatal("SDK discovery changed the explicit schemas")
	}
	input := decode(tool.InputSchema).(map[string]any)
	if input["additionalProperties"] != false {
		t.Fatal("input schema is not closed")
	}
	props := input["properties"].(map[string]any)
	for field, want := range map[string]any{"query": "", "limit": float64(30), "page": float64(0), "include_last_message": true, "sort_by": "last_active"} {
		if got := props[field].(map[string]any)["default"]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s default = %v, want %v", field, got, want)
		}
	}
	resolve := func(value any) *jsonschema.Resolved {
		t.Helper()
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatal(err)
		}
		r, err := schema.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	inputSchema, outputSchema := resolve(tool.InputSchema), resolve(tool.OutputSchema)
	if err := inputSchema.Validate(map[string]any{}); err != nil {
		t.Fatalf("input schema rejected defaults: %v", err)
	}
	if inputSchema.Validate(map[string]any{"private_key": "synthetic-secret"}) == nil {
		t.Error("input schema accepted an unknown field")
	}
	if outputSchema.Validate(map[string]any{"error": map[string]any{"code": "invalid_input", "message": "invalid", "private_key": "synthetic-secret"}}) == nil {
		t.Error("output schema accepted an unknown error field")
	}
	if outputSchema.Validate(map[string]any{"source": "local_store", "untrusted_data": true, "chats": []any{}, "count": 0, "limit": 30, "page": 0, "private_key": "synthetic-secret"}) == nil {
		t.Error("output schema accepted an unknown success field")
	}
	for _, tc := range []struct {
		name string
		args map[string]any
		bad  bool
	}{
		{"defaults", map[string]any{}, false},
		{"explicit_false", map[string]any{"include_last_message": false, "page": 0}, false},
		{"invalid_limit", map[string]any{"limit": 0}, true},
		{"private_value", map[string]any{"sort_by": "synthetic-secret"}, true},
		{"private_field", map[string]any{"private_key": "synthetic-secret"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serverTransport, clientTransport := sdk.NewInMemoryTransports()
			session, err := server.Connect(ctx, serverTransport)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			caseClient, err := sdk.NewClient(&sdk.Implementation{Name: "schema-case", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer caseClient.Close()
			result, err := caseClient.CallTool(ctx, &sdk.CallToolParams{Name: "list_chats", Arguments: tc.args})
			if err != nil || result.IsError != tc.bad {
				t.Fatalf("call result: %+v, %v", result, err)
			}
			if err := outputSchema.Validate(decode(result.StructuredContent)); err != nil {
				t.Fatalf("output schema: %v", err)
			}
			if len(result.Content) != 1 {
				t.Fatalf("content count = %d", len(result.Content))
			}
			text, ok := result.Content[0].(*sdk.TextContent)
			if !ok {
				t.Fatal("missing text rendering")
			}
			var rendered any
			if err := json.Unmarshal([]byte(text.Text), &rendered); err != nil || !reflect.DeepEqual(rendered, decode(result.StructuredContent)) {
				t.Fatalf("text and structured content differ: %v", err)
			}
			b, _ := json.Marshal(result)
			if strings.Contains(string(b), "synthetic-secret") || strings.Contains(string(b), "private_key") {
				t.Fatalf("private input leaked: %s", b)
			}
			if !tc.bad {
				a := <-accepted
				if a.Limit != 30 || a.Page != 0 || a.SortBy != "last_active" || a.IncludeLastMessage != (tc.name == "defaults") {
					t.Fatalf("handler defaults = %+v", a)
				}
			}
		})
	}
	awaitDiagnostic(t, func() bool {
		return strings.Count(w.String(), "outcome=denied tool=list_chats category=invalid_input") == 3
	})
	if strings.Contains(w.String(), "synthetic-secret") || strings.Contains(w.String(), "private_key") {
		t.Fatalf("diagnostic exposed input: %s", w.String())
	}
}

func TestServerPredispatchBothTransports(t *testing.T) {
	for _, kind := range []string{"memory", "framed"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			w := &diagnosticWriter{}
			d := NewDiagnostics(w, slog.LevelError)
			defer d.Finish()
			server := NewServer(NewSDKErrorLatch(), d)
			var dispatched atomic.Int32
			server.sdk.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
				return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
					if method == "subscriptions/listen" || method == "resources/subscribe" || method == "tools/call" {
						dispatched.Add(1)
					}
					return next(ctx, method, req)
				}
			})
			var transport sdk.Transport
			var exchange func(string) string
			var notify func(string)
			if kind == "memory" {
				st, ct := sdk.NewInMemoryTransports()
				transport = st
				c, err := ct.Connect(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				notify = func(request string) {
					if err := c.Write(ctx, wire(t, request)); err != nil {
						t.Fatal(err)
					}
				}
				exchange = func(request string) string {
					if err := c.Write(ctx, wire(t, request)); err != nil {
						t.Fatal(err)
					}
					msg, err := c.Read(ctx)
					if err != nil {
						t.Fatal(err)
					}
					b, err := jsonrpc.EncodeMessage(msg)
					if err != nil {
						t.Fatal(err)
					}
					return string(b)
				}
			} else {
				input, send := io.Pipe()
				output, receive := io.Pipe()
				defer send.Close()
				defer output.Close()
				transport = newFramedTransport(input, receive)
				notify = func(request string) {
					if _, err := io.WriteString(send, request+"\n"); err != nil {
						t.Fatal(err)
					}
				}
				exchange = func(request string) string {
					if _, err := io.WriteString(send, request+"\n"); err != nil {
						t.Fatal(err)
					}
					line, err := bufio.NewReader(output).ReadString('\n')
					if err != nil {
						t.Fatal(err)
					}
					return line
				}
			}
			ss, err := server.Connect(ctx, transport)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			init := exchange(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"sampling":{},"elicitation":{}},"clientInfo":{"name":"test","version":"1"}}}`)
			if !strings.Contains(init, `"result"`) {
				t.Fatalf("initialization failed: %s", init)
			}
			notify(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
			for _, tc := range []struct{ request, code string }{
				{`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":42,"arguments":{"value":"synthetic-secret"}}}`, `"code":-32602`},
				{`{"jsonrpc":"2.0","id":8,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true,"promptsListChanged":true,"resourcesListChanged":true,"resourceSubscriptions":["synthetic-secret"]}}}`, `"code":-32601`},
				{`{"jsonrpc":"2.0","id":10,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true,"resourceSubscriptions":["synthetic-secret"]}}}`, `"code":-32601`},
				{`{"jsonrpc":"2.0","id":12,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true}}}`, `"code":-32601`},
				{`{"jsonrpc":"2.0","id":13,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true}}}`, `"code":-32601`},
				{`{"jsonrpc":"2.0","id":14,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true}}}`, `"code":-32601`},
				{`{"jsonrpc":"2.0","id":9,"method":"resources/subscribe","params":{"uri":"synthetic-secret"}}`, `"code":-32601`},
			} {
				response := exchange(tc.request)
				if !strings.Contains(response, tc.code) || strings.Contains(response, "synthetic-secret") {
					t.Fatalf("unsafe or incorrect response: %s", response)
				}
			}
			if dispatched.Load() != 0 {
				t.Fatal("rejected requests reached SDK middleware")
			}
			if response := exchange(`{"jsonrpc":"2.0","id":11,"method":"ping"}`); !strings.Contains(response, `"result"`) {
				t.Fatalf("session did not remain usable after rejected subscriptions: %s", response)
			}
			awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "event=denied") })
			if got := w.String(); strings.Contains(got, "synthetic-secret") || !strings.Contains(got, "category=invalid_input") {
				t.Fatalf("unsafe or incorrect diagnostic: %s", got)
			}
		})
	}
}

func TestServerSDKFailureIsAudited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	defer d.Finish()
	ss, err := NewServer(NewSDKErrorLatch(), d).Connect(ctx, serverTransport)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	_, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "synthetic-secret", Arguments: map[string]any{"secret": "synthetic-value"}})
	if err == nil {
		t.Fatal("unknown tool unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "synthetic") {
		t.Fatalf("SDK error exposed input: %v", err)
	}
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "event=failed") })
	got := w.String()
	for _, want := range []string{"time=", "outcome=failed", "tool=unknown", "category=internal"} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostic missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "synthetic") {
		t.Fatalf("diagnostic exposed input: %s", got)
	}
}

func TestServerKnownToolFailureAuditIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	w := &diagnosticWriter{}
	d := NewDiagnostics(w, slog.LevelError)
	defer d.Finish()
	st, ct := sdk.NewInMemoryTransports()
	ss, err := NewServer(NewSDKErrorLatch(), d).Connect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.CallTool(ctx, &sdk.CallToolParams{Name: "list_chats", Arguments: map[string]any{"query": "synthetic-secret"}})
	if err == nil {
		t.Fatal("unregistered tool unexpectedly succeeded")
	}
	awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "event=failed") })
	if got := w.String(); !strings.Contains(got, "tool=list_chats") || strings.Contains(got, "synthetic-secret") {
		t.Fatalf("incorrect or unsafe tool identity: %s", got)
	}
}

func TestServerSDKMarshalFailureLatches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	latch := NewSDKErrorLatch()
	d := NewDiagnostics(io.Discard, slog.LevelError)
	defer d.Finish()
	server := NewServer(latch, d)
	server.sdk.AddTool(&sdk.Tool{Name: "test_marshal_failure", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{StructuredContent: make(chan int)}, nil
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.CallTool(ctx, &sdk.CallToolParams{Name: "test_marshal_failure", Arguments: map[string]any{}})
	}()
	select {
	case <-latch.Done():
	case <-ctx.Done():
		t.Fatal("SDK response marshal failure was not latched")
	}
	select {
	case <-done:
		if ctx.Err() != nil {
			t.Fatal("client call only stopped at the test deadline")
		}
	case <-ctx.Done():
		t.Fatal("SDK latch did not stop the connection")
	}
	if err := ss.Wait(); err == nil || strings.Contains(err.Error(), "chan int") {
		t.Fatalf("Wait did not report a sanitized SDK failure: %v", err)
	}
}

func TestServerMandatoryAuditFailureStopsSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d := NewDiagnostics(io.Discard, slog.LevelError)
	st, ct := sdk.NewInMemoryTransports()
	ss, err := NewServer(NewSDKErrorLatch(), d).Connect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	d.Finish()
	_, err = client.CallTool(ctx, &sdk.CallToolParams{Name: "synthetic-secret", Arguments: map[string]any{}})
	if err == nil {
		t.Fatal("failed audit was reported as successful call")
	}
	if !d.Failed() {
		t.Fatal("mandatory audit failure was not latched")
	}
	if err := client.Ping(ctx, nil); err == nil {
		t.Fatal("session remained usable after mandatory audit failure")
	}
	select {
	case <-d.Wake():
	default:
		t.Fatal("mandatory audit failure did not wake owner")
	}
}

func TestServerSDKFailureWaitsForHeldHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	latch := NewSDKErrorLatch()
	d := NewDiagnostics(io.Discard, slog.LevelError)
	defer d.Finish()
	server := NewServer(latch, d)
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server.sdk.AddTool(&sdk.Tool{Name: "held", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return nil, ctx.Err()
	})
	st, ct := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = client.CallTool(ctx, &sdk.CallToolParams{Name: "held"})
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("tool did not start")
	}
	latch.Logger().Error("synthetic SDK failure")
	waited := make(chan error, 1)
	go func() { waited <- ss.Wait() }()
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("SDK failure did not cancel the handler")
	}
	ss.guard.mu.Lock()
	retained := ss.guard.closed && ss.guard.tool && len(ss.guard.active) == 1
	ss.guard.mu.Unlock()
	if !retained {
		t.Fatal("closed guard did not retain the held request's credit")
	}
	select {
	case err := <-waited:
		t.Fatalf("session joined while handler was still held: %v", err)
	default:
	}
	close(release)
	released = true
	select {
	case err := <-waited:
		if err != errSDKSession {
			t.Fatalf("Wait = %v, want fixed SDK failure", err)
		}
	case <-ctx.Done():
		t.Fatal("session did not join after handler return")
	}
	ss.guard.mu.Lock()
	cleared := !ss.guard.tool && len(ss.guard.active) == 0
	ss.guard.mu.Unlock()
	if !cleared {
		t.Fatal("credits still retained after actual SDK drain")
	}
	<-callDone
	_ = client.Close()
}

func TestServerNoSamplingOrElicitation(t *testing.T) {
	for _, kind := range []string{"memory", "framed"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			w := &diagnosticWriter{}
			d := NewDiagnostics(w, slog.LevelError)
			defer d.Finish()
			server := NewServer(NewSDKErrorLatch(), d)
			var sent, sampled, elicited atomic.Int32
			server.sdk.AddSendingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
				return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
					if method == "sampling/createMessage" || method == "elicitation/create" {
						sent.Add(1)
					}
					return next(ctx, method, req)
				}
			})
			var st, ct sdk.Transport
			if kind == "memory" {
				st, ct = sdk.NewInMemoryTransports()
			} else {
				sr, cw := io.Pipe()
				cr, sw := io.Pipe()
				defer sr.Close()
				defer sw.Close()
				st, ct = newFramedTransport(sr, sw), &sdk.IOTransport{Reader: cr, Writer: cw}
			}
			ss, err := server.Connect(ctx, st)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			client := sdk.NewClient(&sdk.Implementation{Name: "supported-client", Version: "1"}, &sdk.ClientOptions{
				CreateMessageHandler: func(context.Context, *sdk.CreateMessageRequest) (*sdk.CreateMessageResult, error) {
					sampled.Add(1)
					return nil, nil
				},
				ElicitationHandler: func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
					elicited.Add(1)
					return nil, nil
				},
			})
			cs, err := client.Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			params := ss.InitializeParams()
			if params == nil || params.Capabilities == nil || params.Capabilities.Sampling == nil || params.Capabilities.Elicitation == nil {
				t.Fatalf("client did not advertise sampling and elicitation: %+v", params)
			}
			if _, err := cs.ListTools(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if err := cs.Subscribe(ctx, &sdk.SubscribeParams{URI: "test://synthetic"}); err != nil {
				t.Fatal(err)
			}
			awaitDiagnostic(t, func() bool { return strings.Contains(w.String(), "event=denied") })
			if err := cs.Ping(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if sent.Load() != 0 || sampled.Load() != 0 || elicited.Load() != 0 {
				t.Fatalf("server-originated calls: sent=%d sampling=%d elicitation=%d", sent.Load(), sampled.Load(), elicited.Load())
			}
		})
	}
}
