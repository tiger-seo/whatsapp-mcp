package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCommonArguments(t *testing.T) {
	cases := []struct{ tool, input, field string }{
		{"list_chats", `null`, "arguments"}, {"list_chats", `[]`, "arguments"},
		{"list_chats", `{"limit":null}`, "limit"}, {"list_chats", `{"limit":0}`, "limit"},
		{"list_chats", `{"limit":201}`, "limit"}, {"list_chats", `{"limit":"1"}`, "limit"},
		{"list_chats", `{"limit":true}`, "limit"}, {"list_chats", `{"page":1.5}`, "page"},
		{"list_chats", `{"page":100001}`, "page"}, {"list_chats", `{"page":501,"limit":200}`, "page"},
		{"list_chats", `{"page":999999999999999999999999}`, "page"},
		{"list_chats", `{"secret-value":1}`, "arguments"}, {"list_chats", `{"limit":1,"\u006cimit":2}`, "limit"},
		{"list_chats", `{"sort_by":"secret-value"}`, "sort_by"}, {"list_chats", `{"include_last_message":null}`, "include_last_message"},
		{"search_contacts", `{}`, "query"}, {"search_contacts", `{"query":" \t"}`, "query"},
		{"get_chat", `{"chat_jid":"1:2@s.whatsapp.net"}`, "chat_jid"},
		{"get_chat", `{"chat_jid":"1@example.org"}`, "chat_jid"},
		{"get_contact_chats", `{"jid":"1@g.us"}`, "jid"},
		{"get_direct_chat_by_contact", `{"sender_phone_number":"123456789012345678901"}`, "sender_phone_number"},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.input, func(t *testing.T) {
			_, err := validateArguments(tc.tool, json.RawMessage(tc.input))
			if err == nil || err.Code != "invalid_input" || err.Field != tc.field || err.Message == "" {
				t.Fatalf("error = %+v, want invalid_input field %s", err, tc.field)
			}
			if strings.Contains(err.Message, "secret-value") {
				t.Fatal("rejected value leaked")
			}
		})
	}
}

func TestCommonExactJSONNumbers(t *testing.T) {
	for _, tc := range []struct {
		input       string
		limit, page int
		valid       bool
	}{
		{`{"limit":1.0,"page":0e999999}`, 1, 0, true},
		{`{"limit":2e2,"page":5.00e2}`, 200, 500, true},
		{`{"limit":1000e-3,"page":1e5}`, 1, 100000, true},
		{`{"limit":0.0100e+2,"page":-0.0}`, 1, 0, true},
		{`{"limit":1.00000000000000000001}`, 0, 0, false},
		{`{"limit":1e-999999}`, 0, 0, false},
		{`{"limit":1e999999}`, 0, 0, false},
		{`{"limit":200.00000000000000001}`, 0, 0, false},
		{`{"page":-0.1}`, 0, 0, false},
		{`{"page":9007199254740993}`, 0, 0, false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			a, e := validateArguments("list_chats", json.RawMessage(tc.input))
			if tc.valid && (e != nil || a.Limit != tc.limit || a.Page != tc.page) {
				t.Fatalf("got %+v, %+v", a, e)
			}
			if !tc.valid && e == nil {
				t.Fatalf("accepted %+v", a)
			}
		})
	}
}

func TestCommonValidationMatrix(t *testing.T) {
	for _, tc := range []struct{ tool, input, field string }{
		{"list_chats", `true`, "arguments"}, {"list_chats", `{} {}`, "arguments"},
		{"list_chats", `{"query":null}`, "query"}, {"list_chats", `{"query":123}`, "query"},
		{"list_chats", `{"page":null}`, "page"}, {"list_chats", `{"page":-1}`, "page"},
		{"list_chats", `{"limit":-1}`, "limit"}, {"list_chats", `{"limit":1.01}`, "limit"},
		{"list_chats", `{"include_last_message":"false"}`, "include_last_message"},
		{"get_chat", `{}`, "chat_jid"}, {"get_chat", `{"chat_jid":" "}`, "chat_jid"},
		{"get_chat", `{"chat_jid":"123:0@s.whatsapp.net"}`, "chat_jid"},
		{"get_contact_chats", `{}`, "jid"}, {"get_contact_chats", `{"jid":""}`, "jid"},
		{"get_contact_chats", `{"jid":"123@example.org"}`, "jid"},
		{"get_direct_chat_by_contact", `{}`, "sender_phone_number"},
		{"get_direct_chat_by_contact", `{"sender_phone_number":" + "}`, "sender_phone_number"},
		{"search_contacts", `{"query":false}`, "query"}, {"search_contacts", `{"query":"\u2003"}`, "query"},
	} {
		t.Run(tc.tool+tc.input, func(t *testing.T) {
			_, e := validateArguments(tc.tool, json.RawMessage(tc.input))
			if e == nil || e.Code != "invalid_input" || e.Field != tc.field || e.Message == "" {
				t.Fatalf("got %+v", e)
			}
			r := argumentErrorResult(e)
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if !r.IsError || strings.Contains(string(b), "example.org") {
				t.Fatalf("unsafe result: %s", b)
			}
		})
	}
	for _, tc := range []struct {
		tool, field, value string
		valid              bool
	}{
		{"get_chat", "chat_jid", strings.Repeat("a", 252) + "@lid", true},
		{"get_chat", "chat_jid", strings.Repeat("a", 253) + "@lid", false},
		{"get_contact_chats", "jid", strings.Repeat("é", 126) + "@lid", true},
		{"get_contact_chats", "jid", strings.Repeat("é", 127) + "@lid", false},
		{"get_contact_chats", "jid", strings.Repeat("1", 20), true},
		{"get_contact_chats", "jid", strings.Repeat("1", 21), false},
	} {
		b, _ := json.Marshal(map[string]string{tc.field: tc.value})
		_, e := validateArguments(tc.tool, b)
		if (e == nil) != tc.valid {
			t.Errorf("%s %d bytes: %+v", tc.field, len(tc.value), e)
		}
	}
}

func TestCommonSchemaValidation(t *testing.T) {
	for _, tc := range []struct{ name, valid, invalid string }{
		{"list_chats", `{}`, `{"limit":0}`},
		{"get_chat", `{"chat_jid":"1@g.us"}`, `{}`},
		{"search_contacts", `{"query":"x"}`, `{"query":null}`},
		{"get_direct_chat_by_contact", `{"sender_phone_number":"123"}`, `{"sender_phone_number":"123","extra":1}`},
		{"get_contact_chats", `{"jid":"123@lid"}`, `{"jid":"123@lid","page":-1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, out := toolSchemas(tc.name)
			resolve := func(m map[string]any) *jsonschema.Resolved {
				b, _ := json.Marshal(m)
				var s jsonschema.Schema
				if err := json.Unmarshal(b, &s); err != nil {
					t.Fatal(err)
				}
				r, err := s.Resolve(nil)
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			decode := func(s string) any {
				var v any
				if err := json.Unmarshal([]byte(s), &v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			i, o := resolve(in), resolve(out)
			if err := i.Validate(decode(tc.valid)); err != nil {
				t.Errorf("valid input: %v", err)
			}
			if err := i.Validate(decode(tc.invalid)); err == nil {
				t.Error("invalid input accepted")
			}
			success := `{"source":"local_store","untrusted_data":true,"chats":[],"count":0,"limit":30,"page":0}`
			if tc.name == "search_contacts" {
				success = `{"source":"local_store","untrusted_data":true,"contacts":[],"count":0,"limit":50,"page":0}`
			}
			if tc.name == "get_chat" || tc.name == "get_direct_chat_by_contact" {
				success = `{"source":"local_store","untrusted_data":true,"chat":{"jid":"1@g.us","name":"","is_group":true,"last_message_time":null}}`
			}
			if err := o.Validate(decode(success)); err != nil {
				t.Errorf("success: %v", err)
			}
			if err := o.Validate(decode(`{"error":{"code":"invalid_input","message":"required","field":"query"}}`)); err != nil {
				t.Errorf("error: %v", err)
			}
			if err := o.Validate(decode(`{"error":{"code":"invalid_input","message":"required","secret":"x"}}`)); err == nil {
				t.Error("unknown error property accepted")
			}
		})
	}
}

func TestCommonDefaultsAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		tool  string
		limit int
	}{{"list_chats", 30}, {"search_contacts", 50}, {"get_contact_chats", 20}} {
		input := `{}`
		if tc.tool == "search_contacts" {
			input = `{"query":" x "}`
		}
		if tc.tool == "get_contact_chats" {
			input = `{"jid":"+1 (555) 12"}`
		}
		a, e := validateArguments(tc.tool, json.RawMessage(input))
		if e != nil || a.Limit != tc.limit || a.Page != 0 {
			t.Fatalf("%s: %+v %+v", tc.tool, a, e)
		}
	}
	a, e := validateArguments("list_chats", json.RawMessage(`{"include_last_message":false,"page":0,"limit":200,"query":" x ","sort_by":"name"}`))
	if e != nil || a.IncludeLastMessage || a.Query != " x " || a.SortBy != "name" {
		t.Fatalf("explicit values: %+v %+v", a, e)
	}
	a, e = validateArguments("list_chats", json.RawMessage(`{"page":500,"limit":200}`))
	if e != nil || a.Offset != 100000 || !a.IncludeLastMessage {
		t.Fatalf("boundary: %+v %+v", a, e)
	}
	for _, n := range []int{512, 513} {
		b, _ := json.Marshal(map[string]string{"query": strings.Repeat("é", n)})
		_, e := validateArguments("search_contacts", b)
		if (e == nil) != (n == 512) {
			t.Fatalf("%d multibyte characters: %+v", n, e)
		}
	}
	for _, input := range []string{`{"jid":"123:2@s.whatsapp.net"}`, `{"jid":"123:2@lid"}`} {
		if _, e := validateArguments("get_contact_chats", json.RawMessage(input)); e != nil {
			t.Fatalf("supported device: %+v", e)
		}
	}
}

func TestCommonSchemas(t *testing.T) {
	for _, name := range []string{"list_chats", "get_chat", "search_contacts", "get_direct_chat_by_contact", "get_contact_chats"} {
		in, out := toolSchemas(name)
		if in["type"] != "object" || in["additionalProperties"] != false || out["oneOf"] == nil {
			t.Errorf("%s schemas: %#v %#v", name, in, out)
		}
	}
}

func TestCommonErrorResult(t *testing.T) {
	_, e := validateArguments("list_chats", json.RawMessage(`{"sort_by":"secret-value"}`))
	if e == nil {
		t.Fatal("invalid sort accepted")
	}
	r := argumentErrorResult(e)
	if !r.IsError || len(r.Content) != 1 {
		t.Fatalf("result = %+v", r)
	}
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := r.Content[0].(*sdk.TextContent)
	if !ok || text.Text != string(b) || strings.Contains(text.Text, "secret-value") {
		t.Fatalf("unsafe or mismatched result: %+v", r)
	}
}

func TestCommonErrorCategories(t *testing.T) {
	for _, code := range []string{"not_found", "busy", "resource_limit", "timeout", "cancelled", "unavailable", "storage_error", "secret-value"} {
		r := toolErrorResult(code)
		root, ok := r.StructuredContent.(ErrorResult)
		if !ok || !r.IsError || root.Error.Message == "" || root.Error.Field != "" {
			t.Fatalf("%s: %+v", code, r)
		}
		if code == "secret-value" {
			code = "storage_error"
		}
		if root.Error.Code != code {
			t.Fatalf("got %+v", root)
		}
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "secret-value") {
			t.Fatalf("leaked error: %s", b)
		}
		text, ok := r.Content[0].(*sdk.TextContent)
		if !ok {
			t.Fatal("missing text")
		}
		var decoded ErrorResult
		if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil || decoded != root {
			t.Fatalf("text mismatch: %s", text.Text)
		}
	}
}

func TestCommonLowDiskError(t *testing.T) {
	r := toolErrorResult("low_disk")
	root, ok := r.StructuredContent.(ErrorResult)
	if !ok || !r.IsError || root.Error.Code != "unavailable" || root.Error.Message != "low disk space" || root.Error.Field != "" {
		t.Fatalf("low-disk result: %+v", r)
	}
	if len(r.Content) != 1 {
		t.Fatalf("content: %+v", r.Content)
	}
	text, ok := r.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content: %+v", r.Content)
	}
	var decoded ErrorResult
	if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil || decoded != root {
		t.Fatalf("text = %q, error = %v", text.Text, err)
	}
	for _, name := range []string{"list_chats", "get_chat", "search_contacts", "get_direct_chat_by_contact", "get_contact_chats"} {
		_, out := toolSchemas(name)
		b, _ := json.Marshal(out)
		var schema jsonschema.Schema
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatal(err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		var object any
		if err := json.Unmarshal([]byte(text.Text), &object); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(object); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Unknown categories are not reflected into either representation.
	r = toolErrorResult("low disk space: SQL secret /private/path")
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "/private/path") {
		t.Fatalf("leaked raw error: %s", b)
	}
}

func TestCommonSchemaDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, required string
		limit          int
		latest         bool
	}{
		{"list_chats", "", 30, true}, {"get_chat", "chat_jid", 0, true},
		{"search_contacts", "query", 50, false}, {"get_direct_chat_by_contact", "sender_phone_number", 0, false},
		{"get_contact_chats", "jid", 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := toolSchemas(tc.name)
			props := in["properties"].(map[string]any)
			required := in["required"].([]string)
			if tc.required == "" && len(required) != 0 || tc.required != "" && (len(required) != 1 || required[0] != tc.required) {
				t.Errorf("required: %v", required)
			}
			if tc.limit != 0 {
				limit := props["limit"].(map[string]any)
				page := props["page"].(map[string]any)
				if limit["default"] != tc.limit || limit["minimum"] != 1 || limit["maximum"] != 200 || page["default"] != 0 || page["minimum"] != 0 || page["maximum"] != 100000 {
					t.Errorf("pagination: %v %v", limit, page)
				}
			}
			if tc.latest && props["include_last_message"].(map[string]any)["default"] != true {
				t.Error("latest default")
			}
			if tc.name == "list_chats" {
				if props["query"].(map[string]any)["default"] != "" || props["sort_by"].(map[string]any)["default"] != "last_active" {
					t.Error("query/sort default")
				}
			}
		})
	}
}

func TestCommonRequiredStringSchemas(t *testing.T) {
	for _, tc := range []struct{ tool, field string }{
		{"get_chat", "chat_jid"}, {"search_contacts", "query"},
		{"get_direct_chat_by_contact", "sender_phone_number"}, {"get_contact_chats", "jid"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			in, _ := toolSchemas(tc.tool)
			prop := in["properties"].(map[string]any)[tc.field].(map[string]any)
			if prop["minLength"] != 1 {
				t.Errorf("minLength = %v, want 1", prop["minLength"])
			}
			b, _ := json.Marshal(in)
			var schema jsonschema.Schema
			if err := json.Unmarshal(b, &schema); err != nil {
				t.Fatal(err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := resolved.Validate(map[string]any{tc.field: ""}); err == nil {
				t.Error("schema accepts empty required string")
			}
			if err := resolved.Validate(map[string]any{tc.field: " "}); err != nil {
				t.Errorf("schema should leave whitespace validation to handler: %v", err)
			}
			for _, value := range []string{"", " \t"} {
				raw, _ := json.Marshal(map[string]string{tc.field: value})
				_, e := validateArguments(tc.tool, raw)
				if e == nil || e.Field != tc.field || e.Code != "invalid_input" {
					t.Errorf("blank %q: %+v", value, e)
				}
			}
		})
	}
	in, _ := toolSchemas("list_chats")
	q := in["properties"].(map[string]any)["query"].(map[string]any)
	if _, ok := q["minLength"]; ok {
		t.Error("optional query must allow empty string")
	}
	if _, e := validateArguments("list_chats", json.RawMessage(`{"query":""}`)); e != nil {
		t.Errorf("empty optional query: %+v", e)
	}
}

func TestCommonMetadataOnlyAndUnknownFields(t *testing.T) {
	for _, tc := range []struct{ tool, base string }{
		{"list_chats", `{"include_last_message":false}`}, {"get_chat", `{"chat_jid":"123@g.us","include_last_message":false}`},
	} {
		t.Run(tc.tool+"/metadata", func(t *testing.T) {
			a, e := validateArguments(tc.tool, json.RawMessage(tc.base))
			if e != nil || a.IncludeLastMessage {
				t.Fatalf("metadata-only arguments: %+v %+v", a, e)
			}
		})
	}
	for _, tc := range []struct{ tool, input string }{
		{"list_chats", `{"private-secret":"value"}`}, {"get_chat", `{"chat_jid":"123@g.us","private-secret":1}`},
		{"search_contacts", `{"query":"x","private-secret":1}`}, {"get_direct_chat_by_contact", `{"sender_phone_number":"123","private-secret":1}`},
		{"get_contact_chats", `{"jid":"123@lid","private-secret":1}`},
	} {
		t.Run(tc.tool+"/unknown", func(t *testing.T) {
			_, e := validateArguments(tc.tool, json.RawMessage(tc.input))
			if e == nil || e.Code != "invalid_input" || e.Field != "arguments" || strings.Contains(e.Message, "private-secret") {
				t.Fatalf("unknown field: %+v", e)
			}
			in, _ := toolSchemas(tc.tool)
			b, _ := json.Marshal(in)
			var schema jsonschema.Schema
			if err := json.Unmarshal(b, &schema); err != nil {
				t.Fatal(err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
				t.Fatal(err)
			}
			if err := resolved.Validate(value); err == nil {
				t.Error("schema accepts unknown field")
			}
		})
	}
}
