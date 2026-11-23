package mcp

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// messageProjection validates the public representation independently of store
// validation. The store must check raw types and lengths before materialization.
func messageProjection(row store.MessageView) (Message, bool) {
	m := Message{ID: row.ID, ChatJID: row.ChatJID, ChatName: row.ChatName,
		Sender: row.SenderJID, SenderName: row.SenderName, Timestamp: resultTimestamp(row.Time),
		IsFromMe: row.FromMe, Content: row.Text, Caption: row.Caption, MediaType: row.MediaType}
	if !validMessageProjection(m) {
		return Message{}, false
	}
	return m, true
}

func validMessageProjection(m Message) bool {
	if m.ID == "" || len(m.ID) > 256 || !utf8.ValidString(m.ID) || strings.IndexFunc(m.ID, unicode.IsControl) >= 0 ||
		!validIdentity(m.ChatJID, false) || !validIdentity(m.Sender, false) || strings.HasSuffix(m.Sender, "@g.us") || !validResultTimestamp(m.Timestamp) {
		return false
	}
	switch m.MediaType {
	case "", "image", "video", "audio", "document":
	default:
		return false
	}
	for _, field := range []struct {
		value string
		max   int
	}{
		{m.ChatName, 1024}, {m.SenderName, 1024},
		{m.Content, 256 << 10}, {m.Caption, 256 << 10},
	} {
		if len(field.value) > field.max || !utf8.ValidString(field.value) {
			return false
		}
	}
	return true
}

// messageSizing precharges both renderings without serializing or retaining rows.
// Callers must reserve enough fixed space for their particular reply metadata.
type messageSizing struct{ charge resultCharge }

func newMessageSizing(reserve uint64) (messageSizing, bool) {
	if reserve < 1024 {
		reserve = 1024
	}
	s := messageSizing{charge: resultCharge{structured: reserve, text: reserve}}
	return s, resultFrameFits(reserve, reserve)
}

// reserve adds metadata and omission markers before admitting their messages.
func (s *messageSizing) reserve(structured, text uint64) bool {
	next := s.charge
	if !addCharge(&next.structured, structured) || !addCharge(&next.text, text) || !resultFrameFits(next.structured, next.text) {
		return false
	}
	s.charge = next
	return true
}

func (s *messageSizing) admit(m Message) bool {
	next := s.charge
	if !chargeResult(reflect.ValueOf(m), "", &next) || !resultFrameFits(next.structured, next.text) {
		return false
	}
	s.charge = next
	return true
}

// admitRow is the synchronous store sizing policy; it does not retain the row.
func (s *messageSizing) admitRow(row store.MessageView) (bool, error) {
	m, ok := messageProjection(row)
	if !ok {
		return false, errors.New("invalid stored message projection")
	}
	return s.admit(m), nil
}

// encodeMessageList is for complete, verified pages only. Context shaping and
// continuation have their own later assembly steps and must not use this path.
func encodeMessageList(messages []Message) *sdk.CallToolResult {
	failure := func() *sdk.CallToolResult {
		root := ErrorResult{Error: ToolError{Code: "resource_limit", Message: "Message reply exceeds the size limit; use a smaller list limit"}}
		text, _ := json.Marshal(root)
		return &sdk.CallToolResult{IsError: true, StructuredContent: root, Content: []sdk.Content{&sdk.TextContent{Text: string(text)}}}
	}
	if len(messages) > 200 {
		return failure()
	}
	sizing, _ := newMessageSizing(1024)
	for _, m := range messages {
		if !validMessageProjection(m) || !sizing.admit(m) {
			return failure()
		}
	}
	if messages == nil {
		messages = []Message{}
	}
	structured, err := json.Marshal(struct {
		Messages []Message `json:"messages"`
	}{messages})
	if err != nil || uint64(len(structured)) > sizing.charge.structured {
		return failure()
	}
	text := safeResultText(structured, int(sizing.charge.text))
	if text == "" || !resultFrameFits(uint64(len(structured)), uint64(len(text))) {
		return failure()
	}
	return &sdk.CallToolResult{StructuredContent: json.RawMessage(structured), Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}
