package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func insertLIDMap(t testing.TB, st *Store, lidUser, phone string) {
	t.Helper()
	if _, err := st.writer.Exec(`INSERT INTO whatsmeow_lid_map (lid, pn) VALUES (?, ?)`, lidUser, phone); err != nil {
		t.Fatal(err)
	}
}

func TestPhoneForLID(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := t.Context()
	insertLIDMap(t, st, "700000000001", "491511000001")

	phone, ok, err := st.PhoneForLID(ctx, lidJID("700000000001"))
	if err != nil || !ok || phone != phoneJID("491511000001") {
		t.Errorf("mapped = %q, %v, %v; want the phone JID", phone, ok, err)
	}
	phone, ok, err = st.PhoneForLID(ctx, "700000000001:4@lid")
	if err != nil || !ok || phone != phoneJID("491511000001") {
		t.Errorf("with a device part = %q, %v, %v; want the phone JID", phone, ok, err)
	}
	phone, ok, err = st.PhoneForLID(ctx, lidJID("700000000002"))
	if err != nil || ok || phone != "" {
		t.Errorf("unmapped = %q, %v, %v; want no mapping", phone, ok, err)
	}
	for _, bad := range []string{phoneJID("491511000001"), groupOne, "", "x@evil.example"} {
		_, _, err := st.PhoneForLID(ctx, bad)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("PhoneForLID(%q) error = %v, want a *ValidationError", bad, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := st.PhoneForLID(cancelled, lidJID("700000000001")); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v, want context.Canceled", err)
	}
	st.Close(context.Background())
	if _, _, err := st.PhoneForLID(ctx, lidJID("700000000001")); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: %v, want ErrClosed", err)
	}
}

func TestMappedLIDsWithData(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := t.Context()

	for _, bad := range []int{0, 201, -1} {
		if _, err := st.MappedLIDsWithData(ctx, "", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("limit %d: %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := st.MappedLIDsWithData(ctx, phoneJID("1"), 10); !errors.Is(err, ErrInvalid) {
		t.Errorf("a phone cursor: %v, want ErrInvalid", err)
	}

	// 1,000 mappings with data: half as a message sender in a group, half as a chat of their own. Every
	// third LID also has a mapping without data next to it, which must be skipped.
	want := map[string]string{}
	var msgs []Message
	for i := range 1000 {
		lid, phone := fmt.Sprintf("7%011d", i*10), fmt.Sprintf("49151%07d", i)
		insertLIDMap(t, st, lid, phone)
		want[lidJID(lid)] = phoneJID(phone)
		if i%2 == 0 {
			msgs = append(msgs, msg(groupOne, fmt.Sprintf("g%d", i), lidJID(lid), at(i)))
		} else {
			msgs = append(msgs, msg(lidJID(lid), fmt.Sprintf("d%d", i), lidJID(lid), at(i)))
		}
		if i%3 == 0 {
			insertLIDMap(t, st, fmt.Sprintf("7%011d", i*10+1), fmt.Sprintf("49152%07d", i))
		}
	}
	mustStore(t, st, msgs...)

	seen := map[string]bool{}
	after := ""
	for page := 0; ; page++ {
		got, err := st.MappedLIDsWithData(ctx, after, 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) > 200 {
			t.Fatalf("page of %d", len(got))
		}
		if len(got) == 0 {
			break
		}
		for _, m := range got {
			if m.LIDJID <= after {
				t.Fatalf("%s is not after the cursor %s", m.LIDJID, after)
			}
			if seen[m.LIDJID] {
				t.Fatalf("%s repeated", m.LIDJID)
			}
			if want[m.LIDJID] != m.PhoneJID {
				t.Fatalf("%s maps to %s, want %s", m.LIDJID, m.PhoneJID, want[m.LIDJID])
			}
			seen[m.LIDJID] = true
			after = m.LIDJID
		}
		// A mapping inserted behind the cursor is not returned; one ahead of it with data is.
		insertLIDMap(t, st, fmt.Sprintf("6%011d", page), fmt.Sprintf("49153%07d", page))
		mustStore(t, st, msg(groupOne, fmt.Sprintf("behind%d", page), lidJID(fmt.Sprintf("6%011d", page)), at(0)))
	}
	if len(seen) != len(want) {
		t.Errorf("returned %d mappings, want %d", len(seen), len(want))
	}
}
