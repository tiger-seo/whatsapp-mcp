package store

import (
	"errors"
	"strings"
	"testing"
)

func TestMCPContactQueriesSearch(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	const phone = "15551234567"
	pn, alias, other := phoneJID(phone), lidJID("700000000001"), phoneJID("15551234568")
	seedLIDMapping(t, dev, "700000000001", phone)
	seedContacts(t, dev,
		contactSeed{jid: pn, full: "Same", first: "Phone First"},
		contactSeed{jid: alias, full: "Alias Full", push: "Alias Push", business: "Alias %_+& Business"},
		contactSeed{jid: other, full: "same"},
		contactSeed{jid: groupOne, full: "Same"},
		contactSeed{jid: "123@newsletter", full: "Same"},
	)
	if _, err := st.writer.Exec("INSERT INTO whatsmeow_contacts (our_jid, their_jid) VALUES (?, ?)", dev.ID.String(), lidJID("700000000002")); err != nil {
		t.Fatal(err)
	}
	r := st.ToolReader()
	for _, query := range []string{"PHONE FIRST", "Alias Full", "Alias Push", "%_+& Business", "1234567"} {
		got, err := r.SearchContacts(t.Context(), query, all)
		if err != nil || !equalContacts(got, []Contact{{pn, phone, "Same"}}) {
			t.Errorf("%q: %+v, %v", query, got, err)
		}
	}
	for _, tc := range []struct {
		query string
		want  []Contact
	}{
		{"555", []Contact{{pn, phone, "Same"}, {other, "15551234568", "same"}}},
		{"+1 (555) 123-4567", nil},
		{"same", []Contact{{pn, phone, "Same"}, {other, "15551234568", "same"}}},
		{"700000000002@lid", nil}, // Identity fallback is a display name, not a stored name field.
	} {
		got, err := r.SearchContacts(t.Context(), tc.query, all)
		if err != nil || !equalContacts(got, tc.want) {
			t.Errorf("%q: %+v, %v; want %+v", tc.query, got, err, tc.want)
		}
	}
	for i, jid := range []string{pn, other, ""} {
		got, err := r.SearchContacts(t.Context(), "555", page(1, i))
		if err != nil || (jid == "" && len(got) != 0) || (jid != "" && (len(got) != 1 || got[0].JID != jid)) {
			t.Errorf("page %d: %+v, %v", i, got, err)
		}
	}
	got, err := r.SearchContacts(t.Context(), "", page(1, 0))
	if err != nil || !equalContacts(got, []Contact{{lidJID("700000000002"), "", lidJID("700000000002")}}) {
		t.Errorf("unnamed LID fallback: %+v, %v", got, err)
	}
}

func TestMCPContactQueriesDirectAndMembership(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	const phone = "15551234567"
	pn, alias, other := phoneJID(phone), lidJID("700000000001"), phoneJID("15551234568")
	seedLIDMapping(t, dev, "700000000001", phone)
	mustStore(t, st, msg(alias, "lid", alias, at(1)),
		msg(other, "participation", alias, at(2)), msg(other, "also-participation", pn, at(2)),
		msg(other, "a", other, at(3)), msg(other, "z", other, at(3)),
		msg(groupOne, "group", pn, at(3)))
	r := st.ToolReader()
	inputs := []string{"+1 (555) 123-4567", pn, "15551234567:3@s.whatsapp.net", alias, "700000000001:2@lid"}
	for _, want := range []string{alias, pn} {
		if want == pn {
			mustStore(t, st, msg(pn, "phone", pn, at(1)))
		}
		for _, input := range inputs {
			row, err := r.DirectChat(t.Context(), input)
			if err != nil || row.JID != want || row.Latest == nil || row.Latest.ChatJID != want {
				t.Errorf("direct %q: %+v, %v; want %s", input, row, err, want)
			}
		}
	}
	if _, err := r.DirectChat(t.Context(), "555123456"); !errors.Is(err, ErrNotFound) {
		t.Errorf("partial lookup: %v", err)
	}
	for _, input := range inputs {
		got, err := r.ContactChats(t.Context(), input, all)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, input, chatJIDs(got), []string{groupOne, other, pn, alias})
		for _, row := range got {
			if row.JID == other && (row.Latest == nil || row.Latest.ID != "z" || row.Latest.SenderJID != other) {
				t.Errorf("latest in %s: %+v", other, row.Latest)
			}
		}
	}
}

func TestMCPContactQueriesNameGuard(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	jid := phoneJID("15551234567")
	seedContacts(t, dev, contactSeed{jid: jid, full: "Fits", business: strings.Repeat("x", maxNameBytes+1)})
	if got, err := st.ToolReader().SearchContacts(t.Context(), "Fits", all); !errors.Is(err, ErrResourceLimit) || got != nil {
		t.Fatalf("oversize nonpreferred field: %+v, %v", got, err)
	}
	if saved, real, err := st.ContactNames(t.Context(), jid); !errors.Is(err, ErrResourceLimit) || saved != "" || real != "" {
		t.Fatalf("oversize contact names: %q, %d bytes, %v", saved, len(real), err)
	}
}
