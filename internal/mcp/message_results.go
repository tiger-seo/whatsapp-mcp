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

type messageContextHit struct {
	Message
	Before        []Message `json:"before"`
	After         []Message `json:"after"`
	BeforeReduced bool      `json:"before_reduced"`
	AfterReduced  bool      `json:"after_reduced"`
	PreviewFields []string  `json:"preview_fields,omitempty"`
}

type messageNextPage struct {
	Page  int `json:"page"`
	Limit int `json:"limit"`
}

type messageContextPage struct {
	Messages     []messageContextHit `json:"messages"`
	PageReduced  bool                `json:"page_reduced"`
	NextPage     *messageNextPage    `json:"next_page,omitempty"`
	NarrowSearch bool                `json:"narrow_search,omitempty"`
}

func messageListPolicy(s *messageSizing, includeContext bool) store.MessageListPolicy {
	return func(row store.MessageView, anchor, first bool) (store.MessageView, bool, error) {
		m, ok := messageProjection(row)
		if !ok {
			return store.MessageView{}, false, errors.New("invalid stored message projection")
		}
		if s.admit(m) {
			return row, true, nil
		}
		if !includeContext || !anchor || !first {
			return store.MessageView{}, false, nil
		}
		row.Text = messagePreview(row.Text)
		row.Caption = messagePreview(row.Caption)
		m, ok = messageProjection(row)
		if !ok || !s.admit(m) {
			return store.MessageView{}, false, nil
		}
		return row, true, nil
	}
}

func messagePreview(s string) string {
	const max = 32 << 10
	if len(s) <= max {
		return s
	}
	end := max
	for !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

func encodeMessageContextPage(page store.MessageList, a messageArguments, sizing messageSizing) *sdk.CallToolResult {
	result := messageContextPage{Messages: make([]messageContextHit, 0, len(page.Hits)), PageReduced: page.PageReduced}
	if page.PageReduced {
		next := a.Offset + len(page.Hits)
		if next > 100000 {
			result.NarrowSearch = true
		} else {
			result.NextPage = &messageNextPage{Page: next, Limit: 1}
		}
	} else if a.Page == 100000 && a.Limit == 1 && len(page.Hits) > 0 {
		result.NarrowSearch = true
	}
	for _, hit := range page.Hits {
		m, ok := messageProjection(hit.Message)
		if !ok {
			return messageReplyLimit()
		}
		item := messageContextHit{Message: m, Before: make([]Message, 0, len(hit.Before)), After: make([]Message, 0, len(hit.After)), BeforeReduced: hit.BeforeReduced, AfterReduced: hit.AfterReduced, PreviewFields: hit.PreviewFields}
		for _, row := range hit.Before {
			m, ok := messageProjection(row)
			if !ok {
				return messageReplyLimit()
			}
			item.Before = append(item.Before, m)
		}
		for _, row := range hit.After {
			m, ok := messageProjection(row)
			if !ok {
				return messageReplyLimit()
			}
			item.After = append(item.After, m)
		}
		result.Messages = append(result.Messages, item)
	}
	structured, err := json.Marshal(result)
	if err != nil || uint64(len(structured)) > sizing.charge.structured {
		return messageReplyLimit()
	}
	text := safeResultText(structured, int(sizing.charge.text))
	if text == "" || !resultFrameFits(uint64(len(structured)), uint64(len(text))) {
		return messageReplyLimit()
	}
	return &sdk.CallToolResult{StructuredContent: json.RawMessage(structured), Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

func messageReplyLimit() *sdk.CallToolResult {
	root := ErrorResult{Error: ToolError{Code: "resource_limit", Message: "Message reply exceeds the size limit; use a smaller list limit or narrower context"}}
	text, _ := json.Marshal(root)
	return &sdk.CallToolResult{IsError: true, StructuredContent: root, Content: []sdk.Content{&sdk.TextContent{Text: string(text)}}}
}

func encodeExactMessageContext(result store.MessageContextResult, sizing messageSizing) *sdk.CallToolResult {
	failure := func() *sdk.CallToolResult {
		return contextError("resource_limit", "Message reply or stored message exceeds a resource limit", nil)
	}
	anchor, ok := messageProjection(result.Message)
	if !ok {
		return failure()
	}
	value := struct {
		Messages      []Message `json:"messages"`
		Before        []Message `json:"before"`
		After         []Message `json:"after"`
		BeforeReduced bool      `json:"before_reduced"`
		AfterReduced  bool      `json:"after_reduced"`
	}{[]Message{anchor}, make([]Message, 0, len(result.Before)), make([]Message, 0, len(result.After)), result.BeforeReduced, result.AfterReduced}
	for _, row := range result.Before {
		m, ok := messageProjection(row)
		if !ok {
			return failure()
		}
		value.Before = append(value.Before, m)
	}
	for _, row := range result.After {
		m, ok := messageProjection(row)
		if !ok {
			return failure()
		}
		value.After = append(value.After, m)
	}
	structured, err := json.Marshal(value)
	if err != nil || uint64(len(structured)) > sizing.charge.structured {
		return failure()
	}
	text := safeResultText(structured, int(sizing.charge.text))
	if text == "" || !resultFrameFits(uint64(len(structured)), uint64(len(text))) {
		return failure()
	}
	return &sdk.CallToolResult{StructuredContent: json.RawMessage(structured), Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}
