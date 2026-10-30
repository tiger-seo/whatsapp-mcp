package store

import (
	"context"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestListChatsSearch(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	friends, cafe, other := phoneJID("491510000001"), phoneJID("491510000002"), phoneJID("491520000003")
	nameChat(t, st, friends, "привіт друзі", NameSaved)
	nameChat(t, st, cafe, "Café", NameReal) // composed é
	nameChat(t, st, other, "Someone", NameReal)
	ctx := context.Background()

	for _, tc := range []struct{ query, want string }{
		{"ПРИВІТ", friends},
		{"café", cafe}, // decomposed é matches the composed name
		{"CAFÉ", cafe},
		{"491510000001", friends}, // part of the JID
	} {
		got, err := st.ListChats(ctx, tc.query, ByLastActivity, false, all)
		if err != nil {
			t.Fatalf("ListChats(%q): %v", tc.query, err)
		}
		wantIDs(t, "ListChats("+tc.query+")", chatJIDs(got), []string{tc.want})
	}
	got, err := st.ListChats(ctx, "1510000", ByName, false, all)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "two JIDs match", chatJIDs(got), []string{cafe, friends})
	got, err = st.ListChats(ctx, "", ByName, false, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("empty query lists %d chats, want 3", len(got))
	}
}

func TestListChatsSortAndPage(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	a, b, c, d := phoneJID("491510000001"), phoneJID("491510000002"), phoneJID("491510000003"), phoneJID("491510000004")
	mustStore(t, st,
		msg(a, "a1", a, at(3)),
		msg(b, "b1", b, at(5)),
		msg(d, "d1", d, at(5)), // the same activity as b: ordered by JID
	)
	nameChat(t, st, c, "alpha", NameSaved) // no activity
	nameChat(t, st, b, "Beta", NameReal)
	ctx := context.Background()

	got, err := st.ListChats(ctx, "", ByLastActivity, false, all)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "by activity", chatJIDs(got), []string{b, d, a, c})

	got, err = st.ListChats(ctx, "", ByName, false, all)
	if err != nil {
		t.Fatal(err)
	}
	// Named chats by folded name, then unnamed ones by JID, although their folded name '' sorts first.
	wantIDs(t, "by name", chatJIDs(got), []string{c, b, a, d})

	got, err = st.ListChats(ctx, "", ByLastActivity, false, page(2, 1))
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "page 2 from 1", chatJIDs(got), []string{d, a})

	got, err = st.ListChats(ctx, "", ByLastActivity, false, page(10, 4))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("page beyond the end: %q, want empty", chatJIDs(got))
	}
}

func TestListChatsLatest(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	a, empty := phoneJID("491510000001"), phoneJID("491510000002")
	// Stored out of order: the latest is the newest by time, not the last stored.
	mustStore(t, st, msg(a, "m1", a, at(1)), msg(a, "m2", a, at(3)), msg(a, "m3", a, at(2)))
	nameChat(t, st, empty, "No messages", NameSaved)
	ctx := context.Background()

	got, err := st.ListChats(ctx, "", ByLastActivity, true, all)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "chats", chatJIDs(got), []string{a, empty})
	if got[0].Latest == nil || got[0].Latest.ID != "m2" {
		t.Errorf("latest of %s = %+v, want m2", a, got[0].Latest)
	}
	if got[0].LastActivity == nil || !got[0].LastActivity.Equal(at(3)) {
		t.Errorf("last activity = %v, want %v", got[0].LastActivity, at(3))
	}
	if got[1].Latest != nil || got[1].LastActivity != nil {
		t.Errorf("chat without messages: latest %+v, activity %v, want nil", got[1].Latest, got[1].LastActivity)
	}
	if got[1].Name != "No messages" {
		t.Errorf("name = %q", got[1].Name)
	}

	got, err = st.ListChats(ctx, "", ByLastActivity, false, all)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Latest != nil {
		t.Errorf("latest without withLatest: %+v, want nil", got[0].Latest)
	}
}

func TestGetChat(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	g := groupOne
	mustStore(t, st, msg(g, "g1", phoneJID("491510000001"), at(1)), msg(g, "g2", phoneJID("491510000002"), at(2)))
	nameChat(t, st, g, "Team", NameReal)
	ctx := context.Background()

	c, err := st.GetChat(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if c.JID != g || c.Name != "Team" || !c.IsGroup {
		t.Errorf("GetChat = %+v", c)
	}
	if c.Latest == nil || c.Latest.ID != "g2" {
		t.Errorf("latest = %+v, want g2", c.Latest)
	}

	_, err = st.GetChat(ctx, groupTwo)
	if nf := wantNotFound(t, "unknown chat", err); nf != nil && nf.ChatJID != groupTwo {
		t.Errorf("not found names %q, want %q", nf.ChatJID, groupTwo)
	}
}

func TestDirectChat(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	alice := "491511111111"
	aliceLID := "200000000001"
	seedLIDMapping(t, dev, aliceLID, alice)
	mustStore(t, st, msg(phoneJID(alice), "a1", phoneJID(alice), at(1)))
	// Bob has only an unmerged @lid chat, and a mapping.
	bob, bobLID := "491512222222", "200000000002"
	seedLIDMapping(t, dev, bobLID, bob)
	mustStore(t, st, msg(lidJID(bobLID), "b1", lidJID(bobLID), at(2)))
	ctx := context.Background()

	for _, contact := range []string{"+49 151 1111111", alice, phoneJID(alice), lidJID(aliceLID)} {
		c, err := st.DirectChat(ctx, contact)
		if err != nil {
			t.Errorf("DirectChat(%q): %v", contact, err)
			continue
		}
		if c.JID != phoneJID(alice) || c.IsGroup {
			t.Errorf("DirectChat(%q) = %s, want %s", contact, c.JID, phoneJID(alice))
		}
	}
	_, err := st.DirectChat(ctx, "4915111")
	wantNotFound(t, "a partial number", err)

	for _, contact := range []string{bob, lidJID(bobLID)} {
		c, err := st.DirectChat(ctx, contact)
		if err != nil {
			t.Errorf("DirectChat(%q): %v", contact, err)
			continue
		}
		if c.JID != lidJID(bobLID) {
			t.Errorf("DirectChat(%q) = %s, want the @lid chat", contact, c.JID)
		}
	}

	_, err = st.DirectChat(ctx, "not a number")
	wantInvalidFor(t, "a malformed contact", err, "contact")
}

func TestContactChats(t *testing.T) {
	alice, aliceLID := "491511111111", "200000000001"
	carol := phoneJID("491513333333")
	seed := func(t *testing.T, mapped bool) *Store {
		st, _ := openTest(t, newDataDir(t))
		dev := seedDevice(t, st)
		if mapped {
			seedLIDMapping(t, dev, aliceLID, alice)
		}
		mustStore(t, st,
			msg(phoneJID(alice), "d1", phoneJID(alice), at(1)),
			msg(groupOne, "g1", phoneJID(alice), at(2)),
			msg(groupOne, "g2", carol, at(5)), // the group's latest is not Alice's
			msg(groupTwo, "h1", lidJID(aliceLID), at(3)),
			msg("120363000000000003@g.us", "x1", carol, at(4)), // a group Alice never wrote in
		)
		return st
	}
	ctx := context.Background()

	t.Run("mapped", func(t *testing.T) {
		st := seed(t, true)
		for _, contact := range []string{"+49 151 1111111", phoneJID(alice), lidJID(aliceLID)} {
			got, err := st.ContactChats(ctx, contact, all)
			if err != nil {
				t.Fatalf("ContactChats(%q): %v", contact, err)
			}
			wantIDs(t, "ContactChats("+contact+")", chatJIDs(got), []string{groupOne, groupTwo, phoneJID(alice)})
			for _, c := range got {
				if c.Latest == nil {
					t.Errorf("%s has no latest message", c.JID)
				}
			}
			if len(got) == 3 && (got[0].Latest == nil || got[0].Latest.ID != "g2") {
				t.Errorf("latest of %s = %+v, want the chat's real latest g2", groupOne, got[0].Latest)
			}
		}
	})
	t.Run("unmapped", func(t *testing.T) {
		st := seed(t, false)
		got, err := st.ContactChats(ctx, alice, all)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, "by phone", chatJIDs(got), []string{groupOne, phoneJID(alice)})
		got, err = st.ContactChats(ctx, lidJID(aliceLID), all)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, "by LID", chatJIDs(got), []string{groupTwo})
	})
	t.Run("before a merge", func(t *testing.T) {
		st := seed(t, true)
		mustStore(t, st, msg(lidJID(aliceLID), "l1", lidJID(aliceLID), at(6)))
		got, err := st.ContactChats(ctx, alice, all)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, "both direct chats", chatJIDs(got), []string{lidJID(aliceLID), groupOne, groupTwo, phoneJID(alice)})
		c, err := st.DirectChat(ctx, lidJID(aliceLID))
		if err != nil {
			t.Fatal(err)
		}
		if c.JID != phoneJID(alice) {
			t.Errorf("DirectChat with both chats = %s, want the phone chat", c.JID)
		}
	})
}

func TestChatListingPages(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	seedDeviceAs(t, st, types.NewJID(testOwnPhone, types.DefaultUserServer), types.NewJID(testOwnLID, types.HiddenUserServer))
	mustStore(t, st, msg(phoneJID("491511111111"), "a1", phoneJID("491511111111"), at(1)))
	ctx := context.Background()
	for name, p := range badPages {
		_, err := st.ListChats(ctx, "", ByLastActivity, false, p)
		wantInvalidFor(t, "ListChats "+name, err, "")
		_, err = st.ContactChats(ctx, "491511111111", p)
		wantInvalidFor(t, "ContactChats "+name, err, "")
	}
	got, err := st.ContactChats(ctx, "491511111111", page(10, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("ContactChats beyond the end: %q, want empty", chatJIDs(got))
	}
}
