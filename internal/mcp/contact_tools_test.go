package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

const (
	searchContactsTool = "search_contacts"
	directContactTool  = "get_direct_chat_by_contact"
	contactChatsTool   = "get_contact_chats"
)

func contactFixture(t *testing.T) *countedReader {
	t.Helper()
	return newFixture(t, func(st *store.Store, dev *wastore.Device) {
		ctx := t.Context()
		alias := types.NewJID("123456789000", types.HiddenUserServer)
		unmapped := types.NewJID("123456789001", types.HiddenUserServer)
		if _, _, err := dev.Contacts.PutBusinessName(ctx, alias, "Alias %_+& Business"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := dev.Contacts.PutPushName(ctx, unmapped, "Unmapped Push"); err != nil {
			t.Fatal(err)
		}
		stamp := time.Date(2026, 1, 2, 0, 0, 0, 123000000, time.UTC)
		const group = "120363000000000001@g.us"
		if err := st.StoreMessages(ctx, []store.Message{
			{ChatJID: alias.String(), SenderJID: alias.String(), ID: "alias-direct", Time: stamp},
			{ChatJID: unmapped.String(), SenderJID: unmapped.String(), ID: "unmapped-direct", Time: stamp},
			{ChatJID: group, SenderJID: alias.String(), ID: "participation-1", Time: stamp},
			{ChatJID: group, SenderJID: fixtureJID(0), ID: "participation-2", Time: stamp},
			{ChatJID: group, SenderJID: fixtureJID(1), ID: "z-latest", Time: stamp, MediaType: "image", Caption: "Synthetic caption"},
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestContactToolsSearch(t *testing.T) {
	r := contactFixture(t)
	c := chatTestServer(t, r)
	for _, query := range []string{"Contact 000", "FIRST 000", "%_+& Business", fixturePhone(0)} {
		t.Run(query, func(t *testing.T) {
			before := r.calls.Load()
			root := chatCall(t, c, searchContactsTool, map[string]any{"query": query}, false)
			want := []any{map[string]any{"jid": fixtureJID(0), "name": "Contact 000", "phone_number": fixturePhone(0)}}
			if r.calls.Load()-before != 1 || root["source"] != "local_store" || root["untrusted_data"] != true ||
				root["limit"] != float64(50) || root["page"] != float64(0) || root["count"] != float64(1) || !reflect.DeepEqual(root["contacts"], want) {
				t.Fatalf("contact result: %+v", root)
			}
		})
	}
	for i, want := range []int{50, 50, 50, 50, 35, 0} {
		root := chatCall(t, c, searchContactsTool, map[string]any{"query": "Contact", "page": i}, false)
		if n := len(root["contacts"].([]any)); n != want || root["count"] != float64(want) || root["page"] != float64(i) {
			t.Fatalf("page %d: %+v", i, root)
		}
		for j, v := range root["contacts"].([]any) {
			if v.(map[string]any)["jid"] != fixtureJID(i*50+j) {
				t.Fatalf("unexpected contact %d on page %d: %v", j, i, v)
			}
		}
	}
	root := chatCall(t, c, searchContactsTool, map[string]any{"query": "unmapped push"}, false)
	u := root["contacts"].([]any)[0].(map[string]any)
	if u["jid"] != "123456789001@lid" || u["phone_number"] != nil {
		t.Fatalf("unmapped contact: %+v", u)
	}
}

func TestContactToolsChats(t *testing.T) {
	r := contactFixture(t)
	c := chatTestServer(t, r)
	var baseline any
	for _, input := range []string{"+1 (555) 000-0000", fixtureJID(0), "15550000000:3@s.whatsapp.net", "123456789000@lid", "123456789000:2@lid"} {
		t.Run(input, func(t *testing.T) {
			before := r.calls.Load()
			direct := chatCall(t, c, directContactTool, map[string]any{"sender_phone_number": input}, false)
			if direct["chat"].(map[string]any)["jid"] != fixtureJID(0) {
				t.Fatalf("phone row did not win: %+v", direct)
			}
			root := chatCall(t, c, contactChatsTool, map[string]any{"jid": input}, false)
			rows := root["chats"].([]any)
			if r.calls.Load()-before != 2 || len(rows) != 3 || root["count"] != float64(3) || root["limit"] != float64(20) || root["page"] != float64(0) {
				t.Fatalf("contact chats: %+v", root)
			}
			for i, jid := range []string{"120363000000000001@g.us", "123456789000@lid", fixtureJID(0)} {
				if rows[i].(map[string]any)["jid"] != jid {
					t.Fatalf("row %d: %+v", i, rows[i])
				}
			}
			latest := rows[0].(map[string]any)["latest_message"].(map[string]any)
			if latest["id"] != "z-latest" || latest["sender"] != fixtureJID(1) || latest["caption"] != "Synthetic caption" || latest["timestamp"] != "2026-01-02T00:00:00.123Z" {
				t.Fatalf("latest: %+v", latest)
			}
			if baseline == nil {
				baseline = root
			} else if !reflect.DeepEqual(baseline, root) {
				t.Fatalf("alias results differ: %+v", root)
			}
		})
	}
	for _, input := range []string{"123456789001:7@lid", "123456789001@lid"} {
		root := chatCall(t, c, directContactTool, map[string]any{"sender_phone_number": input}, false)
		if root["chat"].(map[string]any)["jid"] != "123456789001@lid" {
			t.Fatalf("unmapped direct: %+v", root)
		}
	}
	for _, input := range []string{"555000000", strings.Repeat("1", 20)} {
		root := chatCall(t, c, directContactTool, map[string]any{"sender_phone_number": input}, true)
		if root["error"].(map[string]any)["code"] != "not_found" {
			t.Fatalf("non-exact direct: %+v", root)
		}
	}
	root := chatCall(t, c, contactChatsTool, map[string]any{"jid": fixtureJID(0), "limit": 1, "page": 3}, false)
	if len(root["chats"].([]any)) != 0 || root["count"] != float64(0) {
		t.Fatalf("empty page: %+v", root)
	}
	root = chatCall(t, c, contactChatsTool, map[string]any{"jid": "555000000"}, false)
	if len(root["chats"].([]any)) != 0 {
		t.Fatalf("unknown contact: %+v", root)
	}
}

func TestContactToolsValidation(t *testing.T) {
	r := contactFixture(t)
	c := chatTestServer(t, r)
	for _, tc := range []struct{ tool, arg, field string }{
		{searchContactsTool, `{}`, "query"},
		{searchContactsTool, `{"query":" \t"}`, "query"},
		{searchContactsTool, `{"query":null}`, "query"},
		{searchContactsTool, `{"query":true}`, "query"},
		{searchContactsTool, `{"query":"x","query":"y"}`, "query"},
		{searchContactsTool, `{"query":"x","limit":0}`, "limit"},
		{searchContactsTool, `{"query":"x","limit":"1"}`, "limit"},
		{searchContactsTool, `{"query":"x","extra":"secret-value"}`, "arguments"},
		{directContactTool, `{}`, "sender_phone_number"},
		{directContactTool, `{"sender_phone_number":"+()"}`, "sender_phone_number"},
		{directContactTool, `{"sender_phone_number":"123456789012345678901"}`, "sender_phone_number"},
		{directContactTool, `{"sender_phone_number":"1@g.us"}`, "sender_phone_number"},
		{directContactTool, `{"sender_phone_number":"1:bad@s.whatsapp.net"}`, "sender_phone_number"},
		{contactChatsTool, `{}`, "jid"},
		{contactChatsTool, `{"jid":"secret-value@newsletter"}`, "jid"},
		{contactChatsTool, `{"jid":1}`, "jid"},
		{contactChatsTool, `{"jid":"1","limit":201}`, "limit"},
		{contactChatsTool, `{"jid":"1","page":null}`, "page"},
		{contactChatsTool, `{"jid":"1","limit":200,"page":501}`, "page"},
	} {
		t.Run(tc.tool+tc.arg, func(t *testing.T) {
			before := r.calls.Load()
			root := chatCall(t, c, tc.tool, json.RawMessage(tc.arg), true)
			e := root["error"].(map[string]any)
			if r.calls.Load() != before || e["code"] != "invalid_input" || e["field"] != tc.field || strings.Contains(e["message"].(string), "secret-value") {
				t.Fatalf("validation: %+v", root)
			}
		})
	}
}

type failingContactReader struct {
	ChatContactReader
	err error
}

func (r failingContactReader) SearchContacts(context.Context, string, store.Page) ([]store.Contact, error) {
	return nil, r.err
}
func (r failingContactReader) DirectChat(context.Context, string) (store.ChatView, error) {
	return store.ChatView{}, r.err
}
func (r failingContactReader) ContactChats(context.Context, string, store.Page) ([]store.ChatView, error) {
	return nil, r.err
}

func TestContactToolsStorageErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{store.ErrBusy, "busy"}, {store.ErrResourceLimit, "resource_limit"}, {store.ErrClosed, "unavailable"},
		{store.ErrDiskLow, "unavailable"}, {context.DeadlineExceeded, "timeout"}, {errors.New("secret-value"), "storage_error"},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			c := chatTestServer(t, failingContactReader{err: tc.err})
			for tool, args := range map[string]string{
				searchContactsTool: `{"query":"contact"}`, directContactTool: `{"sender_phone_number":"1"}`, contactChatsTool: `{"jid":"1"}`,
			} {
				root := chatCall(t, c, tool, json.RawMessage(args), true)
				e := root["error"].(map[string]any)
				if e["code"] != tc.code || strings.Contains(e["message"].(string), "secret-value") {
					t.Errorf("%s: %+v", tool, root)
				}
			}
		})
	}
}
