package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestMCPFlood(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	const last = 256
	inner := &scriptedConnection{}
	for i := 1; i <= last; i++ {
		inner.input = append(inner.input, wire(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"list_chats"}}`, i)))
	}
	inner.input = append(inner.input, wire(t, `{"jsonrpc":"2.0","id":257,"method":"ping"}`))
	g := newGuardedConnection(inner)
	t.Cleanup(func() { _ = g.Close() })
	m, err := g.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := m.(*jsonrpc.Request).ID
	m, err = g.Read(ctx)
	if err != nil || m.(*jsonrpc.Request).Method != "ping" {
		t.Fatalf("overload entered SDK dispatch: %v, %v", m, err)
	}
	if len(g.active) != 2 || !g.tool || len(inner.output) != last-1 {
		t.Fatalf("retained=%d tool=%v replies=%d", len(g.active), g.tool, len(inner.output))
	}
	for i, m := range inner.output {
		r := m.(*jsonrpc.Response)
		if r.ID.Raw() != int64(i+2) || r.Error != nil || !strings.Contains(string(r.Result), `"code":"busy"`) {
			t.Fatalf("request %d did not get a correlated tool error", i+2)
		}
	}
	for _, id := range []jsonrpc.ID{first, m.(*jsonrpc.Request).ID} {
		if err := g.Write(ctx, &jsonrpc.Response{ID: id, Result: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(g.active) != 0 || g.tool {
		t.Fatal("credits did not retire after successful writes")
	}
	inner.input = append(inner.input, wire(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_chats"}}`))
	if _, err := g.Read(ctx); err != nil {
		t.Fatalf("readmission after flood: %v", err)
	}
}
