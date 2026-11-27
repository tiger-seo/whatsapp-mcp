package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	wastore "go.mau.fi/whatsmeow/store"
)

func TestMessageListDiscovery(t *testing.T) {
	c := chatTestServer(t, newFixture(t).store)
	list, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name != "list_messages" {
			continue
		}
		schema := chatJSON(t, tool.InputSchema).(map[string]any)
		props := schema["properties"].(map[string]any)
		want := map[string]any{"after": nil, "before": nil, "chat_jid": nil, "sender_phone_number": nil, "query": "", "limit": float64(50), "page": float64(0), "include_context": false, "context_before": float64(5), "context_after": float64(5)}
		if len(props) != len(want) {
			t.Errorf("properties = %v, want %v", props, want)
		}
		for name, def := range want {
			p, ok := props[name].(map[string]any)
			if !ok {
				t.Errorf("missing property %s", name)
				continue
			}
			if def != nil && !reflect.DeepEqual(p["default"], def) {
				t.Errorf("%s default = %v, want %v", name, p["default"], def)
			}
		}
		if required, ok := schema["required"].([]any); ok && len(required) != 0 {
			t.Errorf("unexpected required fields: %v", required)
		}
		return
	}
	t.Fatal("list_messages not discovered")
}

type limitedMessageReader struct{ ChatContactReader }

func (r limitedMessageReader) ListMessages(context.Context, store.MessageFilter, store.Context, store.Page, bool, store.MessageListPolicy) (store.MessageList, error) {
	return store.MessageList{}, &store.ResourceLimitError{Limit: "search work"}
}

func TestMessageListUnverifiedSearch(t *testing.T) {
	r := newFixture(t)
	c := chatTestServer(t, limitedMessageReader{r.store})
	for _, contextEnabled := range []bool{false, true} {
		root := chatCall(t, c, "list_messages", map[string]any{"query": "private-search", "include_context": contextEnabled}, true)
		failure := root["error"].(map[string]any)
		if failure["code"] != "resource_limit" || !strings.Contains(failure["message"].(string), "narrower") || len(root) != 1 || strings.Contains(fmt.Sprint(root), "private-search") {
			t.Fatalf("unverified search: %+v", root)
		}
	}
}

type timedOutMessageReader struct{ ChatContactReader }

func (r timedOutMessageReader) ListMessages(context.Context, store.MessageFilter, store.Context, store.Page, bool, store.MessageListPolicy) (store.MessageList, error) {
	return store.MessageList{}, context.DeadlineExceeded
}

func TestMessageListTimeLimit(t *testing.T) {
	r := newFixture(t)
	c := chatTestServer(t, timedOutMessageReader{r.store})
	for _, contextEnabled := range []bool{false, true} {
		root := chatCall(t, c, "list_messages", map[string]any{"query": "private-search", "include_context": contextEnabled}, true)
		failure := root["error"].(map[string]any)
		if failure["code"] != "timeout" || len(root) != 1 || strings.Contains(fmt.Sprint(root), "private-search") {
			t.Fatalf("time-limited search: %+v", root)
		}
	}
}

func TestMessageListOversizedVerifiedReply(t *testing.T) {
	r := newFixture(t, func(st *store.Store, _ *wastore.Device) {
		body := "anchor" + strings.Repeat("\x01", (256<<10)-len("anchor"))
		err := st.StoreMessages(context.Background(), []store.Message{{ChatJID: "large@g.us", SenderJID: fixtureJID(0), ID: "large", Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Text: body, Caption: body}})
		if err != nil {
			t.Fatal(err)
		}
	})
	c := chatTestServer(t, r.store)
	args := map[string]any{"chat_jid": "large@g.us", "query": "anchor"}
	root := chatCall(t, c, "list_messages", args, true)
	if e := root["error"].(map[string]any); e["code"] != "resource_limit" || !strings.Contains(e["message"].(string), "reply") || len(root) != 1 {
		t.Fatalf("oversized reply: %+v", root)
	}
	args["include_context"] = true
	root = chatCall(t, c, "list_messages", args, false)
	hits := root["messages"].([]any)
	if len(hits) != 1 || len(hits[0].(map[string]any)["preview_fields"].([]any)) != 2 {
		t.Fatal("verified context result did not identify previews")
	}
}

func TestMessageListArguments(t *testing.T) {
	c := chatTestServer(t, newFixture(t).store)
	invalid := []struct{ raw, field string }{
		{`null`, "arguments"}, {`[]`, "arguments"}, {`{"extra":"secret-value"}`, "arguments"},
		{`{"query":"secret-value","\u0071uery":"x"}`, "query"},
		{`{"after":"1999-12-31T23:59:59Z"}`, "after"}, {`{"after":"not-a-date"}`, "after"},
		{`{"before":"2100-01-01T00:00:00Z"}`, "before"},
		{`{"chat_jid":"1@example.org"}`, "chat_jid"}, {`{"sender_phone_number":"secret-value"}`, "sender_phone_number"},
		{fmt.Sprintf(`{"chat_jid":%q}`, strings.Repeat("1", 257)+"@g.us"), "chat_jid"},
		{fmt.Sprintf(`{"query":%q}`, strings.Repeat("é", 513)), "query"},
		{`{"limit":0}`, "limit"}, {`{"limit":201}`, "limit"}, {`{"limit":1.5}`, "limit"},
		{`{"page":-1}`, "page"}, {`{"page":100001}`, "page"}, {`{"limit":200,"page":501}`, "page"},
		{`{"context_before":-1}`, "context_before"}, {`{"context_before":21,"include_context":false}`, "context_before"},
		{`{"context_after":21}`, "context_after"}, {`{"include_context":0}`, "include_context"},
	}
	for _, field := range []string{"after", "before", "chat_jid", "sender_phone_number", "query", "limit", "page", "include_context", "context_before", "context_after"} {
		invalid = append(invalid, struct{ raw, field string }{fmt.Sprintf(`{%q:null}`, field), field})
		wrong := `true`
		if field == "include_context" {
			wrong = `"true"`
		}
		invalid = append(invalid, struct{ raw, field string }{fmt.Sprintf(`{%q:%s}`, field, wrong), field})
	}
	for i, tc := range invalid {
		t.Run(fmt.Sprintf("invalid_%d", i), func(t *testing.T) {
			root := chatCall(t, c, "list_messages", json.RawMessage(tc.raw), true)
			e, ok := root["error"].(map[string]any)
			if !ok || e["code"] != "invalid_input" || e["field"] != tc.field || e["message"] == "" {
				t.Fatalf("invalid input error: %+v", root)
			}
			if strings.Contains(fmt.Sprint(root), "secret-value") {
				t.Fatal("error disclosed input")
			}
		})
	}
	for i, raw := range []string{
		`{}`, `{"after":"2000-01-01T00:00:00Z","before":"2099-12-31T23:59:59Z"}`,
		`{"after":"2026-01-02T00:00:00Z","before":"2026-01-01T00:00:00Z"}`,
		`{"chat_jid":"15550000000@s.whatsapp.net","sender_phone_number":"+1 (555) 000-0000","query":"synthetic"}`,
		fmt.Sprintf(`{"query":%q}`, strings.Repeat("é", 512)),
		`{"limit":1,"page":100000,"include_context":false,"context_before":0,"context_after":20}`,
		`{"limit":200,"page":500,"include_context":true,"context_before":20,"context_after":0}`,
	} {
		t.Run(fmt.Sprintf("valid_%d", i), func(t *testing.T) {
			chatCall(t, c, "list_messages", json.RawMessage(raw), false)
		})
	}
}

func TestMessageListPagingAndContext(t *testing.T) {
	r := newFixture(t, func(st *store.Store, _ *wastore.Device) {
		base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		var rows []store.Message
		for i := range 12 {
			rows = append(rows, store.Message{ChatJID: "listing@g.us", SenderJID: fixtureJID(0), ID: fmt.Sprintf("list-%02d", i), Time: base.Add(time.Duration(i) * time.Minute), Text: "listing fixture"})
		}
		rows[6].Text = "anchor\ncontrol\x01"
		rows[6].Caption = "caption\tvalue"
		if err := st.StoreMessages(context.Background(), rows); err != nil {
			t.Fatal(err)
		}
	})
	c := chatTestServer(t, r.store)
	root := chatCall(t, c, "list_messages", map[string]any{}, false)
	if got := len(root["messages"].([]any)); got != 50 {
		t.Fatalf("default limit returned %d messages, want 50", got)
	}
	args := map[string]any{"chat_jid": "listing@g.us", "query": "anchor", "context_before": 20, "context_after": 20}
	root = chatCall(t, c, "list_messages", args, false)
	messages := root["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("filtered messages: %+v", root)
	}
	message := messages[0].(map[string]any)
	if message["id"] != "list-06" || message["content"] != "anchor\ncontrol\x01" || message["caption"] != "caption\tvalue" {
		t.Fatalf("message content: %+v", message)
	}
	for _, field := range []string{"before", "after", "before_reduced", "after_reduced"} {
		if _, ok := message[field]; ok {
			t.Errorf("context disabled but %s present", field)
		}
	}
	args["include_context"] = true
	delete(args, "context_before")
	delete(args, "context_after")
	root = chatCall(t, c, "list_messages", args, false)
	hit := root["messages"].([]any)[0].(map[string]any)
	for _, side := range []string{"before", "after"} {
		if got := len(hit[side].([]any)); got != 5 {
			t.Errorf("default %s context = %d, want 5", side, got)
		}
		if hit[side+"_reduced"] != false {
			t.Errorf("unexpected %s reduction: %+v", side, hit)
		}
	}
	args["context_before"], args["context_after"] = 0, 1
	root = chatCall(t, c, "list_messages", args, false)
	hit = root["messages"].([]any)[0].(map[string]any)
	if len(hit["before"].([]any)) != 0 || len(hit["after"].([]any)) != 1 || hit["after"].([]any)[0].(map[string]any)["id"] != "list-07" {
		t.Fatalf("explicit context counts: %+v", hit)
	}
	// checkToolResult compares decoded text and structured content. Also reject
	// literal control bytes in the text rendering itself.
	waitToolCredit(t, c.guard)
	result, err := c.CallTool(context.Background(), &sdk.CallToolParams{Name: "list_messages", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	checkToolResult(t, "list_messages", result, false)
	text := result.Content[0].(*sdk.TextContent).Text
	if strings.ContainsAny(text, "\x01\n\t\r") {
		t.Fatalf("text contains unescaped control characters: %q", text)
	}
}
