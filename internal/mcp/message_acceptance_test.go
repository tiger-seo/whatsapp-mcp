package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/lock"
	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	wastore "go.mau.fi/whatsmeow/store"
)

func TestMCPMessageAcceptanceDiscoveryAndJourneys(t *testing.T) {
	r := newFixture(t, func(st *store.Store, _ *wastore.Device) {
		base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		rows := make([]store.Message, 0, 17)
		for i := 0; i < 15; i++ {
			rows = append(rows, store.Message{ChatJID: "journey@g.us", SenderJID: fixtureJID(0), ID: fmt.Sprintf("journey-%02d", i), Time: base.Add(time.Duration(i) * time.Minute), Text: "ordinary"})
		}
		rows[7].Text = "distinctive needle"
		rows = append(rows, store.Message{ChatJID: "duplicate@g.us", SenderJID: fixtureJID(1), ID: "journey-07", Time: base})
		if err := st.StoreMessages(t.Context(), rows); err != nil {
			t.Fatal(err)
		}
	})
	c := chatTestServer(t, r.store)
	list, err := c.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{"get_chat", "get_contact_chats", "get_direct_chat_by_contact", "get_last_interaction", "get_message_context", "list_chats", "list_messages", "search_contacts"}
	if list.NextCursor != "" || !reflect.DeepEqual(names, want) {
		t.Fatalf("discovery: %v cursor %q", names, list.NextCursor)
	}
	root := chatCall(t, c, "list_messages", map[string]any{"query": "distinctive needle", "include_context": true}, false)
	rows := root["messages"].([]any)
	if len(rows) != 1 {
		t.Fatalf("search: %v", root)
	}
	hit := rows[0].(map[string]any)
	if hit["id"] != "journey-07" || len(hit["before"].([]any)) != 5 || len(hit["after"].([]any)) != 5 {
		t.Fatalf("search context: %v", hit)
	}
	root = chatCall(t, c, "list_messages", map[string]any{
		"after": "2026-03-01T00:05:00Z", "before": "2026-03-01T00:09:00Z",
		"chat_jid": "journey@g.us", "sender_phone_number": fixtureJID(0), "query": "ORDINARY",
		"limit": 1, "page": 1, "include_context": true, "context_before": 0, "context_after": 1,
	}, false)
	rows = root["messages"].([]any)
	if len(rows) != 1 {
		t.Fatalf("explicit search: %v", root)
	}
	hit = rows[0].(map[string]any)
	after := hit["after"].([]any)
	if hit["id"] != "journey-06" || len(hit["before"].([]any)) != 0 || len(after) != 1 || after[0].(map[string]any)["id"] != "journey-07" {
		t.Fatalf("explicit search context: %v", hit)
	}
	root = chatCall(t, c, "get_message_context", map[string]any{"message_id": "journey-07"}, true)
	if root["error"].(map[string]any)["code"] != "ambiguous_id" {
		t.Fatalf("duplicate: %v", root)
	}
	root = chatCall(t, c, "get_message_context", map[string]any{"message_id": "journey-07", "chat_jid": "journey@g.us"}, false)
	if len(root["before"].([]any)) != 6 || len(root["after"].([]any)) != 6 {
		t.Fatalf("context: %v", root)
	}
	root = chatCall(t, c, "get_message_context", map[string]any{"message_id": "journey-07", "chat_jid": "journey@g.us", "before": 1, "after": 0}, false)
	if len(root["before"].([]any)) != 1 || len(root["after"].([]any)) != 0 {
		t.Fatalf("explicit context: %v", root)
	}
	root = chatCall(t, c, "get_last_interaction", map[string]any{"jid": fixtureJID(0)}, false)
	if root["messages"].([]any)[0].(map[string]any)["id"] != "journey-14" {
		t.Fatalf("interaction: %v", root)
	}
	root = chatCall(t, c, "list_messages", map[string]any{"query": "never-present-acceptance"}, false)
	if len(root["messages"].([]any)) != 0 {
		t.Fatalf("empty: %v", root)
	}
	root = chatCall(t, c, "get_message_context", map[string]any{"message_id": "missing-acceptance"}, true)
	if root["error"].(map[string]any)["code"] != "not_found" {
		t.Fatalf("missing: %v", root)
	}
}

func TestMCPMessageAcceptanceClosedStore(t *testing.T) {
	var st *store.Store
	r := newFixture(t, func(s *store.Store, _ *wastore.Device) { st = s })
	c := chatTestServer(t, r.store)
	if err := st.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_messages", map[string]any{}},
		{"get_message_context", map[string]any{"message_id": "message-0"}},
		{"get_last_interaction", map[string]any{"jid": fixtureJID(0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := chatCall(t, c, tc.name, tc.args, true)
			e := root["error"].(map[string]any)
			message := strings.ToLower(e["message"].(string))
			if len(root) != 1 || e["code"] != "unavailable" || !strings.Contains(message, "session") || strings.Contains(message, "recover") || strings.Contains(message, "filter") {
				t.Errorf("closed-store contract: %v", root)
			}
		})
	}
}

// scaleSearchRows is the history size at which the timed searches must complete within the store deadline. Hosted
// macOS runners are not reliably fast enough for the larger size.
func scaleSearchRows() int {
	if runtime.GOOS == "darwin" {
		return 100000
	}
	return 400000
}

// scaleMatchStep spaces twelve matches through the history, the newest at nine tenths of it.
func scaleMatchStep() int { return scaleSearchRows() * 3 / 40 }

func seedMessageScale(t *testing.T) *store.Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := private.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Release(); err != nil {
			t.Error(err)
		}
	})
	st, err := store.Open(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rowCount, step := scaleSearchRows(), scaleMatchStep()
	for start := 1; start <= rowCount; start += 1000 {
		rows := make([]store.Message, 0, 1000)
		for i := start; i < start+1000; i++ {
			text := "ordinary chatter"
			if i <= 12*step && i%step == 0 {
				text = "sparse needle"
			}
			rows = append(rows, store.Message{ChatJID: fixtureJID(0), SenderJID: fixtureJID(0), ID: fmt.Sprintf("scale-%08d", i), Time: base.Add(time.Duration(i) * time.Second), Text: text})
		}
		if err := st.StoreMessages(t.Context(), rows); err != nil {
			t.Fatalf("seed %d: %v", start, err)
		}
	}
	return st
}

func TestMCPMessageAcceptanceScale(t *testing.T) {
	st := seedMessageScale(t)
	c := chatTestServer(t, st.ToolReader())
	for _, tc := range []struct {
		query string
		count int
	}{{"sparse needle", 12}, {"not-present", 0}} {
		t.Run(tc.query, func(t *testing.T) {
			waitToolCredit(t, c.guard)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			start := time.Now()
			result, err := c.CallTool(ctx, &sdk.CallToolParams{Name: "list_messages", Arguments: map[string]any{"query": tc.query}})
			elapsed := time.Since(start)
			t.Logf("request through reply: %v", elapsed)
			if err != nil {
				t.Fatal(err)
			}
			if elapsed >= 10*time.Second {
				t.Fatalf("deadline exceeded: %v", elapsed)
			}
			root := checkToolResult(t, "list_messages", result, false)
			rows := root["messages"].([]any)
			if len(rows) != tc.count {
				t.Fatalf("matches: %d, want %d", len(rows), tc.count)
			}
			for i, row := range rows {
				if want := fmt.Sprintf("scale-%08d", (12-i)*scaleMatchStep()); row.(map[string]any)["id"] != want {
					t.Fatalf("match %d: %v, want %s", i, row, want)
				}
			}
		})
	}
}
