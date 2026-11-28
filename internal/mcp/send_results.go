package mcp

import (
	"encoding/json"
	"strconv"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/media/sendapi"
)

// SendResult is the structured result of a send that WhatsApp acknowledged.
type SendResult struct {
	Sent      bool   `json:"sent"`
	ChatJID   string `json:"chat_jid"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	MediaType string `json:"media_type"`
	VoiceNote bool   `json:"voice_note"`
	// DurationSeconds is set for a voice note only.
	DurationSeconds int    `json:"duration_seconds,omitempty"`
	LocalRecord     string `json:"local_record"`
}

// SendError is the body of a send's tool error.
type SendError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
	// RetryAfterSeconds and Limit come with rate_limited.
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	Limit             string `json:"limit,omitempty"`
	// ChatJID, ID, Timestamp and LocalRecord come with sent_cancelled: the message went out.
	ChatJID     string `json:"chat_jid,omitempty"`
	ID          string `json:"id,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
	LocalRecord string `json:"local_record,omitempty"`
}

// SendErrorResult is the root of a send's tool error.
type SendErrorResult struct {
	Error SendError `json:"error"`
}

func localRecord(recorded bool) string {
	if recorded {
		return "stored"
	}
	return "missing"
}

func sendTimestamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// sendSuccess renders a success as structured content and as one sentence with the same facts.
func sendSuccess(res sendapi.Result) *sdk.CallToolResult {
	root := SendResult{Sent: true, ChatJID: res.ChatJID, ID: res.ID, Timestamp: sendTimestamp(res.Time), MediaType: res.MediaType,
		VoiceNote: res.VoiceNote, LocalRecord: localRecord(res.Recorded)}
	what := "a text message"
	switch {
	case res.VoiceNote:
		root.DurationSeconds = res.Seconds
		what = "a voice note of " + strconv.Itoa(res.Seconds) + " seconds"
	case res.MediaType != "":
		what = "a message of type " + res.MediaType
	}
	record := "it is stored in the local history"
	if !res.Recorded {
		record = "the local record is missing"
	}
	text := "Sent " + what + " to " + root.ChatJID + " with id " + root.ID + " at " + root.Timestamp + "; " + record + "."
	return &sdk.CallToolResult{StructuredContent: root, Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// sendFailure renders a send's tool error; the text is the same object.
func sendFailure(err *sendapi.Error) *sdk.CallToolResult {
	root := SendErrorResult{Error: SendError{Code: string(err.Code), Message: err.Message, Field: err.Field,
		RetryAfterSeconds: err.RetryAfterSeconds, Limit: err.Limit}}
	if sent := err.Sent; sent != nil {
		root.Error.ChatJID, root.Error.ID = sent.ChatJID, sent.ID
		root.Error.Timestamp, root.Error.LocalRecord = sendTimestamp(sent.Time), localRecord(sent.Recorded)
	}
	text, _ := json.Marshal(root)
	return &sdk.CallToolResult{IsError: true, StructuredContent: root, Content: []sdk.Content{&sdk.TextContent{Text: string(text)}}}
}

// sendOutputSchema describes both shapes a send tool returns.
func sendOutputSchema() map[string]any {
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	text, number := map[string]any{"type": "string"}, map[string]any{"type": "integer"}
	record := map[string]any{"type": "string", "enum": []string{"stored", "missing"}}
	success := object(map[string]any{
		"sent": map[string]any{"type": "boolean", "const": true}, "chat_jid": text, "id": text, "timestamp": text,
		"media_type":       map[string]any{"type": "string", "enum": []string{"", "image", "video", "audio", "document"}},
		"voice_note":       map[string]any{"type": "boolean"},
		"duration_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 300},
		"local_record":     record,
	}, "sent", "chat_jid", "id", "timestamp", "media_type", "voice_note", "local_record")
	codes := make([]string, 0, len(sendapi.Codes()))
	for _, code := range sendapi.Codes() {
		codes = append(codes, string(code))
	}
	body := object(map[string]any{
		"code": map[string]any{"type": "string", "enum": codes}, "message": text, "field": text,
		"retry_after_seconds": number, "limit": text,
		"chat_jid": text, "id": text, "timestamp": text, "local_record": record,
	}, "code", "message")
	return map[string]any{"type": "object", "oneOf": []any{success, object(map[string]any{"error": body}, "error")}}
}
