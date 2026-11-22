package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/types"
)

func readHandler(name string, reader ChatContactReader) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		raw := req.Params.Arguments
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		a, invalid := validateArguments(name, raw)
		if invalid != nil {
			return argumentErrorResult(invalid), nil
		}
		page := store.Page{Limit: a.Limit, Offset: a.Offset}
		switch name {
		case "get_chat":
			row, err := reader.GetChat(ctx, a.ChatJID, a.IncludeLastMessage)
			if err != nil {
				return chatReadError(err, "chat_jid"), nil
			}
			return encodeResult(ChatResult{Source: "local_store", UntrustedData: true, Chat: chatDTO(row, a.IncludeLastMessage)}), nil
		case "get_direct_chat_by_contact":
			row, err := reader.DirectChat(ctx, a.Contact)
			if err != nil {
				return chatReadError(err, "sender_phone_number"), nil
			}
			return encodeResult(ChatResult{Source: "local_store", UntrustedData: true, Chat: chatDTO(row, true)}), nil
		case "get_contact_chats":
			rows, err := reader.ContactChats(ctx, a.Contact, page)
			if err != nil {
				return chatReadError(err, "jid"), nil
			}
			return encodeResult(chatPageDTO(rows, a, true)), nil
		case "search_contacts":
			rows, err := reader.SearchContacts(ctx, a.Query, page)
			if err != nil {
				return chatReadError(err, "query"), nil
			}
			contacts := make([]Contact, len(rows))
			for i, row := range rows {
				contacts[i] = contactDTO(row)
			}
			return encodeResult(ContactPage{Source: "local_store", UntrustedData: true, Contacts: contacts, Count: len(contacts), Limit: a.Limit, Page: a.Page}), nil
		default: // list_chats; unsupported names fail validation above.
			order := store.ByLastActivity
			if a.SortBy == "name" {
				order = store.ByName
			}
			rows, err := reader.ListChats(ctx, a.Query, order, a.IncludeLastMessage, page)
			if err != nil {
				return chatReadError(err, "arguments"), nil
			}
			return encodeResult(chatPageDTO(rows, a, a.IncludeLastMessage)), nil
		}
	}
}

func chatReadError(err error, field string) *sdk.CallToolResult {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return toolErrorResult("timeout")
	case errors.Is(err, context.Canceled):
		return toolErrorResult("cancelled")
	case errors.Is(err, store.ErrInvalid):
		return argumentErrorResult(invalidArgument(field, "must satisfy the local store's input constraints"))
	case errors.Is(err, store.ErrNotFound):
		return toolErrorResult("not_found")
	case errors.Is(err, store.ErrBusy):
		return toolErrorResult("busy")
	case errors.Is(err, store.ErrDiskLow):
		return toolErrorResult("low_disk")
	case errors.Is(err, store.ErrClosed):
		return toolErrorResult("unavailable")
	case errors.Is(err, store.ErrResourceLimit):
		return toolErrorResult("resource_limit")
	default:
		return toolErrorResult("storage_error")
	}
}

type toolArguments struct {
	Query, ChatJID, Contact, SortBy string
	Limit, Page, Offset             int
	IncludeLastMessage              bool
}

type argumentError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func invalidArgument(field, message string) *argumentError {
	return &argumentError{Code: "invalid_input", Message: message, Field: field}
}

func argumentErrorResult(e *argumentError) *sdk.CallToolResult {
	root := ErrorResult{Error: ToolError{Code: e.Code, Message: e.Message, Field: e.Field}}
	text, _ := json.Marshal(root)
	return &sdk.CallToolResult{IsError: true, StructuredContent: root, Content: []sdk.Content{&sdk.TextContent{Text: string(text)}}}
}

func toolErrorResult(code string) *sdk.CallToolResult {
	messages := map[string]string{
		"not_found":      "Requested chat was not found",
		"busy":           "Server is busy; retry later",
		"resource_limit": "Result exceeds a limit; use a smaller page or disable latest message detail",
		"timeout":        "The request timed out",
		"cancelled":      "The request was cancelled",
		"unavailable":    "The local store is unavailable",
		"low_disk":       "low disk space",
		"storage_error":  "The local store could not complete the request",
	}
	message, ok := messages[code]
	if !ok {
		code, message = "storage_error", messages["storage_error"]
	} else if code == "low_disk" {
		code = "unavailable"
	}
	root := ErrorResult{Error: ToolError{Code: code, Message: message}}
	text, _ := json.Marshal(root)
	return &sdk.CallToolResult{IsError: true, StructuredContent: root, Content: []sdk.Content{&sdk.TextContent{Text: string(text)}}}
}

// validateArguments consumes the raw object so escaped duplicate names remain detectable.
func validateArguments(tool string, raw json.RawMessage) (toolArguments, *argumentError) {
	a := toolArguments{IncludeLastMessage: true, SortBy: "last_active"}
	allowed := map[string]bool{}
	var required string
	switch tool {
	case "list_chats":
		a.Limit = 30
		allowed = map[string]bool{"query": true, "limit": true, "page": true, "include_last_message": true, "sort_by": true}
	case "get_chat":
		required = "chat_jid"
		allowed = map[string]bool{"chat_jid": true, "include_last_message": true}
	case "search_contacts":
		a.Limit, required = 50, "query"
		allowed = map[string]bool{"query": true, "limit": true, "page": true}
	case "get_direct_chat_by_contact":
		required = "sender_phone_number"
		allowed = map[string]bool{required: true}
	case "get_contact_chats":
		a.Limit, required = 20, "jid"
		allowed = map[string]bool{"jid": true, "limit": true, "page": true}
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
		switch key {
		case "limit", "page":
			n, ok := exactNonnegativeInteger(string(value))
			if !ok || (key == "limit" && (n < 1 || n > 200)) || (key == "page" && n > 100000) {
				if key == "limit" {
					return a, invalidArgument(key, "must be an integer from 1 to 200")
				}
				return a, invalidArgument(key, "must be an integer from 0 to 100000")
			}
			if key == "limit" {
				a.Limit = int(n)
			} else {
				a.Page = int(n)
			}
		case "include_last_message":
			if string(value) != "true" && string(value) != "false" {
				return a, invalidArgument(key, "must be a boolean")
			}
			a.IncludeLastMessage = string(value) == "true"
		default:
			var s string
			if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &s) != nil {
				return a, invalidArgument(key, "must be a string")
			}
			switch key {
			case "query":
				if len(s) > 1024 {
					return a, invalidArgument(key, "must be at most 1024 UTF-8 bytes")
				}
				if key == required && strings.TrimSpace(s) == "" {
					return a, invalidArgument(key, "must not be blank")
				}
				a.Query = s
			case "sort_by":
				if s != "name" && s != "last_active" {
					return a, invalidArgument(key, "must be last_active or name")
				}
				a.SortBy = s
			case "chat_jid":
				if !validIdentity(s, false) {
					return a, invalidArgument(key, "must be a supported chat JID without a device suffix, at most 256 UTF-8 bytes")
				}
				a.ChatJID = s
			default:
				if !validIdentity(s, true) {
					return a, invalidArgument(key, "must be a supported contact JID or formatted phone with 1 to 20 digits, at most 256 UTF-8 bytes")
				}
				a.Contact = s
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

// exactNonnegativeInteger never expands an exponent or rounds a fractional value.
// The only accepted range is bounded by the largest page value.
func exactNonnegativeInteger(s string) (int, bool) {
	if s == "" || (s[0] != '-' && (s[0] < '0' || s[0] > '9')) {
		return 0, false
	}
	negative := s[0] == '-'
	if negative {
		s = s[1:]
	}
	mantissa, exponent, hasExponent := strings.Cut(strings.ToLower(s), "e")
	integer, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(integer+fraction, "0")
	if digits == "" {
		return 0, true
	}
	if negative {
		return 0, false
	}
	exp := int64(0)
	if hasExponent {
		var err error
		exp, err = strconv.ParseInt(exponent, 10, 64)
		if err != nil {
			return 0, false
		}
	}
	// Compare before addition to avoid overflow and avoid constructing large powers of ten.
	fractionLen := int64(len(fraction))
	trailing := int64(len(digits) - len(strings.TrimRight(digits, "0")))
	if exp < fractionLen-trailing || exp > fractionLen+6 {
		return 0, false
	}
	shift := exp - fractionLen
	if shift < 0 {
		digits = digits[:len(digits)+int(shift)]
	} else {
		digits += strings.Repeat("0", int(shift))
	}
	if len(digits) > 6 {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	return n, err == nil && n <= 100000
}

func validIdentity(s string, contact bool) bool {
	if len(s) == 0 || len(s) > 256 || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return false
	}
	if contact && !strings.Contains(s, "@") {
		_, err := store.NormalizePhone(s)
		return err == nil
	}
	j, err := types.ParseJID(s)
	if err != nil || j.User == "" || strings.TrimSpace(j.User) != j.User {
		return false
	}
	if j.Server != types.DefaultUserServer && j.Server != types.HiddenUserServer && (contact || j.Server != types.GroupServer) {
		return false
	}
	if !contact && (j.Device != 0 || j.RawAgent != 0 || strings.ContainsAny(strings.SplitN(s, "@", 2)[0], ":.")) {
		return false
	}
	if contact && j.Server == types.DefaultUserServer {
		_, err = store.NormalizePhone(j.User)
		return err == nil
	}
	return true
}

func toolSchemas(tool string) (map[string]any, map[string]any) {
	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	props := map[string]any{}
	required := []string{}
	list := func(limit int) {
		props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": limit}
		props["page"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 100000, "default": 0}
	}
	latest := func() { props["include_last_message"] = map[string]any{"type": "boolean", "default": true} }
	switch tool {
	case "list_chats":
		q := str(1024)
		q["default"] = ""
		props["query"] = q
		list(30)
		latest()
		props["sort_by"] = map[string]any{"type": "string", "enum": []string{"last_active", "name"}, "default": "last_active"}
	case "get_chat":
		props["chat_jid"] = str(256)
		required = append(required, "chat_jid")
		latest()
	case "search_contacts":
		props["query"] = str(1024)
		required = append(required, "query")
		list(50)
	case "get_direct_chat_by_contact", "get_contact_chats":
		field := "jid"
		if tool == "get_direct_chat_by_contact" {
			field = "sender_phone_number"
		} else {
			list(20)
		}
		p := str(256)
		p["description"] = "Formatted phone number, phone JID or LID"
		props[field] = p
		required = append(required, field)
	default:
		return nil, nil
	}
	for _, field := range required {
		props[field].(map[string]any)["minLength"] = 1
	}
	input := map[string]any{"type": "object", "properties": props, "additionalProperties": false, "required": required}
	object := func(properties map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	text := map[string]any{"type": "string"}
	nullableText := map[string]any{"type": []string{"string", "null"}}
	message := object(map[string]any{"id": text, "chat_jid": text, "chat_name": text, "sender": text, "sender_name": text, "timestamp": text, "is_from_me": map[string]any{"type": "boolean"}, "content": text, "caption": text, "media_type": map[string]any{"type": "string", "enum": []string{"", "image", "video", "audio", "document"}}}, []string{"id", "chat_jid", "chat_name", "sender", "sender_name", "timestamp", "is_from_me", "content", "caption", "media_type"})
	chat := object(map[string]any{"jid": text, "name": text, "is_group": map[string]any{"type": "boolean"}, "last_message_time": nullableText, "latest_message": map[string]any{"anyOf": []any{message, map[string]any{"type": "null"}}}}, []string{"jid", "name", "is_group", "last_message_time"})
	if tool == "get_direct_chat_by_contact" || tool == "get_contact_chats" {
		chat["required"] = append(chat["required"].([]string), "latest_message")
	}
	contact := object(map[string]any{"jid": text, "phone_number": nullableText, "name": text}, []string{"jid", "phone_number", "name"})
	successProps := map[string]any{"source": map[string]any{"const": "local_store", "type": "string"}, "untrusted_data": map[string]any{"const": true, "type": "boolean"}}
	successRequired := []string{"source", "untrusted_data"}
	if tool == "get_chat" || tool == "get_direct_chat_by_contact" {
		successProps["chat"] = chat
		successRequired = append(successRequired, "chat")
	} else {
		field, item := "chats", chat
		if tool == "search_contacts" {
			field, item = "contacts", contact
		}
		successProps[field] = map[string]any{"type": "array", "items": item}
		successRequired = append(successRequired, field, "count", "limit", "page")
		for _, k := range []string{"count", "limit", "page"} {
			successProps[k] = map[string]any{"type": "integer"}
		}
	}
	errorBody := object(map[string]any{"code": text, "message": text, "field": text}, []string{"code", "message"})
	output := map[string]any{"type": "object", "oneOf": []any{object(successProps, successRequired), object(map[string]any{"error": errorBody}, []string{"error"})}}
	return input, output
}
