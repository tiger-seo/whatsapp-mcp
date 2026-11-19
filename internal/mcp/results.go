package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// ChatContactReader exposes only the local reads needed by the chat and contact tools.
type ChatContactReader interface {
	ListChats(context.Context, string, store.ChatSort, bool, store.Page) ([]store.ChatView, error)
	GetChat(context.Context, string, bool) (store.ChatView, error)
	SearchContacts(context.Context, string, store.Page) ([]store.Contact, error)
	DirectChat(context.Context, string) (store.ChatView, error)
	ContactChats(context.Context, string, store.Page) ([]store.ChatView, error)
}

type Chat struct {
	JID             string    `json:"jid"`
	Name            string    `json:"name"`
	IsGroup         bool      `json:"is_group"`
	LastMessageTime *string   `json:"last_message_time"`
	LatestMessage   **Message `json:"latest_message,omitempty"`
}

type Message struct {
	ID         string `json:"id"`
	ChatJID    string `json:"chat_jid"`
	ChatName   string `json:"chat_name"`
	Sender     string `json:"sender"`
	SenderName string `json:"sender_name"`
	Timestamp  string `json:"timestamp"`
	IsFromMe   bool   `json:"is_from_me"`
	Content    string `json:"content"`
	Caption    string `json:"caption"`
	MediaType  string `json:"media_type"`
}

type Contact struct {
	JID         string  `json:"jid"`
	PhoneNumber *string `json:"phone_number"`
	Name        string  `json:"name"`
}

type ChatPage struct {
	Source        string `json:"source"`
	UntrustedData bool   `json:"untrusted_data"`
	Chats         []Chat `json:"chats"`
	Count         int    `json:"count"`
	Limit         int    `json:"limit"`
	Page          int    `json:"page"`
}

type ContactPage struct {
	Source        string    `json:"source"`
	UntrustedData bool      `json:"untrusted_data"`
	Contacts      []Contact `json:"contacts"`
	Count         int       `json:"count"`
	Limit         int       `json:"limit"`
	Page          int       `json:"page"`
}

type ChatResult struct {
	Source        string `json:"source"`
	UntrustedData bool   `json:"untrusted_data"`
	Chat          Chat   `json:"chat"`
}

type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

type ErrorResult struct {
	Error ToolError `json:"error"`
}

const (
	resultBudget      = 2 << 20
	frameBudget       = 8 << 20
	storeResultBudget = 32 << 20
)

// MarshalJSON makes the zero-value page's collection an empty array, not null.
func (p ChatPage) MarshalJSON() ([]byte, error) {
	type page ChatPage
	if p.Chats == nil {
		p.Chats = []Chat{}
	}
	return json.Marshal(page(p))
}

func (p ContactPage) MarshalJSON() ([]byte, error) {
	type page ContactPage
	if p.Contacts == nil {
		p.Contacts = []Contact{}
	}
	return json.Marshal(page(p))
}

type resultCharge struct {
	structured, text, store uint64
}

func resultSizesFit(structured, text uint64) bool {
	return structured <= resultBudget && text <= resultBudget
}

func resultFrameFits(structured, text uint64) bool {
	if !resultSizesFit(structured, text) {
		return false
	}
	frame := structured
	return addCharge(&frame, text) && addCharge(&frame, text) && addCharge(&frame, 1024) && frame <= frameBudget
}

func addCharge(n *uint64, v uint64) bool {
	if ^uint64(0)-*n < v {
		return false
	}
	*n += v
	return true
}

// chargeResult walks only the public DTOs, before any result-sized encoding.
// Fixed structural allowances intentionally overestimate the actual JSON.
func chargeResult(v reflect.Value, field string, c *resultCharge) bool {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return addCharge(&c.structured, 8) && addCharge(&c.text, 8)
		}
		return chargeResult(v.Elem(), field, c)
	}
	switch v.Kind() {
	case reflect.Struct:
		if !addCharge(&c.structured, 128) || !addCharge(&c.text, 128) {
			return false
		}
		for i := 0; i < v.NumField(); i++ {
			if !chargeResult(v.Field(i), v.Type().Field(i).Name, c) {
				return false
			}
		}
	case reflect.Slice:
		if v.Len() > 200 || !addCharge(&c.structured, uint64(v.Len())+8) || !addCharge(&c.text, uint64(v.Len())+8) {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if !chargeResult(v.Index(i), field, c) {
				return false
			}
		}
	case reflect.String:
		s := v.String()
		max := 1024
		switch field {
		case "JID", "ID", "ChatJID", "Sender", "PhoneNumber":
			max = 256
		case "Content", "Caption":
			max = 256 << 10
		}
		if len(s) > max || !utf8.ValidString(s) || !addCharge(&c.store, uint64(len(s))) || c.store > storeResultBudget {
			return false
		}
		if !addCharge(&c.structured, 2) || !addCharge(&c.text, 2) {
			return false
		}
		for _, r := range s {
			a, b := uint64(1), uint64(1)
			switch {
			case r < 0x20 || r == '<' || r == '>' || r == '&':
				a, b = 6, 6
			case r == '"' || r == '\\':
				a, b = 2, 2
			case r == 0x2028 || r == 0x2029:
				a, b = 6, 6
			case r >= 0x80:
				a, b = uint64(utf8.RuneLen(r)), 6
				if r > 0xffff {
					b = 12
				}
			case r == 0x7f:
				b = 6
			}
			if !addCharge(&c.structured, a) || !addCharge(&c.text, b) || c.structured > resultBudget || c.text > resultBudget {
				return false
			}
		}
	default:
		return addCharge(&c.structured, 24) && addCharge(&c.text, 24)
	}
	return c.structured <= resultBudget && c.text <= resultBudget
}

func validResultTimestamp(s string) bool {
	if len(s) > 64 || !strings.HasSuffix(s, "Z") {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}

func resultTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func validResultChat(c Chat) bool {
	if c.LastMessageTime != nil && !validResultTimestamp(*c.LastMessageTime) {
		return false
	}
	if c.LatestMessage == nil || *c.LatestMessage == nil {
		return true
	}
	m := *c.LatestMessage
	if m.ChatJID != c.JID || !validResultTimestamp(m.Timestamp) {
		return false
	}
	switch m.MediaType {
	case "", "image", "video", "audio", "document":
		return true
	default:
		return false
	}
}

func validResultContact(c Contact) bool {
	if c.PhoneNumber == nil {
		return true
	}
	if len(*c.PhoneNumber) == 0 || len(*c.PhoneNumber) > 20 {
		return false
	}
	for _, b := range *c.PhoneNumber {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

func validResult(value any) bool {
	switch v := value.(type) {
	case ChatPage:
		if v.Source != "local_store" || !v.UntrustedData || v.Limit < 1 || v.Limit > 200 || v.Page < 0 || v.Page > 100000 || v.Count != len(v.Chats) || v.Count > v.Limit {
			return false
		}
		for _, chat := range v.Chats {
			if !validResultChat(chat) {
				return false
			}
		}
		return true
	case ContactPage:
		if v.Source != "local_store" || !v.UntrustedData || v.Limit < 1 || v.Limit > 200 || v.Page < 0 || v.Page > 100000 || v.Count != len(v.Contacts) || v.Count > v.Limit {
			return false
		}
		for _, contact := range v.Contacts {
			if !validResultContact(contact) {
				return false
			}
		}
		return true
	case ChatResult:
		return v.Source == "local_store" && v.UntrustedData && validResultChat(v.Chat)
	case ErrorResult:
		return v.Error.Code != "" && v.Error.Message != ""
	}
	return false
}

// encodeResult accepts only public result DTOs. A failure never returns partial rows.
func encodeResult(value any) *sdk.CallToolResult {
	resourceLimit := func() *sdk.CallToolResult {
		const text = `{"error":{"code":"resource_limit","message":"Result exceeds a limit; use a smaller page or disable latest message detail"}}`
		return &sdk.CallToolResult{IsError: true, StructuredContent: json.RawMessage(text), Content: []sdk.Content{&sdk.TextContent{Text: text}}}
	}
	switch v := value.(type) {
	case ChatPage:
		if v.Count != len(v.Chats) {
			return resourceLimit()
		}
	case ContactPage:
		if v.Count != len(v.Contacts) {
			return resourceLimit()
		}
	case ChatResult, ErrorResult:
	default:
		return resourceLimit()
	}
	var c resultCharge
	if !chargeResult(reflect.ValueOf(value), "", &c) || !validResult(value) || !resultFrameFits(c.structured, c.text) {
		return resourceLimit()
	}
	// Encode the normalized page directly, avoiding its nested MarshalJSON copy.
	switch p := value.(type) {
	case ChatPage:
		type page ChatPage
		if p.Chats == nil {
			p.Chats = []Chat{}
		}
		value = page(p)
	case ContactPage:
		type page ContactPage
		if p.Contacts == nil {
			p.Contacts = []Contact{}
		}
		value = page(p)
	}
	structured, err := json.Marshal(value)
	if err != nil || uint64(len(structured)) > c.structured {
		return resourceLimit()
	}
	text := safeResultText(structured, int(c.text))
	if text == "" || !resultFrameFits(uint64(len(structured)), uint64(len(text))) {
		return resourceLimit()
	}
	_, isError := value.(ErrorResult)
	return &sdk.CallToolResult{IsError: isError, StructuredContent: json.RawMessage(structured), Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// safeResultText expects compact JSON from json.Marshal, which already escapes
// C0 and HTML characters. Size the ASCII rendering before allocating it.
func safeResultText(structured []byte, limit int) string {
	if limit < 0 || limit > resultBudget || len(structured) > limit {
		return ""
	}
	size := 0
	for remaining := structured; len(remaining) > 0; {
		r, n := utf8.DecodeRune(remaining)
		switch {
		case r > 0xffff:
			size += 12
		case r >= 0x7f:
			size += 6
		default:
			size += n
		}
		if size > limit {
			return ""
		}
		remaining = remaining[n:]
	}
	var text strings.Builder
	text.Grow(size)
	for remaining := structured; len(remaining) > 0; {
		r, n := utf8.DecodeRune(remaining)
		if r == 0x7f || r >= 0x80 {
			appendUnit := func(u rune) {
				const hex = "0123456789abcdef"
				text.WriteString(`\u`)
				text.WriteByte(hex[(u>>12)&15])
				text.WriteByte(hex[(u>>8)&15])
				text.WriteByte(hex[(u>>4)&15])
				text.WriteByte(hex[u&15])
			}
			if r > 0xffff {
				r -= 0x10000
				appendUnit(0xd800 + r>>10)
				appendUnit(0xdc00 + r&0x3ff)
			} else {
				appendUnit(r)
			}
		} else {
			text.WriteByte(remaining[0])
		}
		remaining = remaining[n:]
	}
	return text.String()
}
