package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestMessageGuardCapacityAndRetirement(t *testing.T) {
	c := &scriptedConnection{input: []jsonrpc.Message{
		wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{"query":"private-query"}}}`),
		wire(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_message_context","arguments":{"message_id":"private-id"}}}`),
		wire(t, `{"jsonrpc":"2.0","id":3,"method":"ping"}`),
		wire(t, `{"jsonrpc":"2.0","id":4,"method":"ping"}`),
		wire(t, `{"jsonrpc":"2.0","id":5,"method":"ping"}`),
	}}
	g := newGuardedConnection(c)
	defer g.Close()
	var admitted []jsonrpc.ID
	for range 4 {
		m, err := g.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		admitted = append(admitted, m.(*jsonrpc.Request).ID)
	}
	if len(g.active) != 4 || !g.tool || len(c.output) != 1 {
		t.Fatalf("capacity: active=%d tool=%v refusals=%d", len(g.active), g.tool, len(c.output))
	}
	busy := c.output[0].(*jsonrpc.Response)
	if busy.ID.Raw() != int64(2) || !strings.Contains(string(busy.Result), `"code":"busy"`) || strings.Contains(string(busy.Result), "private-") {
		t.Fatalf("unsafe busy response: %+v", busy)
	}
	for _, id := range admitted {
		if err := g.Write(context.Background(), &jsonrpc.Response{ID: id, Result: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(g.active) != 0 || g.tool {
		t.Fatal("acknowledged responses did not retire credits")
	}
	c.input = append(c.input, wire(t, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_last_interaction","arguments":{"jid":"15550000000@s.whatsapp.net"}}}`))
	if _, err := g.Read(context.Background()); err != nil {
		t.Fatalf("tool readmission: %v", err)
	}
}

func TestMessageGuardDeadlineAndFailedOutput(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("write_failure=%t", fail), func(t *testing.T) {
			c := &scriptedConnection{input: []jsonrpc.Message{
				wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages"}}`),
				wire(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`),
			}}
			g := newGuardedConnection(c)
			defer g.Close()
			start := time.Now()
			m, err := g.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			external, _ := jsonrpc.MakeID(float64(1))
			entry := g.active[external]
			if remaining := entry.deadline.Sub(start); remaining < 10*time.Second || remaining > 10*time.Second+time.Second {
				t.Fatalf("tool deadline = %v", remaining)
			}
			cancel, err := g.Read(context.Background())
			if err != nil || cancel.(*jsonrpc.Request).Method != "notifications/cancelled" || !g.tool || len(g.active) != 1 {
				t.Fatalf("cancellation retired work: %v %v", cancel, err)
			}
			if fail {
				c.writeErr = errors.New("synthetic output failure")
			}
			err = g.Write(context.Background(), &jsonrpc.Response{ID: m.(*jsonrpc.Request).ID, Result: []byte(`{"content":[],"isError":true}`)})
			if fail {
				if err == nil || !g.closed || !g.tool || len(g.active) != 1 {
					t.Fatal("failed output released credit or kept connection open")
				}
			} else if err != nil || g.tool || len(g.active) != 0 {
				t.Fatalf("acknowledged error did not retire: %v", err)
			}
		})
	}
}

func TestMessageGuardProtocolErrorRedaction(t *testing.T) {
	const markers = "credential-marker query-marker message-marker caption-marker"
	c := &scriptedConnection{input: []jsonrpc.Message{wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{"query":"query-marker"}}}`)}}
	reporter := &guardTestReporter{}
	g := newGuardedConnection(c)
	g.reporter = reporter
	defer g.Close()
	m, err := g.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = g.Write(context.Background(), &jsonrpc.Response{ID: m.(*jsonrpc.Request).ID, Error: &jsonrpc.Error{Code: -32602, Message: markers, Data: []byte(`"` + markers + `"`)}})
	if err != nil || len(c.output) != 1 || len(reporter.events) != 1 || reporter.events[0] != GuardSDKToolFailure {
		t.Fatalf("protocol failure reporting: %v %v", err, reporter.events)
	}
	encoded, err := jsonrpc.EncodeMessage(c.output[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range strings.Fields(markers) {
		if strings.Contains(string(encoded), marker) {
			t.Fatalf("protocol error leaked %s", marker)
		}
	}
}
