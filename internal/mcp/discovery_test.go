package mcp

import (
	"reflect"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tiger-seo/whatsapp-mcp/internal/buildinfo"
)

func TestFiveToolDiscovery(t *testing.T) {
	r := contactFixture(t)
	c := chatTestServer(t, r)
	ctx := t.Context()
	init := c.InitializeResult()
	if init.ServerInfo.Name != "whatsapp-mcp" || init.ServerInfo.Version != buildinfo.Get().Version ||
		init.Capabilities.Tools == nil || init.Capabilities.Tools.ListChanged || init.Capabilities.Resources != nil ||
		init.Capabilities.Prompts != nil || init.Capabilities.Logging != nil || init.Capabilities.Experimental != nil {
		t.Fatalf("initialization: %+v", init)
	}
	list, err := c.ListTools(ctx, nil)
	if err != nil || list.NextCursor != "" || len(list.Tools) != 5 {
		t.Fatalf("discovery: %+v, %v", list, err)
	}
	want := map[string]struct {
		required []any
		defaults map[string]any
	}{
		"list_chats":       {[]any{}, map[string]any{"query": "", "limit": float64(30), "page": float64(0), "include_last_message": true, "sort_by": "last_active"}},
		"get_chat":         {[]any{"chat_jid"}, map[string]any{"include_last_message": true}},
		searchContactsTool: {[]any{"query"}, map[string]any{"limit": float64(50), "page": float64(0)}},
		directContactTool:  {[]any{"sender_phone_number"}, nil},
		contactChatsTool:   {[]any{"jid"}, map[string]any{"limit": float64(20), "page": float64(0)}},
	}
	for _, tool := range list.Tools {
		w, ok := want[tool.Name]
		if !ok {
			t.Fatalf("unexpected or duplicate tool %q", tool.Name)
		}
		delete(want, tool.Name)
		input := chatJSON(t, tool.InputSchema).(map[string]any)
		output := chatJSON(t, tool.OutputSchema).(map[string]any)
		if input["type"] != "object" || input["additionalProperties"] != false || !reflect.DeepEqual(input["required"], w.required) ||
			output["type"] != "object" || len(output["oneOf"].([]any)) != 2 || !strings.Contains(tool.Description, "untrusted data") {
			t.Errorf("%s schemas: %v / %v", tool.Name, input, output)
		}
		props := input["properties"].(map[string]any)
		if len(props) != len(w.required)+len(w.defaults) {
			t.Errorf("%s input fields: %v", tool.Name, props)
		}
		for key, value := range w.defaults {
			if props[key].(map[string]any)["default"] != value {
				t.Errorf("%s.%s default = %v, want %v", tool.Name, key, props[key], value)
			}
		}
		a := tool.Annotations
		if a == nil || !a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint || a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s annotations: %+v", tool.Name, a)
		}
	}
	for _, name := range []string{"get_login_status", "get_pairing_qr"} {
		_, err := c.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{"approval": "synthetic-secret"}})
		if err == nil || !strings.Contains(err.Error(), "request failed") || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatalf("deferred tool %s: %v", name, err)
		}
		waitToolCredit(t, c)
	}
	if r.calls.Load() != 0 {
		t.Fatal("discovery/deferred calls reached the store")
	}
	chatCall(t, c, "get_chat", map[string]any{"chat_jid": fixtureJID(0)}, false)
	chatCall(t, c, searchContactsTool, map[string]any{"query": "Contact 000"}, false)
	if r.calls.Load() != 2 {
		t.Fatal("successful calls did not use exactly one read each")
	}
}
