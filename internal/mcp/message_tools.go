package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

type messageListReader interface {
	ListMessages(context.Context, store.MessageFilter, store.Context, store.Page, bool, store.MessageListPolicy) (store.MessageList, error)
}

type messageContextReader interface {
	GetMessageContext(context.Context, string, string, store.Context, func(store.MessageView) (bool, error)) (store.MessageContextResult, error)
}

func messageContextHandler(reader messageContextReader) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		raw := req.Params.Arguments
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		a, invalid := validateMessageArguments("get_message_context", raw)
		if invalid != nil {
			return argumentErrorResult(invalid), nil
		}
		sizing, _ := newMessageSizing(128 << 10)
		result, err := reader.GetMessageContext(ctx, a.ChatJID, a.MessageID, store.Context{Before: a.ContextBefore, After: a.ContextAfter}, sizing.admitRow)
		if err != nil {
			var ambiguous *store.AmbiguousIDError
			var limit *store.ResourceLimitError
			if errors.As(err, &ambiguous) {
				if len(ambiguous.ChatJIDs) < 2 || len(ambiguous.ChatJIDs) > 100 {
					return contextError("resource_limit", "Too many matching chats; specify chat_jid", nil), nil
				}
				for i, chat := range ambiguous.ChatJIDs {
					if !validIdentity(chat, false) || (i > 0 && ambiguous.ChatJIDs[i-1] >= chat) {
						return contextError("resource_limit", "Message identity could not be verified", nil), nil
					}
				}
				return contextError("ambiguous_id", "Message ID matches multiple chats; specify chat_jid", ambiguous.ChatJIDs), nil
			}
			if errors.As(err, &limit) {
				if limit.Limit == "matching chats" {
					return contextError("resource_limit", "Too many matching chats; specify chat_jid", nil), nil
				}
				return contextError("resource_limit", "Message reply or stored message exceeds a resource limit", nil), nil
			}
			if errors.Is(err, store.ErrNotFound) {
				return contextError("not_found", "Message not found", nil), nil
			}
			return chatReadError(err, "message_id"), nil
		}
		return encodeExactMessageContext(result, sizing), nil
	}
}

func contextError(code, message string, chats []string) *sdk.CallToolResult {
	if len(chats) > 0 {
		root := struct {
			Error struct {
				Code          string   `json:"code"`
				Message       string   `json:"message"`
				MatchingChats []string `json:"matching_chats"`
			} `json:"error"`
		}{}
		root.Error.Code, root.Error.Message, root.Error.MatchingChats = code, message, chats
		text, _ := json.Marshal(root)
		return &sdk.CallToolResult{IsError: true, StructuredContent: root, Content: []sdk.Content{&sdk.TextContent{Text: string(text)}}}
	}
	root := ErrorResult{Error: ToolError{Code: code, Message: message}}
	text, _ := json.Marshal(root)
	return &sdk.CallToolResult{IsError: true, StructuredContent: root, Content: []sdk.Content{&sdk.TextContent{Text: string(text)}}}
}

func messageContextSchemas() (map[string]any, map[string]any) {
	_, listOutput := messageListSchemas()
	plain := listOutput["oneOf"].([]any)[0].(map[string]any)
	messageArray := plain["properties"].(map[string]any)["messages"].(map[string]any)
	message := messageArray["items"]
	array := map[string]any{"type": "array", "items": message}
	input := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"message_id"}, "properties": map[string]any{
		"message_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, "chat_jid": map[string]any{"type": "string", "maxLength": 256},
		"before": map[string]any{"type": "integer", "minimum": 0, "maximum": 20, "default": 6}, "after": map[string]any{"type": "integer", "minimum": 0, "maximum": 20, "default": 6},
	}}
	success := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"messages", "before", "after", "before_reduced", "after_reduced"}, "properties": map[string]any{
		"messages": map[string]any{"type": "array", "items": message, "minItems": 1, "maxItems": 1}, "before": array, "after": array, "before_reduced": map[string]any{"type": "boolean"}, "after_reduced": map[string]any{"type": "boolean"},
	}}
	failure := listOutput["oneOf"].([]any)[2].(map[string]any)
	errorSchema := failure["properties"].(map[string]any)["error"].(map[string]any)
	errorSchema["properties"].(map[string]any)["matching_chats"] = map[string]any{"type": "array", "minItems": 2, "maxItems": 100, "items": map[string]any{"type": "string", "maxLength": 256}}
	return input, map[string]any{"type": "object", "oneOf": []any{success, failure}}
}

func messageListHandler(reader messageListReader) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		raw := req.Params.Arguments
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		a, invalid := validateMessageArguments("list_messages", raw)
		if invalid != nil {
			return argumentErrorResult(invalid), nil
		}
		sizing, _ := newMessageSizing(128 << 10)
		page, err := reader.ListMessages(ctx, store.MessageFilter{After: a.After, Before: a.Before, ChatJID: a.ChatJID, Sender: a.Sender, Query: a.Query}, store.Context{Before: a.ContextBefore, After: a.ContextAfter}, store.Page{Limit: a.Limit, Offset: a.Offset}, a.IncludeContext, messageListPolicy(&sizing, a.IncludeContext))
		if err != nil {
			var limit *store.ResourceLimitError
			if errors.As(err, &limit) {
				if limit.Limit == "search work" || limit.Limit == "message identity" {
					root := ErrorResult{Error: ToolError{Code: "resource_limit", Message: "The message search could not be verified within its work limit; use narrower filters"}}
					text, _ := json.Marshal(root)
					return &sdk.CallToolResult{IsError: true, StructuredContent: root, Content: []sdk.Content{&sdk.TextContent{Text: string(text)}}}, nil
				}
				return messageReplyLimit(), nil
			}
			return chatReadError(err, "arguments"), nil
		}
		if a.IncludeContext {
			return encodeMessageContextPage(page, a, sizing), nil
		}
		messages := make([]Message, 0, len(page.Hits))
		for _, hit := range page.Hits {
			m, ok := messageProjection(hit.Message)
			if !ok {
				return messageReplyLimit(), nil
			}
			messages = append(messages, m)
		}
		return encodeMessageList(messages), nil
	}
}

func messageListSchemas() (map[string]any, map[string]any) {
	text := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	num := func(min, max, def int) map[string]any {
		return map[string]any{"type": "integer", "minimum": min, "maximum": max, "default": def}
	}
	query := text(1024)
	query["default"] = ""
	props := map[string]any{"after": text(64), "before": text(64), "chat_jid": text(256), "sender_phone_number": text(256), "query": query, "limit": num(1, 200, 50), "page": num(0, 100000, 0), "include_context": map[string]any{"type": "boolean", "default": false}, "context_before": num(0, 20, 5), "context_after": num(0, 20, 5)}
	input := map[string]any{"type": "object", "properties": props, "additionalProperties": false, "required": []string{}}
	object := func(properties map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	str := map[string]any{"type": "string"}
	boolean := map[string]any{"type": "boolean"}
	fields := []string{"id", "chat_jid", "chat_name", "sender", "sender_name", "timestamp", "is_from_me", "content", "caption", "media_type"}
	messageProps := func() map[string]any {
		return map[string]any{"id": str, "chat_jid": str, "chat_name": str, "sender": str, "sender_name": str, "timestamp": str, "is_from_me": boolean, "content": str, "caption": str, "media_type": map[string]any{"type": "string", "enum": []string{"", "image", "video", "audio", "document"}}}
	}
	message := object(messageProps(), fields)
	hitProps := messageProps()
	hitProps["before"] = map[string]any{"type": "array", "items": message}
	hitProps["after"] = map[string]any{"type": "array", "items": message}
	hitProps["before_reduced"], hitProps["after_reduced"] = boolean, boolean
	hitProps["preview_fields"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"content", "caption"}}}
	hit := object(hitProps, append(append([]string{}, fields...), "before", "after", "before_reduced", "after_reduced"))
	plain := object(map[string]any{"messages": map[string]any{"type": "array", "items": message}}, []string{"messages"})
	next := object(map[string]any{"page": map[string]any{"type": "integer", "minimum": 0, "maximum": 100000}, "limit": map[string]any{"const": 1, "type": "integer"}}, []string{"page", "limit"})
	contextPage := object(map[string]any{"messages": map[string]any{"type": "array", "items": hit}, "page_reduced": boolean, "next_page": next, "narrow_search": map[string]any{"type": "boolean", "const": true}}, []string{"messages", "page_reduced"})
	errorBody := object(map[string]any{"code": str, "message": str, "field": str}, []string{"code", "message"})
	failure := object(map[string]any{"error": errorBody}, []string{"error"})
	output := map[string]any{"type": "object", "oneOf": []any{plain, contextPage, failure}}
	return input, output
}

type messageArguments struct {
	After, Before                          time.Time
	ChatJID, Sender, Query, MessageID, JID string
	Limit, Page, Offset                    int
	IncludeContext                         bool
	ContextBefore, ContextAfter            int
}

// validateMessageArguments consumes the original object to catch duplicate keys.
func validateMessageArguments(tool string, raw json.RawMessage) (messageArguments, *argumentError) {
	a := messageArguments{}
	allowed := map[string]bool{}
	required := ""
	switch tool {
	case "list_messages":
		a.Limit, a.ContextBefore, a.ContextAfter = 50, 5, 5
		for _, k := range []string{"after", "before", "chat_jid", "sender_phone_number", "query", "limit", "page", "include_context", "context_before", "context_after"} {
			allowed[k] = true
		}
	case "get_message_context":
		a.ContextBefore, a.ContextAfter = 6, 6
		required = "message_id"
		for _, k := range []string{"message_id", "chat_jid", "before", "after"} {
			allowed[k] = true
		}
	case "get_last_interaction":
		required = "jid"
		allowed[required] = true
	default:
		return a, invalidArgument("arguments", "unsupported tool")
	}
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return a, invalidArgument("arguments", "must be a valid JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return a, invalidArgument("arguments", "must be a JSON object")
	}
	seen := map[string]bool{}
	for d.More() {
		tok, err = d.Token()
		if err != nil {
			return a, invalidArgument("arguments", "must be a valid JSON object")
		}
		key := tok.(string)
		if !allowed[key] {
			return a, invalidArgument("arguments", "contains an unknown field")
		}
		if seen[key] {
			return a, invalidArgument(key, "must not be repeated")
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return a, invalidArgument(key, "has an invalid value")
		}
		if key == "limit" || key == "page" || key == "context_before" || key == "context_after" || (tool == "get_message_context" && (key == "before" || key == "after")) {
			n, ok := exactNonnegativeInteger(string(value))
			max, min := 20, 0
			if key == "limit" {
				min, max = 1, 200
			}
			if key == "page" {
				max = 100000
			}
			if !ok || n < min || n > max {
				return a, invalidArgument(key, "is outside the allowed integer range")
			}
			switch key {
			case "limit":
				a.Limit = n
			case "page":
				a.Page = n
			case "context_before", "before":
				a.ContextBefore = n
			case "context_after", "after":
				a.ContextAfter = n
			}
			continue
		}
		if key == "include_context" {
			if string(value) != "true" && string(value) != "false" {
				return a, invalidArgument(key, "must be a boolean")
			}
			a.IncludeContext = string(value) == "true"
			continue
		}
		var s string
		if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &s) != nil || !utf8.ValidString(s) {
			return a, invalidArgument(key, "must be a string")
		}
		switch key {
		case "after", "before":
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil || t.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || !t.Before(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)) {
				return a, invalidArgument(key, "must be an RFC 3339 instant from 2000 inclusive to 2100 exclusive")
			}
			if key == "after" {
				a.After = t.UTC()
			} else {
				a.Before = t.UTC()
			}
		case "query":
			if len(s) > 1024 {
				return a, invalidArgument(key, "must be at most 1024 UTF-8 bytes")
			}
			a.Query = s
		case "message_id":
			if len(s) == 0 || len(s) > 256 || strings.IndexFunc(s, unicode.IsControl) >= 0 {
				return a, invalidArgument(key, "must be a valid nonempty ID of at most 256 UTF-8 bytes")
			}
			a.MessageID = s
		case "chat_jid":
			if !validIdentity(s, false) {
				return a, invalidArgument(key, "must be a supported chat JID")
			}
			a.ChatJID = s
		default:
			if !validIdentity(s, true) {
				return a, invalidArgument(key, "must be a supported contact identity")
			}
			if key == "jid" {
				a.JID = s
			} else {
				a.Sender = s
			}
		}
	}
	if _, err = d.Token(); err != nil {
		return a, invalidArgument("arguments", "must be a valid JSON object")
	}
	if _, err = d.Token(); err != io.EOF {
		return a, invalidArgument("arguments", "must be a valid JSON object")
	}
	if required != "" && !seen[required] {
		return a, invalidArgument(required, "is required")
	}
	if a.Limit > 0 && a.Page > 100000/a.Limit {
		return a, invalidArgument("page", "page times limit must not exceed 100000")
	}
	a.Offset = a.Page * a.Limit
	return a, nil
}
