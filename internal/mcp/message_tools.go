package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

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
