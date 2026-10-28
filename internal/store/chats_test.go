package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

type chatRow struct {
	name    string
	quality int
	nameMS  *int64
	last    *int64
	phone   string
}

func readChat(t testing.TB, st *Store, jid string) chatRow {
	t.Helper()
	var c chatRow
	if err := st.reader.QueryRow("SELECT name, name_quality, name_time, last_message_time, phone FROM chats WHERE jid = ?", jid).
		Scan(&c.name, &c.quality, &c.nameMS, &c.last, &c.phone); err != nil {
		t.Fatalf("chat %s: %v", jid, err)
	}
	return c
}

func offer(name string, q NameQuality, minute int) ChatUpdate {
	return ChatUpdate{JID: alice, Name: name, NameQuality: q, NameTime: at(minute)}
}

func update(t *testing.T, st *Store, us ...ChatUpdate) {
	t.Helper()
	if err := st.UpdateChats(context.Background(), us); err != nil {
		t.Fatalf("UpdateChats: %v", err)
	}
}

func TestNameRuleQualityOrder(t *testing.T) {
	cases := []struct {
		name   string
		offers []ChatUpdate
		want   string
	}{
		{"real beats a later fallback", []ChatUpdate{offer("Alice", NameReal, 0), offer("491511234567", NameFallback, 10)}, "Alice"},
		{"real beats an earlier fallback", []ChatUpdate{offer("491511234567", NameFallback, 0), offer("Alice", NameReal, 10)}, "Alice"},
		{"saved beats a later real", []ChatUpdate{offer("Mum", NameSaved, 0), offer("Bank Support", NameReal, 10)}, "Mum"},
		{"saved replaces an earlier real", []ChatUpdate{offer("Bank Support", NameReal, 0), offer("Mum", NameSaved, 10)}, "Mum"},
		{"newer real replaces older", []ChatUpdate{offer("Old", NameReal, 10), offer("New", NameReal, 11)}, "New"},
		{"older real does not replace newer", []ChatUpdate{offer("New", NameReal, 11), offer("Old", NameReal, 9)}, "New"},
		{"equal time takes the greater name", []ChatUpdate{offer("Anna", NameReal, 5), offer("Anne", NameReal, 5)}, "Anne"},
		{"equal time takes the greater name, other order", []ChatUpdate{offer("Anne", NameReal, 5), offer("Anna", NameReal, 5)}, "Anne"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, _ := openTest(t, newDataDir(t))
			for _, o := range c.offers {
				update(t, st, o)
			}
			if got := readChat(t, st, alice).name; got != c.want {
				t.Errorf("name = %q, want %q", got, c.want)
			}
		})
	}
}

func TestNameRuleStoresQualityTimeAndFold(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	update(t, st, offer("ÉTÉ", NameSaved, 3))
	var fold string
	st.reader.QueryRow("SELECT name_fold FROM chats").Scan(&fold)
	c := readChat(t, st, alice)
	if c.name != "ÉTÉ" || c.quality != int(NameSaved) || c.nameMS == nil || *c.nameMS != at(3).UnixMilli() || fold != "été" {
		t.Errorf("chat = %+v, fold %q", c, fold)
	}
}

func TestEmptyAndBadOffers(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	update(t, st, offer("Alice", NameReal, 0))
	update(t, st, ChatUpdate{JID: alice, Name: "", NameQuality: NameSaved, NameTime: at(50)})
	if got := readChat(t, st, alice).name; got != "Alice" {
		t.Errorf("an empty name replaced Alice: %q", got)
	}
	for field, u := range map[string]ChatUpdate{
		"name_time":     {JID: alice, Name: "X", NameQuality: NameReal},
		"name_quality":  {JID: alice, Name: "X", NameTime: at(0)},
		"name_time ":    {JID: alice, Name: "X", NameQuality: NameReal, NameTime: time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)},
		"last_activity": {JID: alice, LastActivity: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)},
		"chat_jid":      {JID: "nobody@evil.example"},
	} {
		err := st.UpdateChats(context.Background(), []ChatUpdate{u})
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: %v, want a validation error", field, err)
		}
	}
	if err := st.UpdateChats(context.Background(), nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty batch: %v", err)
	}
	if got := readChat(t, st, alice).name; got != "Alice" {
		t.Errorf("a rejected offer changed the name: %q", got)
	}
}

func TestActivityOnlyMovesForward(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	update(t, st, ChatUpdate{JID: alice, LastActivity: at(20)})
	update(t, st, ChatUpdate{JID: alice})                       // none offered: never clears
	update(t, st, ChatUpdate{JID: alice, LastActivity: at(10)}) // older: ignored
	if got := readChat(t, st, alice).last; got == nil || *got != at(20).UnixMilli() {
		t.Errorf("last activity = %v, want 20", got)
	}
	update(t, st, ChatUpdate{JID: alice, LastActivity: at(30)})
	if got := readChat(t, st, alice).last; got == nil || *got != at(30).UnixMilli() {
		t.Errorf("last activity = %v, want 30", got)
	}
}

func TestChatWithoutActivityHasNull(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	update(t, st, ChatUpdate{JID: alice})
	c := readChat(t, st, alice)
	if c.last != nil || c.name != "" || c.quality != 0 || c.nameMS != nil || c.phone != "491511234567" {
		t.Errorf("chat = %+v", c)
	}
}

func TestBatchOfUpdatesIsAtomic(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	err := st.UpdateChats(context.Background(), []ChatUpdate{offer("Alice", NameReal, 0), {JID: "bad"}})
	if err == nil {
		t.Fatal("a bad update was accepted")
	}
	if countRows(t, st, "chats") != 0 {
		t.Error("an invalid batch created chats")
	}
	many := make([]ChatUpdate, maxChatUpdates+1)
	for i := range many {
		many[i] = ChatUpdate{JID: alice}
	}
	if err := st.UpdateChats(context.Background(), many); !errors.Is(err, ErrInvalid) {
		t.Errorf("1,001 updates: %v", err)
	}
}
