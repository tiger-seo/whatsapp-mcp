package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// contactsFixture seeds the device store with contacts that match by each kind of name, by phone, under one or
// both identities, and a group.
func contactsFixture(t *testing.T) *Store {
	t.Helper()
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	seedContacts(t, dev,
		contactSeed{jid: phoneJID("491511000001"), full: "Alice Smith"},
		contactSeed{jid: phoneJID("491511000002"), full: "José"}, // composed é
		contactSeed{jid: phoneJID("491511000003"), push: "Pushy"},
		contactSeed{jid: phoneJID("491511000004"), first: "Firsty"},
		contactSeed{jid: phoneJID("491511000005"), business: "Biz Co"},
		contactSeed{jid: groupOne, push: "Alice group"},
		// Carl under both identities, named in both entries.
		contactSeed{jid: phoneJID("491511000006"), first: "Carl"},
		contactSeed{jid: lidJID("700000000007"), push: "Carl L"},
		// Dana's phone entry has no name, so the name comes from her LID entry.
		contactSeed{jid: lidJID("700000000008"), push: "Dana L"},
		// Erin only under an unmapped LID.
		contactSeed{jid: lidJID("700000000009"), push: "Erin"},
		contactSeed{jid: phoneJID("491511000010"), full: "Bob"},
	)
	if _, err := st.writer.Exec("INSERT INTO whatsmeow_contacts (our_jid, their_jid) VALUES (?, ?)", dev.ID.String(), phoneJID("491511000007")); err != nil {
		t.Fatal(err)
	}
	seedLIDMapping(t, dev, "700000000007", "491511000006")
	seedLIDMapping(t, dev, "700000000008", "491511000007")
	return st
}

func TestSearchContacts(t *testing.T) {
	st := contactsFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		query string
		want  []Contact
	}{
		{"alice", []Contact{{phoneJID("491511000001"), "491511000001", "Alice Smith"}}}, // not the group
		{"JOSÉ", []Contact{{phoneJID("491511000002"), "491511000002", "José"}}},
		{"pushy", []Contact{{phoneJID("491511000003"), "491511000003", "Pushy"}}},
		{"FIRSTY", []Contact{{phoneJID("491511000004"), "491511000004", "Firsty"}}},
		{"biz co", []Contact{{phoneJID("491511000005"), "491511000005", "Biz Co"}}},
		{"1511000001", []Contact{{phoneJID("491511000001"), "491511000001", "Alice Smith"}}},
		{"+49 151 1000001", []Contact{{phoneJID("491511000001"), "491511000001", "Alice Smith"}}},
		// Once, under the phone JID, with the phone entry's name, whichever entry matched.
		{"carl", []Contact{{phoneJID("491511000006"), "491511000006", "Carl"}}},
		{"carl l", []Contact{{phoneJID("491511000006"), "491511000006", "Carl"}}},
		{"dana", []Contact{{phoneJID("491511000007"), "491511000007", "Dana L"}}},
		{"erin", []Contact{{lidJID("700000000009"), "", "Erin"}}},
		// No phone number in the query: names only, so Bob is not matched through an empty digit string.
		{"Alice2", nil},
		{"Bob", []Contact{{phoneJID("491511000010"), "491511000010", "Bob"}}},
	} {
		got, err := st.SearchContacts(ctx, tc.query, all)
		if err != nil {
			t.Fatalf("SearchContacts(%q): %v", tc.query, err)
		}
		if !equalContacts(got, tc.want) {
			t.Errorf("SearchContacts(%q) = %+v, want %+v", tc.query, got, tc.want)
		}
	}
	got, err := st.SearchContacts(ctx, "", all)
	if err != nil {
		t.Fatal(err)
	}
	// Every person once, groups excluded, ordered by folded name: Alice Smith, Biz Co, Bob, Carl, Dana L, Erin,
	// Firsty, José, Pushy.
	if len(got) != 9 || got[0].Name != "Alice Smith" || got[8].Name != "Pushy" {
		t.Errorf("all contacts = %+v", got)
	}
	got, err = st.SearchContacts(ctx, "", page(2, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Biz Co" || got[1].Name != "Bob" {
		t.Errorf("page 2 from 1 = %+v", got)
	}
	for name, p := range badPages {
		_, err := st.SearchContacts(ctx, "", p)
		wantInvalidFor(t, "SearchContacts "+name, err, "")
	}
}

func TestSearchContactsMatchedThroughTheOtherEntry(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	seedContacts(t, dev,
		contactSeed{jid: phoneJID("491511000010"), full: "Bob"},
		contactSeed{jid: lidJID("700000000010"), push: "Alice"},
	)
	seedLIDMapping(t, dev, "700000000010", "491511000010")
	want := []Contact{{phoneJID("491511000010"), "491511000010", "Bob"}}
	for _, q := range []string{"alice", "bob"} {
		got, err := st.SearchContacts(context.Background(), q, all)
		if err != nil {
			t.Fatal(err)
		}
		if !equalContacts(got, want) {
			t.Errorf("SearchContacts(%q) = %+v, want %+v", q, got, want)
		}
	}
}

func TestSearchContactsWithoutDevice(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	got, err := st.SearchContacts(context.Background(), "alice", all)
	if err != nil || len(got) != 0 {
		t.Errorf("never paired: %+v, %v, want an empty result", got, err)
	}
}

func TestResolveContact(t *testing.T) {
	ctx := context.Background()
	resolve := func(t *testing.T, st *Store, s string) (contactIDs, error) {
		t.Helper()
		conn, err := st.reader.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		return resolveContact(ctx, conn, s)
	}
	carl := contactIDs{phone: "491511000006", phoneJID: phoneJID("491511000006"), lidJID: lidJID("700000000007")}

	st := contactsFixture(t)
	for _, tc := range []struct {
		input string
		want  contactIDs
	}{
		{"+49 151 1000006", carl},
		{phoneJID("491511000006"), carl},
		{"491511000006:3@s.whatsapp.net", carl},
		{lidJID("700000000007"), carl},
		{"491511000001", contactIDs{phone: "491511000001", phoneJID: phoneJID("491511000001")}},
		{lidJID("700000000009"), contactIDs{lidJID: lidJID("700000000009")}},
	} {
		got, err := resolve(t, st, tc.input)
		if err != nil {
			t.Errorf("resolveContact(%q): %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveContact(%q) = %+v, want %+v", tc.input, got, tc.want)
		}
	}
	_, err := resolve(t, st, "Alice")
	wantInvalidFor(t, "a name", err, "contact")

	t.Run("no device", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		got, err := resolve(t, st, "491511000006")
		if err != nil {
			t.Fatal(err)
		}
		if want := (contactIDs{phone: "491511000006", phoneJID: phoneJID("491511000006")}); got != want {
			t.Errorf("no device: %+v, want %+v", got, want)
		}
	})
}

func equalContacts(a, b []Contact) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestContactNames(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := t.Context()

	// Without a device there are no names.
	if saved, real, err := st.ContactNames(ctx, phoneJID("491511000001")); err != nil || saved != "" || real != "" {
		t.Errorf("no device = %q, %q, %v", saved, real, err)
	}

	dev := seedDevice(t, st)
	seedContacts(t, dev,
		contactSeed{jid: phoneJID("491511000001"), full: "Alice Smith", first: "Alice", push: "Ali", business: "Ali Co"},
		contactSeed{jid: phoneJID("491511000002"), first: "Bob"},
		contactSeed{jid: phoneJID("491511000003"), business: "Carl Co"},
		contactSeed{jid: lidJID("700000000004"), push: "Dana"},
	)
	for _, c := range []struct{ jid, saved, real string }{
		{phoneJID("491511000001"), "Alice Smith", "Ali"},
		{phoneJID("491511000002"), "Bob", ""},
		{phoneJID("491511000003"), "", "Carl Co"},
		{lidJID("700000000004"), "", "Dana"},
		{phoneJID("491511000009"), "", ""},
	} {
		saved, real, err := st.ContactNames(ctx, c.jid)
		if err != nil || saved != c.saved || real != c.real {
			t.Errorf("ContactNames(%s) = %q, %q, %v; want %q, %q", c.jid, saved, real, err, c.saved, c.real)
		}
	}

	// Another device's row is never read.
	conn, err := st.writer.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"PRAGMA foreign_keys = OFF",
		"INSERT INTO whatsmeow_contacts (our_jid, their_jid, full_name, push_name) VALUES ('1@s.whatsapp.net', '491511000005@s.whatsapp.net', 'Other', 'Other')",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()
	if saved, real, err := st.ContactNames(ctx, phoneJID("491511000005")); err != nil || saved != "" || real != "" {
		t.Errorf("another device's row = %q, %q, %v; want nothing", saved, real, err)
	}

	// A name over 1 KiB in the device table is a resource limit, never truncated.
	long := strings.Repeat("n", maxNameBytes+1)
	if _, err := st.writer.Exec("UPDATE whatsmeow_contacts SET push_name = ? WHERE their_jid = ?", long, lidJID("700000000004")); err != nil {
		t.Fatal(err)
	}
	if saved, real, err := st.ContactNames(ctx, lidJID("700000000004")); !errors.Is(err, ErrResourceLimit) || saved != "" || real != "" {
		t.Errorf("1 KiB + 1 = %q, %d bytes, %v; want ErrResourceLimit", saved, len(real), err)
	}
	if _, _, err := st.ContactNames(ctx, "not a jid"); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid JID: %v, want ErrInvalid", err)
	}
}
