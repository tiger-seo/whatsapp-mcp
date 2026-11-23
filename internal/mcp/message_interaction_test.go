package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	wastore "go.mau.fi/whatsmeow/store"
)

type interactionFixtureReader struct {
	ChatContactReader
	row store.MessageView
	err error
}

func TestMessageInteractionStoreSDK(t *testing.T) {
	r := newFixture(t, func(st *store.Store, _ *wastore.Device) {
		rows := []store.Message{{ChatJID: "15550000000@s.whatsapp.net", SenderJID: "15550000001@s.whatsapp.net", ID: "outgoing", FromMe: true, Time: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), Text: "synthetic"}}
		if err := st.StoreMessages(context.Background(), rows); err != nil {
			t.Fatal(err)
		}
	})
	c := chatTestServer(t, r.store)
	for _, jid := range []string{"+1 (555) 000-0000", "15550000000@s.whatsapp.net", "123456789000@lid"} {
		root := chatCall(t, c, "get_last_interaction", map[string]any{"jid": jid}, false)
		if rows := root["messages"].([]any); len(rows) != 1 || rows[0].(map[string]any)["id"] != "outgoing" {
			t.Fatalf("interaction: %v", root)
		}
	}
	root := chatCall(t, c, "get_last_interaction", map[string]any{"jid": "15550009999@s.whatsapp.net"}, true)
	if root["error"].(map[string]any)["code"] != "not_found" {
		t.Fatalf("not found: %v", root)
	}
}

func (r interactionFixtureReader) GetLastInteraction(_ context.Context, _ string, accept func(store.MessageView) (bool, error)) (store.MessageView, error) {
	if r.err != nil {
		return store.MessageView{}, r.err
	}
	ok, err := accept(r.row)
	if err != nil {
		return store.MessageView{}, err
	}
	if !ok {
		return store.MessageView{}, &store.ResourceLimitError{Limit: "reply bytes"}
	}
	return r.row, nil
}

func TestMessageInteractionSDK(t *testing.T) {
	r := interactionFixtureReader{ChatContactReader: newFixture(t).store, row: store.MessageView{
		ID: "selected", ChatJID: "15550000000@s.whatsapp.net", SenderJID: "15550000000@s.whatsapp.net",
		Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Text: "hello\n\x01",
	}}
	c := chatTestServer(t, r)
	tools, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "get_last_interaction" {
			found = true
			schema := chatJSON(t, tool.InputSchema).(map[string]any)
			if schema["additionalProperties"] != false || len(schema["required"].([]any)) != 1 {
				t.Fatalf("schema: %v", schema)
			}
		}
	}
	if !found {
		t.Fatal("get_last_interaction not discovered")
	}
	for _, raw := range []string{`{}`, `{"jid":null}`, `{"jid":""}`, `{"jid":"invalid"}`, `{"jid":true}`, `{"jid":"15550000000@s.whatsapp.net","jid":"x"}`, `{"jid":"15550000000@s.whatsapp.net","query":"private"}`} {
		chatCall(t, c, "get_last_interaction", json.RawMessage(raw), true)
	}
	for _, jid := range []string{"+1 (555) 000-0000", "15550000000@s.whatsapp.net", "12345@lid"} {
		root := chatCall(t, c, "get_last_interaction", map[string]any{"jid": jid}, false)
		messages := root["messages"].([]any)
		if len(messages) != 1 || messages[0].(map[string]any)["id"] != "selected" {
			t.Fatalf("result: %v", root)
		}
	}
}

func TestMessageInteractionLimits(t *testing.T) {
	base := newFixture(t).store
	for _, tc := range []struct{ limit, contains string }{{"interaction work", "verified"}, {"reply bytes", "reply"}} {
		c := chatTestServer(t, interactionFixtureReader{ChatContactReader: base, err: &store.ResourceLimitError{Limit: tc.limit}})
		root := chatCall(t, c, "get_last_interaction", map[string]any{"jid": "15550000000@s.whatsapp.net"}, true)
		e := root["error"].(map[string]any)
		message := e["message"].(string)
		if e["code"] != "resource_limit" || !strings.Contains(message, tc.contains) || strings.Contains(message, "filter") || len(root) != 1 {
			t.Fatalf("limit: %v", root)
		}
	}
	c := chatTestServer(t, interactionFixtureReader{ChatContactReader: base, err: &store.NotFoundError{}})
	root := chatCall(t, c, "get_last_interaction", map[string]any{"jid": "15550000000@s.whatsapp.net"}, true)
	if root["error"].(map[string]any)["code"] != "not_found" {
		t.Fatalf("not found: %v", root)
	}
	large := strings.Repeat("\x01", 256<<10)
	c = chatTestServer(t, interactionFixtureReader{ChatContactReader: base, row: store.MessageView{
		ID: "large", ChatJID: "15550000000@s.whatsapp.net", SenderJID: "15550000000@s.whatsapp.net",
		Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Text: large, Caption: large,
	}})
	root = chatCall(t, c, "get_last_interaction", map[string]any{"jid": "15550000000@s.whatsapp.net"}, true)
	e := root["error"].(map[string]any)
	if e["code"] != "resource_limit" || !strings.Contains(e["message"].(string), "reply") || len(root) != 1 {
		t.Fatalf("oversized verified interaction: %v", root)
	}
}
