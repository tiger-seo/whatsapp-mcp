package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

func sendText(recipient, message string) map[string]any {
	return map[string]any{"recipient": recipient, "message": message}
}

// wantNothingSent checks that the fake saw no upload, send or record.
func (r *sendRig) wantNothingSent() {
	r.t.Helper()
	calls := r.port.Calls()
	if len(calls.Uploads) != 0 || len(calls.Deliveries) != 0 || len(calls.Records) != 0 {
		r.t.Fatalf("%d uploads, %d sends, %d records; want none", len(calls.Uploads), len(calls.Deliveries), len(calls.Records))
	}
}

func TestSendToolsListed(t *testing.T) {
	client := newSendRig(t).client()
	list, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string][]string{"send_message": {"recipient", "message"}, "send_file": {"recipient", "media_path"}, "send_audio_message": {"recipient", "media_path"}}
	properties := map[string][]string{"send_message": {"recipient", "message"}, "send_file": {"recipient", "media_path", "caption"}, "send_audio_message": {"recipient", "media_path"}}
	found, reads := 0, 0
	for _, tool := range list.Tools {
		want, send := required[tool.Name]
		if !send {
			reads++
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Errorf("%s is no longer read-only", tool.Name)
			}
			continue
		}
		found++
		a := tool.Annotations
		if a == nil || a.ReadOnlyHint || a.IdempotentHint || a.DestructiveHint == nil || *a.DestructiveHint || a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("%s annotations = %+v", tool.Name, a)
		}
		for _, phrase := range []string{"owner's name", "send_allowed_recipients", "off until"} {
			if !strings.Contains(tool.Description, phrase) {
				t.Errorf("%s description lacks %q: %s", tool.Name, phrase, tool.Description)
			}
		}
		encoded, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Required             []string                  `json:"required"`
			Properties           map[string]map[string]any `json:"properties"`
			AdditionalProperties bool                      `json:"additionalProperties"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		if strings.Join(schema.Required, ",") != strings.Join(want, ",") || schema.AdditionalProperties || len(schema.Properties) != len(properties[tool.Name]) {
			t.Errorf("%s schema = %s", tool.Name, encoded)
		}
		for _, name := range properties[tool.Name] {
			p := schema.Properties[name]
			if p["type"] != "string" {
				t.Errorf("%s.%s = %v", tool.Name, name, p)
			}
			// Only a real default is listed.
			if def, ok := p["default"]; ok != (name == "caption") || (ok && def != "") {
				t.Errorf("%s.%s default = %v, %v", tool.Name, name, def, ok)
			}
		}
	}
	if found != 3 || reads != 8 {
		t.Errorf("send tools %d, read tools %d", found, reads)
	}
}

func TestSendMessageSuccess(t *testing.T) {
	r := newSendRig(t)
	client := r.client()
	reply := call(t, client, "send_message", sendText(sendPeer, "hello there"))
	if reply.isError {
		t.Fatalf("reply = %+v", reply)
	}
	s := reply.structured
	id, _ := s["id"].(string)
	want := map[string]any{"sent": true, "chat_jid": sendPeerJID, "id": id, "timestamp": "2026-11-27T12:00:00Z", "media_type": "",
		"voice_note": false, "local_record": "stored"}
	got, _ := json.Marshal(s)
	expected, _ := json.Marshal(want)
	if string(got) != string(expected) || id == "" {
		t.Errorf("structured = %s, want %s", got, expected)
	}
	for _, fact := range []string{sendPeerJID, id, "2026-11-27T12:00:00Z", "text message", "stored"} {
		if !strings.Contains(reply.text, fact) {
			t.Errorf("text %q lacks %q", reply.text, fact)
		}
	}
	calls := r.port.Calls()
	if len(calls.Deliveries) != 1 || calls.Deliveries[0].To.String() != sendPeerJID || calls.Deliveries[0].Message.GetConversation() != "hello there" {
		t.Fatalf("deliveries = %+v", calls.Deliveries)
	}
	// The read tools see what was sent.
	listed := call(t, client, "list_messages", map[string]any{"chat_jid": sendPeerJID, "include_context": false})
	encoded, _ := json.Marshal(listed.structured)
	for _, part := range []string{`"hello there"`, `"` + id + `"`, `"is_from_me":true`} {
		if !strings.Contains(string(encoded), part) {
			t.Errorf("history lacks %s: %s", part, encoded)
		}
	}

	r.port.SetFailure(wa.FakeRecord, wa.ErrRecordMissing)
	missing := call(t, client, "send_message", sendText(sendPeer, "not recorded"))
	if missing.isError || missing.structured["local_record"] != "missing" || !strings.Contains(missing.text, "record is missing") {
		t.Errorf("reply = %+v", missing)
	}
}

func TestSendMessageRecipients(t *testing.T) {
	for _, tt := range []struct{ recipient, to string }{
		{sendPeer, sendPeerJID},
		{"+49 151 100-00-01", sendPeerJID},
		{sendPeerJID, sendPeerJID},
		{sendPeerLID, sendPeerJID},
		{"100000000002@lid", "100000000002@lid"},
		{sendGroupJID, sendGroupJID},
		{sendOwnPhone, sendOwnPhone + "@s.whatsapp.net"},
	} {
		t.Run(tt.recipient, func(t *testing.T) {
			r := newSendRig(t)
			r.port.SetRegistration(sendOwnPhone, wa.Registration{Registered: true})
			reply := call(t, r.client(), "send_message", sendText(tt.recipient, "x"))
			if reply.isError || reply.structured["chat_jid"] != tt.to {
				t.Fatalf("reply = %+v", reply)
			}
			if got := r.port.Calls().Deliveries[0].To.String(); got != tt.to {
				t.Errorf("sent to %s", got)
			}
		})
	}
	r := newSendRig(t)
	client := r.client()
	for _, recipient := range []string{"status@broadcast", "1@broadcast", "1@newsletter", "s.whatsapp.net", "491511000001@c.us", "not a number", " "} {
		reply := call(t, client, "send_message", sendText(recipient, "x"))
		if reply.code() != "invalid_input" || reply.errorField("field") != "recipient" {
			t.Errorf("%q = %+v", recipient, reply)
		}
		if strings.Contains(reply.text, "newsletter@") || strings.Contains(reply.text, "491511000001") {
			t.Errorf("the refusal repeats the input: %s", reply.text)
		}
	}
	r.wantNothingSent()
}

func TestSendMessageArguments(t *testing.T) {
	r := newSendRig(t)
	client := r.client()
	for name, tt := range map[string]struct{ raw, field string }{
		"no arguments":       {`{}`, "recipient"},
		"no recipient":       {`{"message":"x"}`, "recipient"},
		"no message":         {`{"recipient":"491511000001"}`, "message"},
		"empty message":      {`{"recipient":"491511000001","message":""}`, "message"},
		"empty recipient":    {`{"recipient":"","message":"x"}`, "recipient"},
		"long recipient":     {`{"recipient":"` + strings.Repeat("1", 257) + `","message":"x"}`, "recipient"},
		"null recipient":     {`{"recipient":null,"message":"x"}`, "recipient"},
		"null message":       {`{"recipient":"491511000001","message":null}`, "message"},
		"number":             {`{"recipient":491511000001,"message":"x"}`, "recipient"},
		"array":              {`{"recipient":"491511000001","message":["x"]}`, "message"},
		"unknown":            {`{"recipient":"491511000001","message":"x","approved":true}`, "arguments"},
		"caption":            {`{"recipient":"491511000001","message":"x","caption":"y"}`, "arguments"},
		"duplicate":          {`{"recipient":"491511000001","message":"x","message":"y"}`, "message"},
		"escaped duplicate":  {`{"recipient":"491511000001","message":"x","message":"y"}`, "message"},
		"unpaired surrogate": {`{"recipient":"491511000001","message":"secret\ud800"}`, "message"},
		"low surrogate":      {`{"recipient":"491511000001","message":"secret\udc00x"}`, "message"},
	} {
		reply := call(t, client, "send_message", json.RawMessage(tt.raw))
		// A repeated name is refused by the guard before the handler sees it.
		if reply.code() != "invalid_input" && !strings.Contains(name, "duplicate") {
			t.Errorf("%s: %+v", name, reply)
			continue
		}
		if reply.code() == "invalid_input" && reply.errorField("field") != tt.field {
			t.Errorf("%s: field = %v, want %s", name, reply.errorField("field"), tt.field)
		}
		if strings.Contains(reply.text, "secret") {
			t.Errorf("%s: the refusal repeats the input", name)
		}
	}
	r.wantNothingSent()
	for name, message := range map[string]string{
		"one byte":            "x",
		"the limit":           strings.Repeat("a", 65536),
		"the limit, two-byte": strings.Repeat("é", 32768),
		"paired surrogates":   "ok 😀",
	} {
		if reply := call(t, client, "send_message", sendText(sendPeer, message)); reply.isError {
			t.Errorf("%s: %+v", name, reply.structured)
		}
	}
	sent := len(r.port.Calls().Deliveries)
	for name, message := range map[string]string{"one over": strings.Repeat("a", 65537), "one over, two-byte": strings.Repeat("é", 32768) + "a"} {
		reply := call(t, client, "send_message", sendText(sendPeer, message))
		if reply.code() != "invalid_input" || reply.errorField("field") != "message" {
			t.Errorf("%s: %+v", name, reply.structured)
		}
	}
	if len(r.port.Calls().Deliveries) != sent {
		t.Error("an oversized message was sent")
	}
}

// A message of the full size reaches the tool over stdio even when every byte is escaped to six; every other frame
// keeps the 64 KiB limit.
func TestSendFrameLimits(t *testing.T) {
	r := newSendRig(t)
	s := r.stdio()
	escaped := strings.Repeat("\u0001", 65536)
	s.call("big", "send_message", sendText(sendPeer, escaped))
	if reply := s.reply("big"); reply.isError {
		t.Fatalf("a message of 65536 escaped bytes = %+v", reply.structured)
	}
	if got := r.port.Calls().Deliveries; len(got) != 1 || got[0].Message.GetConversation() != escaped {
		t.Fatal("the message did not arrive whole")
	}
	s.alive()
	for name, frame := range map[string]string{
		"a read tool":      `{"jsonrpc":"2.0","id":"r","method":"tools/call","params":{"name":"list_chats","arguments":{"query":"` + strings.Repeat("a", 70000) + `"}}}`,
		"a ping":           `{"jsonrpc":"2.0","id":"r","method":"ping","params":{"x":"` + strings.Repeat("a", 70000) + `"}}`,
		"a send, too long": `{"jsonrpc":"2.0","id":"r","method":"tools/call","params":{"name":"send_message","arguments":{"message":"` + strings.Repeat("a", 520<<10) + `"}}}`,
		"padding a read":   `{"jsonrpc":"2.0","id":"r","method":"tools/call","params":{"name":"list_chats"}}` + strings.Repeat(" ", 70000),
	} {
		t.Run(name, func(t *testing.T) {
			s := newSendRig(t).stdio()
			s.t = t
			s.write(frame)
			select {
			case <-s.session.Stopped():
			case reply := <-s.replies:
				t.Fatalf("an oversized frame was answered: %v", reply)
			case <-time.After(10 * time.Second):
				t.Fatal("an oversized frame kept the session open")
			}
		})
	}
	// Through a transport without frames the guard applies the same limit to the parameters.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := newSendRig(t).client()
	_, err := client.CallTool(ctx, &sdk.CallToolParams{Name: "list_chats", Arguments: map[string]any{"query": strings.Repeat("a", 70000)}})
	if err == nil || !strings.Contains(err.Error(), "invalid parameters") {
		t.Errorf("an oversized read call = %v", err)
	}
}

func TestSendMessageRefusals(t *testing.T) {
	t.Run("sending is off by default", func(t *testing.T) {
		r := newSendRig(t, func(o *sendOptions) { o.recipients = nil })
		reply := call(t, r.client(), "send_message", sendText(sendPeer, "x"))
		if reply.code() != "not_permitted" || !strings.Contains(reply.text, "send_allowed_recipients") {
			t.Errorf("reply = %+v", reply)
		}
		r.wantNothingSent()
	})
	t.Run("not on the list", func(t *testing.T) {
		r := newSendRig(t, func(o *sendOptions) { o.recipients = []string{sendGroupJID} })
		if reply := call(t, r.client(), "send_message", sendText(sendPeer, "x")); reply.code() != "not_permitted" {
			t.Errorf("reply = %+v", reply)
		}
		r.wantNothingSent()
	})
	t.Run("rate limited", func(t *testing.T) {
		r := newSendRig(t)
		client := r.client()
		for range 10 {
			if reply := call(t, client, "send_message", sendText(sendPeer, "x")); reply.isError {
				t.Fatalf("reply = %+v", reply)
			}
		}
		reply := call(t, client, "send_message", sendText(sendPeer, "x"))
		retry, _ := reply.errorField("retry_after_seconds").(float64)
		if reply.code() != "rate_limited" || retry < 1 || retry > 60 || reply.errorField("limit") != "sends_per_minute" {
			t.Errorf("reply = %+v", reply)
		}
		if got := len(r.port.Calls().Deliveries); got != 10 {
			t.Errorf("sends = %d", got)
		}
	})
	t.Run("busy", func(t *testing.T) {
		r := newSendRig(t)
		slot, _, _, _ := r.limits.Acquire()
		defer slot.Release()
		if reply := call(t, r.client(), "send_message", sendText(sendPeer, "x")); reply.code() != "busy" {
			t.Errorf("reply = %+v", reply)
		}
		r.wantNothingSent()
	})
	t.Run("not connected", func(t *testing.T) {
		r := newSendRig(t)
		r.port.SetFailure(wa.FakeReady, wa.ErrNotConnected)
		if reply := call(t, r.client(), "send_message", sendText(sendPeer, "x")); reply.code() != "not_connected" {
			t.Errorf("reply = %+v", reply)
		}
		r.wantNothingSent()
	})
	t.Run("unregistered and failed lookup", func(t *testing.T) {
		r := newSendRig(t)
		client := r.client()
		if reply := call(t, client, "send_message", sendText("491511000002", "x")); reply.code() != "not_on_whatsapp" {
			t.Errorf("reply = %+v", reply)
		}
		r.port.SetFailure(wa.FakeLookup, errors.New("synthetic"))
		if reply := call(t, client, "send_message", sendText("491511000003", "x")); reply.code() != "lookup_failed" {
			t.Errorf("reply = %+v", reply)
		}
		r.wantNothingSent()
	})
}

func TestSendMessageFailures(t *testing.T) {
	for code, failure := range map[string]error{"send_failed": wa.ErrSendFailed, "outcome_unknown": wa.ErrOutcomeUnknown} {
		t.Run(code, func(t *testing.T) {
			r := newSendRig(t)
			r.port.SetFailure(wa.FakeDeliver, failure)
			client := r.client()
			reply := call(t, client, "send_message", sendText(sendPeer, "x"))
			if reply.code() != code {
				t.Fatalf("reply = %+v", reply)
			}
			if code == "outcome_unknown" && !strings.Contains(reply.text, "may have been delivered") {
				t.Errorf("text = %s", reply.text)
			}
			calls := r.port.Calls()
			if len(calls.Deliveries) != 1 || len(calls.Records) != 0 {
				t.Errorf("calls = %+v", calls)
			}
			// Every send handed over is counted: nine more fill the minute.
			for range 9 {
				if reply := call(t, client, "send_message", sendText(sendPeer, "x")); reply.code() != code {
					t.Fatalf("a later send = %+v", reply)
				}
			}
			if reply := call(t, client, "send_message", sendText(sendPeer, "x")); reply.code() != "rate_limited" {
				t.Errorf("the handed-over sends were not counted: %+v", reply)
			}
		})
	}
}
