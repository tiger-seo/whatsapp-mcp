package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	wastore "go.mau.fi/whatsmeow/store"
)

func TestMessageContextSDK(t *testing.T) {
	r := newFixture(t, func(st *store.Store, _ *wastore.Device) {
		var rows []store.Message
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i < 15; i++ {
			rows = append(rows, store.Message{ChatJID: "context@g.us", SenderJID: fixtureJID(0), ID: fmt.Sprintf("ctx-%02d", i), Time: base.Add(time.Duration(i) * time.Minute), Text: "safe\n\x01"})
		}
		for _, count := range []int{2, 100, 101} {
			for i := 0; i < count; i++ {
				rows = append(rows, store.Message{ChatJID: fmt.Sprintf("amb-%03d@g.us", i), SenderJID: fixtureJID(0), ID: fmt.Sprintf("duplicate-%d", count), Time: base})
			}
		}
		large := strings.Repeat("\x01", 256<<10)
		rows = append(rows, store.Message{ChatJID: "huge@g.us", SenderJID: fixtureJID(0), ID: "huge", Time: base, Text: large, Caption: large})
		for i := 0; i < 12; i++ {
			rows = append(rows, store.Message{ChatJID: "reduced@g.us", SenderJID: fixtureJID(0), ID: fmt.Sprintf("row-%02d", i), Time: base.Add(time.Duration(i) * time.Minute), Text: large})
		}
		if err := st.StoreMessages(context.Background(), rows); err != nil {
			t.Fatal(err)
		}
	})
	c := chatTestServer(t, r.store)
	list, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name == "get_message_context" {
			found = true
			s := chatJSON(t, tool.InputSchema).(map[string]any)
			p := s["properties"].(map[string]any)
			if s["additionalProperties"] != false || p["before"].(map[string]any)["default"] != float64(6) || p["after"].(map[string]any)["default"] != float64(6) {
				t.Fatalf("schema: %v", s)
			}
		}
	}
	if !found {
		t.Fatal("context tool missing")
	}
	for _, raw := range []string{`{}`, `{"message_id":null}`, `{"message_id":""}`, `{"message_id":"x","before":21}`, `{"message_id":"x","after":-1}`, `{"message_id":"x","before":null}`, `{"message_id":"x","before":true}`, `{"message_id":"x","message_id":"y"}`, `{"message_id":"x","extra":1}`, `{"message_id":"x","chat_jid":"bad"}`} {
		chatCall(t, c, "get_message_context", json.RawMessage(raw), true)
	}
	root := chatCall(t, c, "get_message_context", map[string]any{"message_id": "ctx-07"}, false)
	if len(root["messages"].([]any)) != 1 || len(root["before"].([]any)) != 6 || len(root["after"].([]any)) != 6 || root["before_reduced"] != false || root["after_reduced"] != false {
		t.Fatalf("defaults: %v", root)
	}
	root = chatCall(t, c, "get_message_context", map[string]any{"message_id": "ctx-07", "chat_jid": "context@g.us", "before": 0, "after": 0}, false)
	if len(root["before"].([]any)) != 0 || len(root["after"].([]any)) != 0 {
		t.Fatalf("zero context: %v", root)
	}
	root = chatCall(t, c, "get_message_context", map[string]any{"message_id": "ctx-07", "chat_jid": "missing@g.us"}, true)
	if root["error"].(map[string]any)["code"] != "not_found" {
		t.Fatalf("exact lookup fell back: %v", root)
	}
	root = chatCall(t, c, "get_message_context", map[string]any{"message_id": "ctx-07", "before": 20, "after": 20}, false)
	if len(root["before"].([]any)) != 7 || len(root["after"].([]any)) != 7 {
		t.Fatalf("maximum counts: %v", root)
	}
	for _, count := range []int{2, 100, 101} {
		root = chatCall(t, c, "get_message_context", map[string]any{"message_id": fmt.Sprintf("duplicate-%d", count)}, true)
		e := root["error"].(map[string]any)
		if count == 101 {
			if e["code"] != "resource_limit" || !strings.Contains(e["message"].(string), "chat_jid") || strings.Contains(fmt.Sprint(root), "amb-") {
				t.Fatalf("too many: %v", root)
			}
		} else {
			chats, ok := e["matching_chats"].([]any)
			if e["code"] != "ambiguous_id" || !ok || len(chats) != count {
				t.Fatalf("ambiguity: %v", root)
			}
			for i, chat := range chats {
				if chat != fmt.Sprintf("amb-%03d@g.us", i) {
					t.Fatalf("chat %d: %v", i, chat)
				}
			}
		}
	}
	root = chatCall(t, c, "get_message_context", map[string]any{"message_id": "huge", "before": 0, "after": 0}, true)
	if e := root["error"].(map[string]any); e["code"] != "resource_limit" || strings.Contains(e["message"].(string), "narrower context") {
		t.Fatalf("oversize: %v", root)
	}
	root = chatCall(t, c, "get_message_context", map[string]any{"message_id": "row-06", "before": 6, "after": 5}, false)
	if root["before_reduced"] != true && root["after_reduced"] != true {
		t.Fatal("expected neighbour reduction")
	}
	if m := root["messages"].([]any)[0].(map[string]any); len(m["content"].(string)) != 256<<10 {
		t.Fatal("anchor was previewed")
	}
}
