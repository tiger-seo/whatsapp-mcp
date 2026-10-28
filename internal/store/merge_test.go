package store

import (
	"context"
	"errors"
	"testing"
)

func merge(t *testing.T, st *Store) {
	t.Helper()
	if err := st.MergeLID(context.Background(), lidAlice, alice); err != nil {
		t.Fatalf("MergeLID: %v", err)
	}
}

func chatExists(t testing.TB, st *Store, jid string) bool {
	t.Helper()
	var n int
	st.reader.QueryRow("SELECT count(*) FROM chats WHERE jid = ?", jid).Scan(&n)
	return n == 1
}

func TestMergeBothChats(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st,
		msg(alice, "P1", alice, at(10)), msg(alice, "P2", alice, at(20)),
		msg(lidAlice, "L1", lidAlice, at(30)), msg(lidAlice, "L2", lidAlice, at(5)),
	)
	update(t, st,
		ChatUpdate{JID: alice, Name: "Alice P", NameQuality: NameReal, NameTime: at(1)},
		ChatUpdate{JID: lidAlice, Name: "Alice L", NameQuality: NameSaved, NameTime: at(2)},
	)
	merge(t, st)

	if chatExists(t, st, lidAlice) {
		t.Error("the LID chat is still there")
	}
	var n int
	st.reader.QueryRow("SELECT count(*) FROM messages WHERE chat_jid = ?", alice).Scan(&n)
	if n != 4 || countRows(t, st, "messages") != 4 {
		t.Errorf("phone chat holds %d of %d messages, want all 4", n, countRows(t, st, "messages"))
	}
	if r := readRow(t, st, alice, "L1"); r.senderJID != alice || r.senderPhone != "491511234567" {
		t.Errorf("moved message sender = %q / %q", r.senderJID, r.senderPhone)
	}
	c := readChat(t, st, alice)
	if c.last == nil || *c.last != at(30).UnixMilli() {
		t.Errorf("activity = %v, want the later one", c.last)
	}
	if c.name != "Alice L" || c.quality != int(NameSaved) {
		t.Errorf("name = %q (quality %d), want the saved one from the LID chat", c.name, c.quality)
	}
}

func TestMergeOnlyLIDChat(t *testing.T) {
	t.Run("saved name is carried", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		mustStore(t, st, msg(lidAlice, "L1", lidAlice, at(7)))
		update(t, st, ChatUpdate{JID: lidAlice, Name: "Alice", NameQuality: NameSaved, NameTime: at(1)})
		merge(t, st)
		c := readChat(t, st, alice)
		if c.name != "Alice" || c.last == nil || *c.last != at(7).UnixMilli() || c.phone != "491511234567" {
			t.Errorf("created phone chat = %+v", c)
		}
		if !messageExists(t, st, alice, "L1") || chatExists(t, st, lidAlice) {
			t.Error("the message did not move or the LID chat stayed")
		}
	})
	t.Run("fallback name is dropped", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		mustStore(t, st, msg(lidAlice, "L1", lidAlice, at(7)))
		update(t, st, ChatUpdate{JID: lidAlice, Name: "100000000042", NameQuality: NameFallback, NameTime: at(1)})
		merge(t, st)
		if c := readChat(t, st, alice); c.name != "" || c.quality != 0 {
			t.Errorf("phone chat took a fallback name: %+v", c)
		}
	})
	t.Run("a newer fallback never replaces an older one", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		mustStore(t, st, msg(lidAlice, "L1", lidAlice, at(7)), msg(alice, "P1", alice, at(8)))
		update(t, st,
			ChatUpdate{JID: alice, Name: "491511234567", NameQuality: NameFallback, NameTime: at(1)},
			ChatUpdate{JID: lidAlice, Name: "100000000042", NameQuality: NameFallback, NameTime: at(50)},
		)
		merge(t, st)
		if c := readChat(t, st, alice); c.name != "491511234567" || c.nameMS == nil || *c.nameMS != at(1).UnixMilli() {
			t.Errorf("name = %+v, want the older fallback kept", c)
		}
	})
}

func TestMergeSharedMessageKeepsThePhoneCopy(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st,
		msg(alice, "X", alice, at(1), withText("phone copy")),
		msg(lidAlice, "X", lidAlice, at(2), withText("lid copy")),
	)
	merge(t, st)
	if got := readRow(t, st, alice, "X").text; got != "phone copy" {
		t.Errorf("text = %q, want the phone chat's copy", got)
	}
	if countRows(t, st, "messages") != 1 {
		t.Errorf("messages = %d, want 1", countRows(t, st, "messages"))
	}
}

func TestMergeRewritesSendersInEveryChat(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st,
		msg(lidAlice, "L1", lidAlice, at(1)),
		msg(groupG, "G1", lidAlice, at(2)), msg(groupH, "H1", lidAlice, at(3)),
		msg(groupG, "G2", bob, at(4)), msg(groupH, "H2", "100000000099@lid", at(5)),
	)
	merge(t, st)
	for chat, id := range map[string]string{groupG: "G1", groupH: "H1", alice: "L1"} {
		if r := readRow(t, st, chat, id); r.senderJID != alice || r.senderPhone != "491511234567" {
			t.Errorf("%s/%s sender = %q / %q", chat, id, r.senderJID, r.senderPhone)
		}
	}
	if r := readRow(t, st, groupG, "G2"); r.senderJID != bob {
		t.Errorf("another sender changed: %q", r.senderJID)
	}
	if r := readRow(t, st, groupH, "H2"); r.senderJID != "100000000099@lid" || r.senderPhone != "" {
		t.Errorf("another LID changed: %q / %q", r.senderJID, r.senderPhone)
	}
}

func TestMergeWithoutALIDChatOnlyRewritesSenders(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st, msg(groupG, "G1", lidAlice, at(2)))
	chats := countRows(t, st, "chats")
	merge(t, st)
	if r := readRow(t, st, groupG, "G1"); r.senderJID != alice {
		t.Errorf("sender = %q", r.senderJID)
	}
	if countRows(t, st, "chats") != chats || chatExists(t, st, alice) {
		t.Error("a chat was created or removed")
	}
}

func TestMergeFailureChangesNothing(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st, msg(lidAlice, "L1", lidAlice, at(1)), msg(groupG, "G1", lidAlice, at(2)), msg(alice, "P1", alice, at(3)))
	before := tableDump(t, st)
	st.hooks.afterSenderMerge = func() error { return errors.New("injected") }
	if err := st.MergeLID(context.Background(), lidAlice, alice); err == nil {
		t.Fatal("the injected failure was not reported")
	}
	st.hooks.afterSenderMerge = nil
	if got := tableDump(t, st); got != before {
		t.Errorf("a failed merge changed the data:\n%s\nwas\n%s", got, before)
	}
}

func TestMergeTwiceIsANoOp(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st, msg(lidAlice, "L1", lidAlice, at(1)), msg(groupG, "G1", lidAlice, at(2)))
	merge(t, st)
	once := tableDump(t, st)
	merge(t, st)
	if tableDump(t, st) != once {
		t.Error("the second merge changed the data")
	}
}

func TestMergeValidation(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	for name, c := range map[string][2]string{
		"into itself":      {lidAlice, lidAlice},
		"source not a LID": {alice, bob},
		"target not phone": {lidAlice, groupG},
		"target is a LID":  {lidAlice, "100000000043@lid"},
		"malformed":        {"x", alice},
	} {
		err := st.MergeLID(context.Background(), c[0], c[1])
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want a validation error", name, err)
		}
	}
}

func TestMergeUnderLowDisk(t *testing.T) {
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return diskReserve - 1, nil },
	}})
	// Seed around the guard, which is for writes the store makes.
	exec(t, st.writer, "INSERT INTO chats (jid) VALUES ('"+lidAlice+"')")
	before := tableDump(t, st)
	err := st.MergeLID(context.Background(), lidAlice, alice)
	if !errors.Is(err, ErrDiskLow) {
		t.Fatalf("MergeLID = %v, want ErrDiskLow", err)
	}
	if tableDump(t, st) != before {
		t.Error("a refused merge changed the data")
	}
}
