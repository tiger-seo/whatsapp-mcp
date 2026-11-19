package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMCPChatQueriesLiteralSearch(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	jid := "MiXeD-Café@g.us"
	nameChat(t, st, jid, "O'Reilly \"Café\" 100%_+&\nline", NameSaved)
	other := "other@g.us"
	nameChat(t, st, other, "OReilly Cafe 100XY line", NameReal)
	for _, query := range []string{"mixed-cafe\u0301", "MIXED-CAFÉ", "o'reilly", "\"cafe\u0301\"", "%_", "+&", "\nline"} {
		t.Run(query, func(t *testing.T) {
			got, err := st.ToolReader().ListChats(context.Background(), query, ByName, false, all)
			if err != nil {
				t.Fatal(err)
			}
			wantIDs(t, "literal search", chatJIDs(got), []string{jid})
		})
	}
	for _, query := range []string{"%missing", "_missing", "o'reilly' OR 1=1 --"} {
		got, err := st.ToolReader().ListChats(context.Background(), query, ByName, false, all)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, "literal non-match", chatJIDs(got), nil)
	}
}

func TestMCPChatQueriesOrder(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	a, b, c, d, e := "a@g.us", "b@g.us", "c@g.us", "d@g.us", "e@g.us"
	mustStore(t, st, msg(a, "1", alice, at(2)), msg(b, "1", alice, at(2)), msg(c, "1", alice, at(1)))
	nameChat(t, st, b, "ALPHA", NameFallback)
	nameChat(t, st, c, "alpha", NameSaved)
	nameChat(t, st, d, "Beta", NameReal)
	if err := st.UpdateChat(context.Background(), ChatUpdate{JID: e}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sort ChatSort
		want []string
	}{
		{ByName, []string{b, c, d, a, e}},
		{ByLastActivity, []string{a, b, c, d, e}},
	} {
		got, err := st.ToolReader().ListChats(context.Background(), "", tc.sort, false, all)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, "chat order", chatJIDs(got), tc.want)
	}
}

func TestMCPChatQueriesLatestCompositeIdentity(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	a, b := "a@g.us", "b@g.us"
	mustStore(t, st,
		msg(a, "z", alice, at(1), withText("older")),
		msg(a, "a", alice, at(2), withText("tie loser")),
		msg(a, "shared", alice, at(2), withText("a winner")),
		msg(b, "shared", bob, at(3), withText("b winner")),
	)
	reader := st.ToolReader()
	chats, err := reader.ListChats(context.Background(), "", ByLastActivity, true, all)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "latest order", chatJIDs(chats), []string{b, a})
	for _, tc := range []struct{ jid, text string }{{a, "a winner"}, {b, "b winner"}} {
		chat, err := reader.GetChat(context.Background(), tc.jid, true)
		if err != nil {
			t.Fatal(err)
		}
		if chat.Latest == nil || chat.Latest.ID != "shared" || chat.Latest.ChatJID != tc.jid || chat.Latest.Text != tc.text {
			t.Errorf("GetChat(%s) latest = %+v", tc.jid, chat.Latest)
		}
		for _, listed := range chats {
			if listed.JID == tc.jid && (listed.Latest == nil || listed.Latest.ID != "shared" || listed.Latest.ChatJID != tc.jid || listed.Latest.Text != tc.text) {
				t.Errorf("ListChats(%s) latest = %+v", tc.jid, listed.Latest)
			}
		}
	}
}

func TestMCPChatQueriesGetMetadataSkipsLatest(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	jid := "a@g.us"
	mustStore(t, st, msg(jid, "one", alice, at(1)))
	if _, err := st.writer.ExecContext(context.Background(), `UPDATE messages SET text = ? WHERE chat_jid = ?`, strings.Repeat("x", maxTextBytes+1), jid); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	st.hooks.wrapReader = func(q querier) querier { rec.q = q; return rec }
	chat, err := st.ToolReader().GetChat(context.Background(), jid, false)
	if err != nil || chat.JID != jid || chat.Latest != nil {
		t.Fatalf("metadata-only lookup = %+v, %v", chat, err)
	}
	if len(rec.queries) != 1 || strings.Contains(strings.ToLower(rec.queries[0].sql), "messages") {
		t.Fatalf("metadata-only statements: %#v", rec.queries)
	}
	if _, err := st.ToolReader().GetChat(context.Background(), jid, true); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("latest lookup = %v, want resource limit", err)
	}
}
