package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

func TestMessageArgumentsFoundation(t *testing.T) {
	a, err := validateMessageArguments("list_messages", json.RawMessage(`{}`))
	if err != nil || a.Limit != 50 || a.ContextBefore != 5 || a.ContextAfter != 5 {
		t.Fatalf("defaults: %+v %v", a, err)
	}
	for _, raw := range []string{`null`, `{"query":null}`, `{"query":"private","\u0071uery":"x"}`, `{"private":"x"}`, `{"context_before":21}`, `{"include_context":0}`, `{"limit":200,"page":501}`, `{"after":"2100-01-01T00:00:00Z"}`} {
		_, err := validateMessageArguments("list_messages", json.RawMessage(raw))
		if err == nil || strings.Contains(err.Message, "private") || strings.Contains(err.Field, "private") {
			t.Errorf("expected redacted error for %s: %+v", raw, err)
		}
	}
	if _, err := validateMessageArguments("get_message_context", json.RawMessage(`{"message_id":"id","before":0,"after":20}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := validateMessageArguments("get_last_interaction", json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing jid accepted")
	}
}

func TestMessageArgumentsStrictFields(t *testing.T) {
	for _, tc := range []struct {
		tool, raw string
		valid     bool
	}{
		{"list_messages", `{"query":"","limit":1,"page":100000,"include_context":false,"context_before":20,"context_after":0}`, true},
		{"list_messages", `{"after":"2026-01-02T00:00:00Z","before":"2026-01-01T00:00:00Z"}`, true},
		{"list_messages", `{"limit":null}`, false},
		{"list_messages", `{"limit":1.0}`, true},
		{"list_messages", `{"limit":1.5}`, false},
		{"list_messages", `{"limit":true}`, false},
		{"list_messages", `{"context_after":-1}`, false},
		{"list_messages", `{"include_context":null}`, false},
		{"list_messages", `{"query":[],"private":"secret"}`, false},
		{"list_messages", `{"query":"first","query":"secret"}`, false},
		{"get_message_context", `{"message_id":"id","chat_jid":"123@s.whatsapp.net","before":0,"after":20}`, true},
		{"get_message_context", `{"message_id":null}`, false},
		{"get_message_context", `{"message_id":"id","before":null}`, false},
		{"get_last_interaction", `{"jid":"+1 (234) 567"}`, true},
		{"get_last_interaction", `{"jid":"123@s.whatsapp.net","query":"secret"}`, false},
	} {
		t.Run(tc.tool+"/"+tc.raw, func(t *testing.T) {
			_, err := validateMessageArguments(tc.tool, json.RawMessage(tc.raw))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%+v", tc.valid, err)
			}
			if err != nil && (strings.Contains(err.Field, "secret") || strings.Contains(err.Message, "secret")) {
				t.Fatal("argument error disclosed a supplied value")
			}
		})
	}
}

func TestMessageSizingRejectDoesNotCharge(t *testing.T) {
	s, ok := newMessageSizing(1024)
	if !ok {
		t.Fatal("reserve rejected")
	}
	before := s.charge
	tooLarge := Message{ID: "id", ChatJID: "123@s.whatsapp.net", Sender: "123@s.whatsapp.net", Timestamp: "2026-01-01T00:00:00Z", Content: strings.Repeat("\n", 256<<10), Caption: strings.Repeat("\n", 256<<10)}
	if !validMessageProjection(tooLarge) {
		t.Fatal("valid fields were rejected before reply sizing")
	}
	if s.admit(tooLarge) {
		t.Fatal("oversized escaped message accepted")
	}
	if s.charge != before {
		t.Fatal("rejected message changed sizing ledger")
	}
	small := tooLarge
	small.Content, small.Caption = "small", ""
	if !s.admit(small) {
		t.Fatal("rejected message consumed reply capacity")
	}
}

func TestMessageSizingMetadataReservation(t *testing.T) {
	s, ok := newMessageSizing(0)
	if !ok || s.charge.structured < 1024 || s.charge.text < 1024 {
		t.Fatal("missing fixed envelope reserve")
	}
	if !s.reserve(128, 256) {
		t.Fatal("metadata reserve refused")
	}
	before := s.charge
	if s.reserve(^uint64(0), 0) || s.charge != before {
		t.Fatal("overflowing metadata reserve changed the ledger")
	}
}

func TestMessageSizingRowPolicy(t *testing.T) {
	s, _ := newMessageSizing(1024)
	row := store.MessageView{ID: "id", ChatJID: "123@s.whatsapp.net", SenderJID: "123@s.whatsapp.net", Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Text: "text"}
	if ok, err := s.admitRow(row); err != nil || !ok {
		t.Fatalf("valid row: admitted=%v err=%v", ok, err)
	}
	before := s.charge
	row.MediaType = "private-invalid-media"
	if ok, err := s.admitRow(row); err == nil || ok || strings.Contains(err.Error(), row.MediaType) || s.charge != before {
		t.Fatalf("invalid row leaked or consumed budget: admitted=%v err=%v", ok, err)
	}
	row.MediaType = ""
	row.Text, row.Caption = strings.Repeat("\n", 256<<10), strings.Repeat("\n", 256<<10)
	if ok, err := s.admitRow(row); err != nil || ok || s.charge != before {
		t.Fatalf("oversized row: admitted=%v err=%v", ok, err)
	}
}

func TestMessageProjectionAndSizingFoundation(t *testing.T) {
	row := store.MessageView{ID: "id", ChatJID: "123@s.whatsapp.net", SenderJID: "123@s.whatsapp.net", Time: time.Date(2026, 1, 1, 3, 0, 0, 0, time.FixedZone("x", 3600)), Text: "a\n", Caption: "caption", MediaType: "image"}
	m, ok := messageProjection(row)
	if !ok || m.Timestamp != "2026-01-01T02:00:00Z" || m.Content != "a\n" || m.Caption != "caption" {
		t.Fatalf("projection: %+v %v", m, ok)
	}
	for _, bad := range []store.MessageView{{ID: "secret\x00", ChatJID: row.ChatJID, Time: row.Time}, {ID: "id", ChatJID: row.ChatJID, Time: row.Time, MediaType: "secret"}, {ID: "id", ChatJID: row.ChatJID, Time: row.Time, Text: strings.Repeat("x", (256<<10)+1)}} {
		if _, ok := messageProjection(bad); ok {
			t.Fatal("accepted malformed projection")
		}
	}
	badSender := row
	badSender.SenderJID = ""
	if _, ok := messageProjection(badSender); ok {
		t.Fatal("accepted missing sender")
	}
	s, ok := newMessageSizing(1024)
	if !ok || !s.admit(m) {
		t.Fatal("valid message not admitted")
	}
	if result := encodeMessageList([]Message{m}); result.IsError {
		t.Fatal("valid reply rejected")
	}
	if result := encodeMessageList(nil); result.IsError || string(result.StructuredContent.(json.RawMessage)) != `{"messages":[]}` {
		t.Fatal("empty reply malformed")
	}
	m.Content = strings.Repeat("\n", 256<<10)
	if result := encodeMessageList([]Message{m, m}); !result.IsError {
		t.Fatal("oversized escaped reply accepted")
	}
}

func TestMessageReplyRepresentationParity(t *testing.T) {
	want := Message{ID: "id", ChatJID: "123@s.whatsapp.net", Sender: "123@s.whatsapp.net", Timestamp: "2026-01-01T00:00:00Z", Content: "unicode: \u00e9 \U0001f600\n", Caption: "<caption>\x01"}
	result := encodeMessageList([]Message{want})
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("reply: %#v", result)
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatal("missing text representation")
	}
	var structured, rendered struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(result.StructuredContent.(json.RawMessage), &structured); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(text.Text), &rendered); err != nil {
		t.Fatal(err)
	}
	if len(structured.Messages) != 1 || len(rendered.Messages) != 1 || structured.Messages[0] != want || rendered.Messages[0] != want {
		t.Fatal("message representations differ")
	}
}

func TestMessageFoundationSDKStoreBoundary(t *testing.T) {
	r := newFixture(t)
	reader, ok := r.store.(*store.ToolReader)
	if !ok {
		t.Fatal("fixture does not expose a tool reader")
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "message-foundation-test", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "message_foundation_test", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		sizing, _ := newMessageSizing(1024)
		result, err := reader.GetMessageExact(ctx, fixtureJID(0), "message-1", store.Context{Before: 1}, sizing.admitRow)
		if err != nil {
			return nil, err
		}
		if len(result.Before) != 1 || result.Before[0].ID != "message-0" || result.BeforeReduced {
			return nil, errors.New("unexpected synthetic context")
		}
		message, valid := messageProjection(result.Message)
		if !valid {
			return nil, errors.New("invalid synthetic projection")
		}
		return encodeMessageList([]Message{message}), nil
	})
	client := newSDKSession(t, server)
	result, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "message_foundation_test", Arguments: map[string]any{}})
	if err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("guarded SDK call failed: result=%#v err=%v", result, err)
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok || !strings.Contains(text.Text, `"id":"message-1"`) {
		t.Fatalf("unexpected text reply: %#v", result.Content)
	}
}
