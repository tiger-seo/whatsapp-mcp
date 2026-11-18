package mcp

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResultEmptyPageArray(t *testing.T) {
	b, err := json.Marshal(ChatPage{Source: "local_store", UntrustedData: true, Limit: 30})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	if _, ok := result["chats"].([]any); !ok {
		t.Fatalf("chats must be an empty array, got %s", b)
	}
}

func TestResultSafeTextAndOptionalFields(t *testing.T) {
	stamp := "2026-11-18T00:20:46.123456789Z"
	msg := &Message{ID: "id", Timestamp: stamp, Content: "\x00\x1b\x7f\u0085\u202eé😀\\\"<>&"}
	page := ChatPage{Source: "local_store", UntrustedData: true, Limit: 3, Count: 3, Chats: []Chat{
		{}, {LatestMessage: new(*Message)}, {LastMessageTime: &stamp, LatestMessage: &msg},
	}}
	r := encodeResult(page)
	if r.IsError {
		t.Fatal("unexpected resource limit")
	}
	structured := r.StructuredContent.(json.RawMessage)
	text := r.Content[0].(*sdk.TextContent).Text
	for _, b := range []byte(text) {
		if b < 0x20 || b >= 0x7f {
			t.Fatalf("unsafe byte %x", b)
		}
	}
	var a, b any
	if json.Unmarshal(structured, &a) != nil || json.Unmarshal([]byte(text), &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("representations differ: %s / %s", structured, text)
	}
	chats := a.(map[string]any)["chats"].([]any)
	if _, ok := chats[0].(map[string]any)["latest_message"]; ok {
		t.Fatal("unrequested latest included")
	}
	if v, ok := chats[1].(map[string]any)["latest_message"]; !ok || v != nil {
		t.Fatal("missing requested null latest")
	}
	if !bytes.Contains(structured, []byte(stamp)) {
		t.Fatal("fractional timestamp lost")
	}
	if got := chats[2].(map[string]any)["latest_message"].(map[string]any)["content"]; got != msg.Content {
		t.Fatalf("content changed: %q", got)
	}
}

func TestResultRefusesBeforeEncoding(t *testing.T) {
	for _, content := range []string{string([]byte{0xff}), strings.Repeat("x", 256<<10+1)} {
		m := &Message{Timestamp: "2026-11-18T00:00:00Z", Content: content}
		r := encodeResult(ChatResult{Source: "local_store", UntrustedData: true, Chat: Chat{LatestMessage: &m}})
		if !r.IsError || !bytes.Contains(r.StructuredContent.(json.RawMessage), []byte(`"resource_limit"`)) {
			t.Fatal("invalid field accepted")
		}
	}
	var n uint64 = math.MaxUint64
	if addCharge(&n, 1) {
		t.Fatal("overflow accepted")
	}
}

func TestResultTextBudget(t *testing.T) {
	chats := make([]Chat, 2)
	for i := range chats {
		m := &Message{Timestamp: "2026-11-18T00:00:00Z", Content: strings.Repeat("\x01", 256<<10)}
		chats[i].LatestMessage = &m
	}
	r := encodeResult(ChatPage{Source: "local_store", UntrustedData: true, Limit: 2, Chats: chats, Count: len(chats)})
	if !r.IsError {
		t.Fatal("over-budget text accepted")
	}
}

func TestResultSDKEncodingAndCharges(t *testing.T) {
	m := &Message{ID: "id", ChatJID: "jid", ChatName: "name", Sender: "sender", SenderName: "sender name", Timestamp: "2026-11-18T00:00:00.123Z", Content: "x\x00\u0085😀", Caption: "caption", MediaType: "image"}
	p := ChatPage{Source: "local_store", UntrustedData: true, Count: 1, Limit: 1, Chats: []Chat{{JID: "jid", LatestMessage: &m}}}
	var charge resultCharge
	if !chargeResult(reflect.ValueOf(p), "", &charge) {
		t.Fatal("charge failed")
	}
	r := encodeResult(p)
	if r.IsError {
		t.Fatal("result rejected")
	}
	structured := r.StructuredContent.(json.RawMessage)
	text := r.Content[0].(*sdk.TextContent).Text
	if uint64(len(structured)) > charge.structured || uint64(len(text)) > charge.text {
		t.Fatalf("undercharged structured %d/%d text %d/%d", len(structured), charge.structured, len(text), charge.text)
	}
	wire, err := json.Marshal(struct {
		JSONRPC string              `json:"jsonrpc"`
		ID      string              `json:"id"`
		Result  *sdk.CallToolResult `json:"result"`
	}{"2.0", strings.Repeat("x", 128), r})
	if err != nil {
		t.Fatal(err)
	}
	if len(structured) > resultBudget || len(text) > resultBudget || len(wire)+1 > frameBudget || uint64(len(wire)+1) > charge.structured+2*charge.text+1024 {
		t.Fatalf("wire budget exceeded: %d", len(wire)+1)
	}
}

func TestResultHTMLMetacharacterCharges(t *testing.T) {
	for _, char := range []string{"<", ">", "&"} {
		t.Run(char, func(t *testing.T) {
			message := &Message{Timestamp: "2026-11-18T00:00:00Z", Content: strings.Repeat(char, 256<<10)}
			page := ChatPage{Source: "local_store", UntrustedData: true, Limit: 1, Count: 1, Chats: []Chat{{LatestMessage: &message}}}
			var charge resultCharge
			if !chargeResult(reflect.ValueOf(page), "", &charge) {
				t.Fatal("bounded fixture rejected")
			}
			encoded, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			text := safeResultText(encoded, 0)
			if uint64(len(encoded)) > charge.structured || uint64(len(text)) > charge.text {
				t.Fatalf("undercharged: structured %d/%d, text %d/%d", len(encoded), charge.structured, len(text), charge.text)
			}
			message.Caption = strings.Repeat(char, 256<<10)
			charge = resultCharge{}
			if chargeResult(reflect.ValueOf(page), "", &charge) {
				t.Fatal("over-budget HTML-escaped result passed preflight")
			}
			if !encodeResult(page).IsError {
				t.Fatal("over-budget result accepted")
			}
		})
	}
}

func TestResultSDKEncodingEscapedBudget(t *testing.T) {
	chats := make([]Chat, 2)
	for i := range chats {
		jid := string(rune('a'+i)) + "@s.whatsapp.net"
		message := &Message{ChatJID: jid, Timestamp: "2026-11-18T00:00:00Z", Content: strings.Repeat("\x01\\\"", 40000)}
		chats[i] = Chat{JID: jid, LatestMessage: &message}
	}
	page := ChatPage{Source: "local_store", UntrustedData: true, Count: len(chats), Limit: len(chats), Chats: chats}
	var charge resultCharge
	if !chargeResult(reflect.ValueOf(page), "", &charge) {
		t.Fatal("escaped fixture exceeds preflight")
	}
	result := encodeResult(page)
	if result.IsError {
		t.Fatal("escaped fixture was rejected")
	}
	structured := result.StructuredContent.(json.RawMessage)
	text := result.Content[0].(*sdk.TextContent).Text
	wire, err := json.Marshal(struct {
		JSONRPC string              `json:"jsonrpc"`
		ID      string              `json:"id"`
		Result  *sdk.CallToolResult `json:"result"`
	}{"2.0", strings.Repeat("x", 128), result})
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(structured)) > charge.structured || uint64(len(text)) > charge.text ||
		uint64(len(wire)+1) > charge.structured+2*charge.text+1024 || len(wire)+1 > frameBudget {
		t.Fatalf("escaped result undercharged: structured %d/%d, text %d/%d, wire %d", len(structured), charge.structured, len(text), charge.text, len(wire)+1)
	}
}

func TestResultContactNullAndEmptyArray(t *testing.T) {
	r := encodeResult(ContactPage{Source: "local_store", UntrustedData: true, Limit: 1})
	if r.IsError || !bytes.Contains(r.StructuredContent.(json.RawMessage), []byte(`"contacts":[]`)) {
		t.Fatal("empty contacts missing")
	}
	r = encodeResult(ContactPage{Source: "local_store", UntrustedData: true, Limit: 1, Count: 1, Contacts: []Contact{{}}})
	if r.IsError || !bytes.Contains(r.StructuredContent.(json.RawMessage), []byte(`"phone_number":null`)) {
		t.Fatal("null phone missing")
	}
}

func TestResultPreflightAllocation(t *testing.T) {
	large := strings.Repeat("\x01", 256<<10)
	chats := make([]Chat, 2)
	for i := range chats {
		m := &Message{Content: large}
		chats[i].LatestMessage = &m
	}
	p := ChatPage{Chats: chats, Count: 2}
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if !encodeResult(p).IsError {
				b.Fatal("expected limit")
			}
		}
	})
	if got := result.AllocedBytesPerOp(); got > 64<<10 {
		t.Fatalf("rejected result allocated %d bytes/op", got)
	}
	t.Logf("rejected preflight: %d bytes/op, %d allocs/op", result.AllocedBytesPerOp(), result.AllocsPerOp())
}

func TestResultSemanticValidation(t *testing.T) {
	valid := ChatResult{Source: "local_store", UntrustedData: true, Chat: Chat{JID: "123@s.whatsapp.net"}}
	for name, result := range map[string]ChatResult{
		"provenance":        {Source: "wrong", UntrustedData: true, Chat: valid.Chat},
		"trust marker":      {Source: "local_store", Chat: valid.Chat},
		"invalid timestamp": {Source: "local_store", UntrustedData: true, Chat: Chat{LastMessageTime: ptrString("not a date")}},
		"non UTC timestamp": {Source: "local_store", UntrustedData: true, Chat: Chat{LastMessageTime: ptrString("2026-11-18T02:20:46+02:00")}},
	} {
		t.Run(name, func(t *testing.T) {
			if !encodeResult(result).IsError {
				t.Fatal("invalid result accepted")
			}
		})
	}
	msg := &Message{ChatJID: valid.Chat.JID, Timestamp: "2026-11-18T00:20:46.123456789Z", MediaType: "application/unknown"}
	valid.Chat.LatestMessage = &msg
	if !encodeResult(valid).IsError {
		t.Fatal("unsupported media type accepted")
	}
	msg.MediaType = "image"
	if encodeResult(valid).IsError {
		t.Fatal("valid precise UTC timestamp rejected")
	}
	msg.ChatJID = "different-chat"
	if !encodeResult(valid).IsError {
		t.Fatal("message from another chat accepted")
	}
	if !encodeResult(ContactPage{Source: "local_store", UntrustedData: true, Limit: 1, Count: 1, Contacts: []Contact{{PhoneNumber: ptrString("+123")}}}).IsError {
		t.Fatal("formatted phone accepted")
	}
}

func ptrString(s string) *string { return &s }

func TestResultTimestampUTC(t *testing.T) {
	tm := time.Date(2026, 11, 18, 2, 20, 46, 123456789, time.FixedZone("plus2", 2*60*60))
	if got := resultTimestamp(tm); got != "2026-11-18T00:20:46.123456789Z" {
		t.Fatalf("timestamp: %q", got)
	}
}

func TestResultSizeLimits(t *testing.T) {
	for _, tc := range []struct {
		structured, text uint64
		want             bool
	}{
		{resultBudget - 1, resultBudget - 1, true},
		{resultBudget, resultBudget, true},
		{resultBudget + 1, resultBudget, false},
		{resultBudget, resultBudget + 1, false},
		{math.MaxUint64, 1, false},
		{1, math.MaxUint64, false},
	} {
		if got := resultSizesFit(tc.structured, tc.text); got != tc.want {
			t.Fatalf("sizes %d/%d: %v", tc.structured, tc.text, got)
		}
		if got := resultFrameFits(tc.structured, tc.text); got != tc.want {
			t.Fatalf("frame charge %d/%d: %v", tc.structured, tc.text, got)
		}
	}
}

func TestResultActualTwoMiBBoundary(t *testing.T) {
	for _, target := range []int{resultBudget - 1, resultBudget, resultBudget + 1} {
		page := ChatPage{Source: "local_store", UntrustedData: true, Limit: 9, Count: 9, Chats: make([]Chat, 9)}
		for i := range page.Chats {
			m := &Message{Timestamp: "2026-11-18T00:00:00Z"}
			page.Chats[i].LatestMessage = &m
		}
		base, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		remaining := target - len(base)
		for i := range page.Chats {
			m := *page.Chats[i].LatestMessage
			for _, field := range []*string{&m.Content, &m.Caption} {
				n := min(remaining, 256<<10)
				*field = strings.Repeat("x", n)
				remaining -= n
			}
		}
		if remaining != 0 {
			t.Fatal("fixture cannot reach target")
		}
		actual, err := json.Marshal(page)
		if err != nil || len(actual) != target {
			t.Fatalf("encoded %d, target %d, err %v", len(actual), target, err)
		}
		if got := resultSizesFit(uint64(len(actual)), uint64(len(actual))); got != (target <= resultBudget) {
			t.Fatalf("actual size %d: %v", target, got)
		}
		if target > resultBudget && !encodeResult(page).IsError {
			t.Fatal("oversized result accepted")
		}
	}
}

func TestResultActualTextTwoMiBBoundary(t *testing.T) {
	for _, target := range []int{resultBudget - 1, resultBudget, resultBudget + 1} {
		page := ChatPage{Source: "local_store", UntrustedData: true, Limit: 9, Count: 9, Chats: make([]Chat, 9)}
		for i := range page.Chats {
			m := &Message{Timestamp: "2026-11-18T00:00:00Z"}
			page.Chats[i].LatestMessage = &m
		}
		base, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		remaining := target - len(safeResultText(base, 0))
		for i := range page.Chats {
			m := *page.Chats[i].LatestMessage
			for _, field := range []*string{&m.Content, &m.Caption} {
				n := min(remaining/6, 131072)
				*field = strings.Repeat("\u0085", n)
				remaining -= n * 6
				if n < 131072 {
					*field += strings.Repeat("x", remaining)
					remaining = 0
				}
			}
		}
		if remaining != 0 {
			t.Fatal("fixture cannot reach target")
		}
		structured, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		text := safeResultText(structured, 0)
		if len(text) != target {
			t.Fatalf("safe text %d, target %d", len(text), target)
		}
		if got := resultSizesFit(uint64(len(structured)), uint64(len(text))); got != (target <= resultBudget) {
			t.Fatalf("actual text size %d: %v", target, got)
		}
		if target > resultBudget && !encodeResult(page).IsError {
			t.Fatal("oversized text accepted")
		}
	}
}

func TestResultSuccessfulNearLimitAllocations(t *testing.T) {
	page := ChatPage{Source: "local_store", UntrustedData: true, Limit: 9, Count: 9, Chats: make([]Chat, 9)}
	for i := range page.Chats {
		m := &Message{Timestamp: "2026-11-18T00:00:00.123456789Z"}
		page.Chats[i].LatestMessage = &m
	}
	base, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	remaining := resultBudget - (16 << 10) - len(base)
	for i := range page.Chats {
		m := *page.Chats[i].LatestMessage
		for _, field := range []*string{&m.Content, &m.Caption} {
			n := min(remaining, 256<<10)
			*field = strings.Repeat("x", n)
			remaining -= n
		}
	}
	if remaining != 0 {
		t.Fatal("fixture cannot reach target")
	}
	result := encodeResult(page)
	if result.IsError {
		t.Fatal("near-limit result refused")
	}
	structured := result.StructuredContent.(json.RawMessage)
	text := result.Content[0].(*sdk.TextContent).Text
	if len(structured) != resultBudget-(16<<10) || len(text) != len(structured) {
		t.Fatalf("sizes: structured %d text %d", len(structured), len(text))
	}
	id, err := jsonrpc.MakeID("bounded-id")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := jsonrpc.EncodeMessage(&jsonrpc.Response{ID: id, Result: raw})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw)+1 > len(structured)+2*len(text)+1024 || len(wire)+1 > len(structured)+2*len(text)+1024 {
		t.Fatal("serialized result exceeds charge")
	}
	t.Logf("retained sample: structured=%d cap=%d text=%d result=%d cap=%d frame=%d cap=%d", len(structured), cap(structured), len(text), len(raw), cap(raw), len(wire)+1, cap(wire))
	measure := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			res := encodeResult(page)
			if res.IsError {
				b.Fatal("near-limit result refused")
			}
			raw, err := json.Marshal(res)
			if err != nil {
				b.Fatal(err)
			}
			wire, err := jsonrpc.EncodeMessage(&jsonrpc.Response{ID: id, Result: raw})
			if err != nil || len(wire)+1 > frameBudget {
				b.Fatalf("wire size %d, %v", len(wire), err)
			}
		}
	})
	t.Logf("structured=%d text=%d full-path=%d bytes/op %d allocs/op", len(structured), len(text), measure.AllocedBytesPerOp(), measure.AllocsPerOp())
}

func TestResultOutputSchemas(t *testing.T) {
	for _, tc := range []struct {
		tool  string
		value any
	}{
		{"list_chats", ChatPage{Source: "local_store", UntrustedData: true, Limit: 30}},
		{"get_chat", ChatResult{Source: "local_store", UntrustedData: true}},
		{"search_contacts", ContactPage{Source: "local_store", UntrustedData: true, Limit: 50}},
		{"get_direct_chat_by_contact", ChatResult{Source: "local_store", UntrustedData: true}},
		{"get_contact_chats", ChatPage{Source: "local_store", UntrustedData: true, Limit: 20}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			_, schema := toolSchemas(tc.tool)
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			var s jsonschema.Schema
			if err := json.Unmarshal(encoded, &s); err != nil {
				t.Fatal(err)
			}
			resolved, err := s.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			for i, value := range []any{tc.value, ErrorResult{Error: ToolError{Code: "resource_limit", Message: "limit"}}} {
				result := encodeResult(value)
				if result.IsError != (i == 1) {
					t.Fatalf("unexpected error flag for case %d", i)
				}
				var decoded any
				if err := json.Unmarshal(result.StructuredContent.(json.RawMessage), &decoded); err != nil {
					t.Fatal(err)
				}
				if err := resolved.Validate(decoded); err != nil {
					t.Fatal(err)
				}
				var decodedText any
				if err := json.Unmarshal([]byte(result.Content[0].(*sdk.TextContent).Text), &decodedText); err != nil || !reflect.DeepEqual(decoded, decodedText) {
					t.Fatal("text and structured content differ")
				}
				decoded.(map[string]any)["unexpected"] = true
				if err := resolved.Validate(decoded); err == nil {
					t.Fatal("output schema accepted unknown property")
				}
			}
		})
	}
}
