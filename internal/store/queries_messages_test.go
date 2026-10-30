package store

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// Bob writes under his phone number and under a mapped LID; Carol only under her phone number.
const (
	bobPhone = "491513333333"
	bobLID   = "300000000003"
)

var (
	bobChat = phoneJID(bobPhone)
	carol   = phoneJID("491514444444")
)

// listingFixture is a store with Bob's and Carol's messages across three chats, with equal times across chats.
func listingFixture(t *testing.T) (*Store, []Message) {
	t.Helper()
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	seedLIDMapping(t, dev, bobLID, bobPhone)
	msgs := []Message{
		msg(bobChat, "b1", bobChat, at(1), withText("hello there")),
		msg(bobChat, "b2", ownJID, at(2), withText("reply"), fromMe()),
		msg(groupOne, "g1", lidJID(bobLID), at(3), withText("Été arrive")),
		msg(groupOne, "g2", carol, at(4), withText("100% sure_thing")),
		msg(groupOne, "g3", bobChat, at(5), withText(""), withCaption("photo caption")),
		msg(groupOne, "g4", carol, at(5), withText(`it's a \ backslash`)),
		msg(groupTwo, "h1", lidJID(bobLID), at(5), withText("été decomposed")),
		msg(groupTwo, "h2", carol, at(6), withText("late")),
	}
	mustStore(t, st, msgs...)
	return st, msgs
}

// expectedListing filters and orders msgs as ListMessages must: strict times, chat, Bob under either identity,
// text or caption containing the folded query; newest first, ties by chat JID, then ID, descending.
func expectedListing(msgs []Message, f MessageFilter, bob bool) []string {
	var out []Message
	for _, m := range msgs {
		switch {
		case !f.After.IsZero() && !m.Time.After(f.After),
			!f.Before.IsZero() && !m.Time.Before(f.Before),
			f.ChatJID != "" && m.ChatJID != f.ChatJID,
			bob && m.SenderJID != bobChat && m.SenderJID != lidJID(bobLID),
			f.Query != "" && !strings.Contains(fold(m.Text), fold(f.Query)) && !strings.Contains(fold(m.Caption), fold(f.Query)):
			continue
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b Message) int {
		return cmp.Or(b.Time.Compare(a.Time), cmp.Compare(b.ChatJID, a.ChatJID), cmp.Compare(b.ID, a.ID))
	})
	ids := make([]string, len(out))
	for i, m := range out {
		ids[i] = m.ID
	}
	return ids
}

func TestListMessagesFilters(t *testing.T) {
	st, msgs := listingFixture(t)
	ctx := context.Background()
	for _, after := range []time.Time{{}, at(2)} {
		for _, before := range []time.Time{{}, at(5)} {
			for _, chat := range []string{"", groupOne} {
				for _, sender := range []string{"", "+49 151 3333333", bobChat, lidJID(bobLID)} {
					for _, query := range []string{"", "E"} {
						f := MessageFilter{After: after, Before: before, ChatJID: chat, Sender: sender, Query: query}
						name := fmt.Sprintf("after=%v before=%v chat=%q sender=%q query=%q", !after.IsZero(), !before.IsZero(), chat, sender, query)
						got, err := st.ListMessages(ctx, f, Context{}, all)
						if err != nil {
							t.Fatalf("%s: %v", name, err)
						}
						wantIDs(t, name, hitIDs(got), expectedListing(msgs, f, sender != ""))
					}
				}
			}
		}
	}
}

func TestListMessagesPaging(t *testing.T) {
	st, msgs := listingFixture(t)
	ctx := context.Background()
	for _, f := range []MessageFilter{{}, {Sender: bobPhone}, {ChatJID: groupOne}} {
		want := expectedListing(msgs, f, f.Sender != "")
		var got []string
		for offset := 0; ; offset += 2 {
			hits, err := st.ListMessages(ctx, f, Context{}, page(2, offset))
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) == 0 {
				break
			}
			got = append(got, hitIDs(hits)...)
		}
		wantIDs(t, fmt.Sprintf("all pages of %+v", f), got, want)
	}
}

func TestListMessagesSearch(t *testing.T) {
	st, _ := listingFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"100%", []string{"g2"}},
		{"%", []string{"g2"}},
		{"_", []string{"g2"}},
		{"'", []string{"g4"}},
		{`\`, []string{"g4"}},
		{"PHOTO", []string{"g3"}},       // the caption
		{"ÉTÉ", []string{"h1", "g1"}},   // composed and decomposed é match each other
		{"été", []string{"h1", "g1"}}, // a decomposed query too
		{"ete", nil},                    // but é is not e
		{"sure_thing", []string{"g2"}},
		{"sure%thing", nil},
	} {
		got, err := st.ListMessages(ctx, MessageFilter{Query: tc.query}, Context{}, all)
		if err != nil {
			t.Fatalf("query %q: %v", tc.query, err)
		}
		wantIDs(t, "query "+tc.query, hitIDs(got), tc.want)
	}
}

func TestListMessagesAfterBeyondBefore(t *testing.T) {
	st, _ := listingFixture(t)
	got, err := st.ListMessages(context.Background(), MessageFilter{After: at(5), Before: at(2)}, Context{}, all)
	if err != nil {
		t.Fatalf("after > before: %v, want an empty result", err)
	}
	if len(got) != 0 {
		t.Errorf("after > before: %q, want empty", hitIDs(got))
	}
}

func TestListMessagesChatAndSender(t *testing.T) {
	st, _ := listingFixture(t)
	ctx := context.Background()
	for _, sender := range []string{bobPhone, bobChat, lidJID(bobLID)} {
		got, err := st.ListMessages(ctx, MessageFilter{ChatJID: groupOne, Sender: sender}, Context{}, all)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, "Bob in group one by "+sender, hitIDs(got), []string{"g3", "g1"})
	}
	got, err := st.ListMessages(ctx, MessageFilter{ChatJID: groupOne, Sender: carol}, Context{}, all)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "Carol in group one", hitIDs(got), []string{"g4", "g2"})
}

func TestListMessagesBothIdentitiesAtOneTime(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	seedLIDMapping(t, dev, bobLID, bobPhone)
	same := at(7)
	mustStore(t, st,
		msg(groupOne, "x1", bobChat, same),
		msg(groupOne, "x2", lidJID(bobLID), same),
		msg(groupOne, "x3", bobChat, same),
		msg(groupTwo, "y1", lidJID(bobLID), same),
		msg(groupTwo, "y2", bobChat, same),
	)
	want := []string{"y2", "y1", "x3", "x2", "x1"} // groupTwo > groupOne, then ID descending
	ctx := context.Background()
	got, err := st.ListMessages(ctx, MessageFilter{Sender: bobPhone}, Context{}, all)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "one listing", hitIDs(got), want)
	var paged []string
	for offset := 0; offset < 10; offset++ {
		hits, err := st.ListMessages(ctx, MessageFilter{Sender: lidJID(bobLID)}, Context{}, page(1, offset))
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, hitIDs(hits)...)
	}
	wantIDs(t, "pages of one", paged, want)
}

func TestMessageContext(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	chat := phoneJID("491515555555")
	var msgs []Message
	for i := 1; i <= 10; i++ {
		msgs = append(msgs, msg(chat, fmt.Sprintf("c%02d", i), chat, at(i), withText(fmt.Sprintf("ctx %d", i))))
		// Another chat at the same times, which must never appear in the context.
		msgs = append(msgs, msg(groupOne, fmt.Sprintf("o%02d", i), chat, at(i)))
	}
	mustStore(t, st, msgs...)
	ctx := context.Background()

	h, err := st.MessageContext(ctx, chat, "c05", Context{Before: 2, After: 3})
	if err != nil {
		t.Fatal(err)
	}
	if h.Message.ID != "c05" {
		t.Errorf("hit = %s", h.Message.ID)
	}
	wantIDs(t, "before", messageIDs(h.Before), []string{"c03", "c04"})
	wantIDs(t, "after", messageIDs(h.After), []string{"c06", "c07", "c08"})

	h, err = st.MessageContext(ctx, chat, "c01", Context{Before: 20, After: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Before) != 0 || len(h.After) != 0 {
		t.Errorf("oldest message: before %q, after %q, want none", messageIDs(h.Before), messageIDs(h.After))
	}

	hits, err := st.ListMessages(ctx, MessageFilter{ChatJID: chat, Query: "ctx 9"}, Context{Before: 1, After: 5}, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %q", hitIDs(hits))
	}
	wantIDs(t, "listing before", messageIDs(hits[0].Before), []string{"c08"})
	wantIDs(t, "listing after", messageIDs(hits[0].After), []string{"c10"})

	for _, c := range []Context{{Before: -1}, {After: -1}, {Before: maxContextSide + 1}, {After: maxContextSide + 1}} {
		_, err := st.MessageContext(ctx, chat, "c05", c)
		wantInvalidFor(t, fmt.Sprintf("context %+v", c), err, "")
		_, err = st.ListMessages(ctx, MessageFilter{}, c, all)
		wantInvalidFor(t, fmt.Sprintf("listing context %+v", c), err, "")
	}
	_, err = st.MessageContext(ctx, chat, "missing", Context{})
	wantNotFound(t, "unknown message", err)
	for name, p := range badPages {
		_, err := st.ListMessages(ctx, MessageFilter{}, Context{}, p)
		wantInvalidFor(t, "ListMessages "+name, err, "")
	}
}

func TestLastInteraction(t *testing.T) {
	dave, daveLID := "491515555555", "500000000005"
	ctx := context.Background()
	seed := func(t *testing.T) *Store {
		st, _ := openTest(t, newDataDir(t))
		dev := seedDevice(t, st)
		seedLIDMapping(t, dev, daveLID, dave)
		mustStore(t, st,
			msg(phoneJID(dave), "d1", phoneJID(dave), at(1)),
			msg(phoneJID(dave), "d2", ownJID, at(2), fromMe()),
			msg(groupOne, "d3", lidJID(daveLID), at(3)), // a group message under the LID
			msg(groupOne, "c1", carol, at(9)),           // someone else, later
		)
		return st
	}

	t.Run("group message under the LID", func(t *testing.T) {
		st := seed(t)
		for _, contact := range []string{"+49 151 5555555", phoneJID(dave), lidJID(daveLID)} {
			m, err := st.LastInteraction(ctx, contact)
			if err != nil {
				t.Fatal(err)
			}
			if m == nil || m.ID != "d3" {
				t.Errorf("LastInteraction(%q) = %+v, want d3", contact, m)
			}
		}
	})
	t.Run("newer message in the @lid chat", func(t *testing.T) {
		st := seed(t)
		mustStore(t, st, msg(lidJID(daveLID), "l1", ownJID, at(4), fromMe()))
		m, err := st.LastInteraction(ctx, dave)
		if err != nil {
			t.Fatal(err)
		}
		if m == nil || m.ID != "l1" {
			t.Errorf("LastInteraction = %+v, want l1 from the @lid chat", m)
		}
	})
	t.Run("none", func(t *testing.T) {
		st := seed(t)
		m, err := st.LastInteraction(ctx, "491519999999")
		if err != nil || m != nil {
			t.Errorf("unknown contact: %+v, %v, want nil, nil", m, err)
		}
	})
	t.Run("ties", func(t *testing.T) {
		st := seed(t)
		frank := "491517777777"
		mustStore(t, st,
			msg(phoneJID(frank), "f1", phoneJID(frank), at(10)),
			msg(groupTwo, "f2", phoneJID(frank), at(10)),
		)
		m, err := st.LastInteraction(ctx, frank)
		if err != nil {
			t.Fatal(err)
		}
		// The same time: the greater chat JID wins ("4915…@s.whatsapp.net" > "1203…@g.us").
		if m == nil || m.ID != "f1" {
			t.Errorf("tie = %+v, want f1", m)
		}
		mustStore(t, st, msg(phoneJID(frank), "f3", phoneJID(frank), at(10)))
		if m, err = st.LastInteraction(ctx, frank); err != nil || m == nil || m.ID != "f3" {
			t.Errorf("tie in one chat = %+v, %v, want f3 (greater ID)", m, err)
		}
	})
}

func TestLastInteractionOwnAccount(t *testing.T) {
	alice := phoneJID("491511111111")
	ctx := context.Background()
	seedMessages := func(t *testing.T, st *Store, ownChat bool) {
		mustStore(t, st, msg(alice, "a5", ownJID, at(5), fromMe())) // to Alice, newer
		if ownChat {
			mustStore(t, st, msg(ownJID, "s1", ownJID, at(1), fromMe())) // "message yourself"
		}
	}
	own := types.NewJID(testOwnPhone, types.DefaultUserServer)
	lid := types.NewJID(testOwnLID, types.HiddenUserServer)

	for _, tc := range []struct {
		name string
		own  types.JID
	}{
		{"plain JID", own},
		{"JID with a device part", types.JID{User: testOwnPhone, Device: 7, Server: types.DefaultUserServer}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := openTest(t, newDataDir(t))
			seedDeviceAs(t, st, tc.own, lid)
			seedMessages(t, st, true)
			for _, contact := range []string{testOwnPhone, ownJID, ownLID} {
				m, err := st.LastInteraction(ctx, contact)
				if err != nil {
					t.Fatal(err)
				}
				if m == nil || m.ID != "s1" {
					t.Errorf("LastInteraction(%q) = %+v, want the own chat's s1", contact, m)
				}
			}
		})
	}
	t.Run("no own chat", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		seedDeviceAs(t, st, own, lid)
		seedMessages(t, st, false)
		m, err := st.LastInteraction(ctx, testOwnPhone)
		if err != nil || m != nil {
			t.Errorf("own number without an own chat = %+v, %v, want nil", m, err)
		}
	})
	t.Run("logged out", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		seedMessages(t, st, false)
		m, err := st.LastInteraction(ctx, testOwnPhone)
		if err != nil {
			t.Fatal(err)
		}
		if m != nil && m.ID == "a5" {
			t.Errorf("logged out: returned a5, a message the account sent to someone else")
		}
	})
	for _, tc := range []struct {
		name string
		set  string
	}{{"NULL own LID", "NULL"}, {"empty own LID", "''"}} {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := openTest(t, newDataDir(t))
			seedDeviceAs(t, st, own, types.JID{})
			if _, err := st.writer.Exec("UPDATE whatsmeow_device SET lid = " + tc.set); err != nil {
				t.Fatal(err)
			}
			erin := phoneJID("491516666666") // no LID mapping, so her LID is empty
			mustStore(t, st,
				msg(erin, "e1", erin, at(1)),
				msg(ownJID, "s9", ownJID, at(9), fromMe()),
			)
			m, err := st.LastInteraction(ctx, erin)
			if err != nil {
				t.Fatal(err)
			}
			if m == nil || m.ID != "e1" {
				t.Errorf("unmapped contact with %s = %+v, want e1, not the own chat", tc.name, m)
			}
		})
	}
}

func TestMessageViewFields(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	full, first, push, business := phoneJID("491510000011"), phoneJID("491510000012"), phoneJID("491510000013"), phoneJID("491510000014")
	mappedLID, mappedPhone := "600000000006", "491510000015"
	chatNamed, chatFallback := phoneJID("491510000016"), phoneJID("491510000017")
	seedContacts(t, dev,
		contactSeed{jid: full, full: "Full Name", first: "First", push: "Push", business: "Biz"},
		contactSeed{jid: first, first: "Only First", push: "Push2"},
		contactSeed{jid: push, push: "Only Push", business: "Biz3"},
		contactSeed{jid: business, business: "Only Biz"},
		contactSeed{jid: phoneJID(mappedPhone), full: "Mapped Person"}, // the LID sender is found through the map
	)
	seedLIDMapping(t, dev, mappedLID, mappedPhone)
	nameChat(t, st, groupOne, "Team", NameReal)
	nameChat(t, st, chatNamed, "Gina", NameReal)
	nameChat(t, st, chatFallback, "+49 151 0000017", NameFallback)
	length := int64(1234)
	media := msg(groupOne, "m1", full, at(1), withText(""), withCaption("a caption"))
	media.MediaType = "image"
	media.Media = MediaMeta{URL: "https://example.invalid/x", MediaKey: make([]byte, 32), FileLength: &length, MIMEType: "image/jpeg"}
	mustStore(t, st,
		media,
		msg(groupOne, "m2", first, at(2)),
		msg(groupOne, "m3", push, at(3)),
		msg(groupOne, "m4", business, at(4)),
		msg(groupOne, "m5", lidJID(mappedLID), at(5)),
		msg(groupOne, "m6", chatNamed, at(6)),
		msg(groupOne, "m7", chatFallback, at(7)),
		msg(groupOne, "m8", ownJID, at(8), fromMe()),
	)
	hits, err := st.ListMessages(context.Background(), MessageFilter{ChatJID: groupOne}, Context{}, all)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]MessageView{}
	for _, h := range hits {
		byID[h.Message.ID] = h.Message
	}

	want := MessageView{
		ID: "m1", ChatJID: groupOne, ChatName: "Team",
		SenderJID: full, SenderPhone: "491510000011", SenderName: "Full Name",
		Time: at(1), Text: "", Caption: "a caption", MediaType: "image",
	}
	if got := byID["m1"]; !got.Time.Equal(want.Time) || got.Time.Location() != time.UTC {
		t.Errorf("time = %v, want %v in UTC", got.Time, want.Time)
	} else if got.Time = want.Time; got != want {
		t.Errorf("m1 = %+v\nwant %+v", got, want)
	}
	for id, name := range map[string]string{
		"m2": "Only First", // first before push
		"m3": "Only Push",  // push before business
		"m4": "Only Biz",
		"m5": "Mapped Person",
		"m6": "Gina", // the direct chat's real name
		"m7": "",     // a fallback chat name is not a name
		"m8": "",     // from me: the tools render "Me"
	} {
		if got := byID[id].SenderName; got != name {
			t.Errorf("%s sender name = %q, want %q", id, got, name)
		}
	}
	if m := byID["m5"]; m.SenderJID != lidJID(mappedLID) || m.SenderPhone != "" {
		t.Errorf("m5 sender = %q / %q, want the LID as stored and no phone", m.SenderJID, m.SenderPhone)
	}
	if !byID["m8"].FromMe {
		t.Errorf("m8 is not from me")
	}
}
