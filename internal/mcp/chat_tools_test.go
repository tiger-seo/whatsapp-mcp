package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

type chatTestClient struct {
	*sdk.ClientSession
	guard *guardedConnection
}

func chatTestServer(t *testing.T, r ChatContactReader) chatTestClient {
	t.Helper()
	d := NewDiagnostics(io.Discard, slog.LevelError)
	t.Cleanup(func() { d.Finish() })
	server := NewServer(NewSDKErrorLatch(), d)
	server.RegisterReadTools(r)
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client, err := sdk.NewClient(&sdk.Implementation{Name: "chat-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return chatTestClient{client, ss.guard}
}

func chatJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func chatCall(t *testing.T, c chatTestClient, tool string, args any, bad bool) map[string]any {
	t.Helper()
	waitToolCredit(t, c.guard)
	r, err := c.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return checkToolResult(t, tool, r, bad)
}

func waitToolCredit(t *testing.T, guard *guardedConnection) {
	t.Helper()
	// The in-memory peer can receive the previous reply before Write returns.
	deadline := time.Now().Add(time.Second)
	for {
		guard.mu.Lock()
		busy := guard.tool
		guard.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("previous tool response did not retire its credit")
		}
		time.Sleep(time.Millisecond)
	}
}

func checkToolResult(t *testing.T, tool string, r *sdk.CallToolResult, bad bool) map[string]any {
	t.Helper()
	if r.IsError != bad {
		t.Fatalf("%s error = %v, want %v: %+v", tool, r.IsError, bad, r)
	}
	if len(r.Content) != 1 {
		t.Fatalf("content: %+v", r.Content)
	}
	text, ok := r.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("not text: %+v", r.Content[0])
	}
	var rendered any
	if err := json.Unmarshal([]byte(text.Text), &rendered); err != nil {
		t.Fatal(err)
	}
	value := chatJSON(t, r.StructuredContent)
	if !reflect.DeepEqual(value, rendered) {
		t.Fatal("text differs from structured content")
	}
	_, schema := toolSchemas(tool)
	b, _ := json.Marshal(schema)
	var s jsonschema.Schema
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("output schema: %v", err)
	}
	return value.(map[string]any)
}

func TestChatToolsDiscovery(t *testing.T) {
	c := chatTestServer(t, newFixture(t))
	list, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*sdk.Tool{}
	for _, tool := range list.Tools {
		tools[tool.Name] = tool
	}
	for _, name := range []string{"list_chats", "get_chat"} {
		tool := tools[name]
		if tool == nil {
			t.Errorf("missing tool %s", name)
			continue
		}
		in, out := toolSchemas(name)
		if !reflect.DeepEqual(chatJSON(t, tool.InputSchema), chatJSON(t, in)) || !reflect.DeepEqual(chatJSON(t, tool.OutputSchema), chatJSON(t, out)) {
			t.Errorf("%s schemas differ", name)
		}
		if a := tool.Annotations; a == nil || !a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint || a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s annotations = %+v", name, a)
		}
	}
}

func TestChatToolsPaging(t *testing.T) {
	r := newFixture(t)
	c := chatTestServer(t, r)
	for _, limit := range []int{0, 1, 200} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			size := limit
			if size == 0 {
				size = 30
			}
			seen := map[string]bool{}
			for page := 0; ; page++ {
				args := map[string]any{"page": page}
				if limit != 0 {
					args["limit"] = limit
				}
				before := r.calls.Load()
				root := chatCall(t, c, "list_chats", args, false)
				if r.calls.Load()-before != 1 {
					t.Fatal("expected one reader call")
				}
				chats := root["chats"].([]any)
				if root["limit"] != float64(size) || root["page"] != float64(page) || root["count"] != float64(len(chats)) {
					t.Fatalf("page metadata: %+v", root)
				}
				for _, v := range chats {
					chat := v.(map[string]any)
					jid := chat["jid"].(string)
					if seen[jid] {
						t.Fatalf("duplicate %s", jid)
					}
					seen[jid] = true
					if chat["latest_message"] == nil {
						t.Fatalf("missing default latest for %s", jid)
					}
				}
				if len(chats) < size {
					break
				}
			}
			if len(seen) != fixtureChats {
				t.Fatalf("got %d chats, want %d", len(seen), fixtureChats)
			}
			for i := 0; i < fixtureChats; i++ {
				if !seen[fixtureJID(i)] {
					t.Errorf("missing %s", fixtureJID(i))
				}
			}
		})
	}
	root := chatCall(t, c, "list_chats", map[string]any{"query": "no-such-chat"}, false)
	if chats, ok := root["chats"].([]any); !ok || len(chats) != 0 {
		t.Fatalf("empty chats: %+v", root)
	}
	root = chatCall(t, c, "list_chats", map[string]any{"sort_by": "name", "limit": 1, "include_last_message": false}, false)
	chat := root["chats"].([]any)[0].(map[string]any)
	if chat["jid"] != fixtureJID(0) {
		t.Fatalf("name sort: %+v", chat)
	}
	if _, ok := chat["latest_message"]; ok {
		t.Fatal("latest detail not omitted")
	}
}

func TestChatToolsInvalidArguments(t *testing.T) {
	r := newFixture(t)
	c := chatTestServer(t, r)
	for _, tc := range []struct{ tool, raw, field string }{
		{"list_chats", `{"limit":0}`, "limit"}, {"list_chats", `{"limit":201}`, "limit"},
		{"list_chats", `{"limit":null}`, "limit"}, {"list_chats", `{"limit":"1"}`, "limit"},
		{"list_chats", `{"limit":1.5}`, "limit"}, {"list_chats", `{"page":-1}`, "page"},
		{"list_chats", `{"page":100001}`, "page"}, {"list_chats", `{"limit":200,"page":501}`, "page"},
		{"list_chats", `{"query":null}`, "query"}, {"list_chats", `{"query":12}`, "query"},
		{"list_chats", `{"sort_by":"secret-value"}`, "sort_by"}, {"list_chats", `{"include_last_message":null}`, "include_last_message"},
		{"list_chats", `{"extra":"secret-value"}`, "arguments"}, {"list_chats", `{"limit":1,"\u006cimit":2}`, "limit"},
		{"get_chat", `{}`, "chat_jid"}, {"get_chat", `{"chat_jid":null}`, "chat_jid"},
		{"get_chat", `{"chat_jid":""}`, "chat_jid"}, {"get_chat", `{"chat_jid":"1:2@s.whatsapp.net"}`, "chat_jid"},
		{"get_chat", `{"chat_jid":"1@example.org"}`, "chat_jid"}, {"get_chat", `{"chat_jid":"1@lid","include_last_message":"false"}`, "include_last_message"},
	} {
		t.Run(tc.tool+tc.raw, func(t *testing.T) {
			before := r.calls.Load()
			root := chatCall(t, c, tc.tool, json.RawMessage(tc.raw), true)
			e := root["error"].(map[string]any)
			if e["code"] != "invalid_input" || e["field"] != tc.field || e["message"] == "" {
				t.Fatalf("error: %+v", e)
			}
			if strings.Contains(fmt.Sprint(root), "secret-value") || r.calls.Load() != before {
				t.Fatal("invalid input leaked or reached reader")
			}
		})
	}
}

func TestChatToolsExactLookup(t *testing.T) {
	r := newFixture(t)
	c := chatTestServer(t, r)
	for _, latest := range []bool{true, false} {
		before := r.calls.Load()
		root := chatCall(t, c, "get_chat", map[string]any{"chat_jid": fixtureJID(0), "include_last_message": latest}, false)
		if r.calls.Load()-before != 1 {
			t.Fatal("expected one reader call")
		}
		chat := root["chat"].(map[string]any)
		if chat["jid"] != fixtureJID(0) || chat["last_message_time"] == nil {
			t.Fatalf("chat: %+v", chat)
		}
		_, present := chat["latest_message"]
		if present != latest {
			t.Fatalf("latest presence = %v", present)
		}
	}
	for _, jid := range []string{"123456789000@lid", "999999999999@s.whatsapp.net"} {
		root := chatCall(t, c, "get_chat", map[string]any{"chat_jid": jid}, true)
		if root["error"].(map[string]any)["code"] != "not_found" {
			t.Fatalf("not found: %+v", root)
		}
	}
}

type chatEdgeReader struct{ ChatContactReader }

func (r chatEdgeReader) ListChats(ctx context.Context, q string, sort store.ChatSort, latest bool, p store.Page) ([]store.ChatView, error) {
	if p.Offset >= 35 {
		return []store.ChatView{}, nil
	}
	if p.Limit > 35-p.Offset {
		p.Limit = 35 - p.Offset
	}
	return r.ChatContactReader.ListChats(ctx, q, sort, latest, p)
}

func (r chatEdgeReader) GetChat(ctx context.Context, jid string, latest bool) (store.ChatView, error) {
	switch jid {
	case "empty@g.us":
		return store.ChatView{JID: jid, IsGroup: true}, nil
	case "caption@g.us":
		when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		chat := store.ChatView{JID: jid, IsGroup: true, LastActivity: &when}
		if latest {
			chat.Latest = &store.MessageView{ID: "image", ChatJID: jid, SenderJID: fixtureJID(0), Time: when, Text: "body", Caption: "caption", MediaType: "image"}
		}
		return chat, nil
	default:
		return r.ChatContactReader.GetChat(ctx, jid, latest)
	}
}

func TestChatToolsEdges(t *testing.T) {
	c := chatTestServer(t, chatEdgeReader{newFixture(t)})
	seen := map[string]bool{}
	for page, want := range []int{30, 5, 0} {
		root := chatCall(t, c, "list_chats", map[string]any{"page": page}, false)
		chats := root["chats"].([]any)
		if len(chats) != want {
			t.Fatalf("page %d has %d chats, want %d", page, len(chats), want)
		}
		for _, v := range chats {
			jid := v.(map[string]any)["jid"].(string)
			if seen[jid] {
				t.Fatalf("duplicate %s", jid)
			}
			seen[jid] = true
		}
	}
	if len(seen) != 35 {
		t.Fatalf("got %d chats", len(seen))
	}
	for _, include := range []bool{true, false} {
		root := chatCall(t, c, "get_chat", map[string]any{"chat_jid": "empty@g.us", "include_last_message": include}, false)
		chat := root["chat"].(map[string]any)
		if chat["last_message_time"] != nil {
			t.Fatalf("activity: %+v", chat)
		}
		value, present := chat["latest_message"]
		if present != include || value != nil {
			t.Fatalf("latest: %+v", chat)
		}
	}
	root := chatCall(t, c, "get_chat", map[string]any{"chat_jid": "caption@g.us"}, false)
	message := root["chat"].(map[string]any)["latest_message"].(map[string]any)
	if message["content"] != "body" || message["caption"] != "caption" || message["media_type"] != "image" {
		t.Fatalf("caption: %+v", message)
	}
}
