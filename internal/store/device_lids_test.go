package store

import (
	"context"
	"slices"
	"strings"
	"testing"

	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

func TestDeviceLIDReadsCommittedChangesWithoutCache(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	pn := types.NewJID("491511234567", types.DefaultUserServer)
	if got, err := dev.LIDs.GetLIDForPN(ctx, pn); err != nil || !got.IsEmpty() {
		t.Fatalf("initial LID = %v, %v", got, err)
	}
	lid := types.NewJID("100000000042", types.HiddenUserServer)
	if got, err := dev.LIDs.GetPNForLID(ctx, lid); err != nil || !got.IsEmpty() {
		t.Fatalf("initial PN = %v, %v", got, err)
	}
	if _, err := st.writer.Exec(`INSERT INTO whatsmeow_lid_map (lid, pn) VALUES (?, ?)`, "100000000042", pn.User); err != nil {
		t.Fatal(err)
	}
	got, err := dev.LIDs.GetLIDForPN(ctx, pn)
	if err != nil || got.User != "100000000042" || got.Server != types.HiddenUserServer {
		t.Fatalf("LID after committed insert = %v, %v", got, err)
	}
	gotPN, err := dev.LIDs.GetPNForLID(ctx, lid)
	if err != nil || gotPN.User != pn.User || gotPN.Server != types.DefaultUserServer {
		t.Fatalf("PN after committed insert = %v, %v", gotPN, err)
	}
}

func TestDeviceLIDBulkRollsBackOnSQLFailure(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	if _, err := st.writer.Exec(`CREATE TRIGGER reject_test_lid BEFORE INSERT ON whatsmeow_lid_map WHEN NEW.lid = '2' BEGIN SELECT RAISE(ABORT, 'synthetic'); END`); err != nil {
		t.Fatal(err)
	}
	mappings := []wastore.LIDMapping{
		{LID: types.NewJID("1", types.HiddenUserServer), PN: types.NewJID("10", types.DefaultUserServer)},
		{LID: types.NewJID("2", types.HiddenUserServer), PN: types.NewJID("20", types.DefaultUserServer)},
	}
	if err := dev.LIDs.PutManyLIDMappings(context.Background(), mappings); err == nil {
		t.Fatal("expected synthetic SQL failure")
	}
	if n := countRows(t, st, "whatsmeow_lid_map"); n != 0 {
		t.Fatalf("failed batch retained %d mappings", n)
	}
}

type mappingNote struct{ lid, old, new string }

// The guarded LID store reports each inserted or changed mapping once, after the write committed and without a
// store lock held, and nothing for an unchanged upsert, a failed write or a refused one.
func TestDeviceLIDMappingObserver(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	lid := func(u string) types.JID { return types.NewJID(u, types.HiddenUserServer) }
	pn := func(u string) types.JID { return types.NewJID(u, types.DefaultUserServer) }

	// A nil observer is allowed.
	barrier.SetMappingObserver(nil)
	if err := dev.LIDs.PutLIDMapping(ctx, lid("1"), pn("10")); err != nil {
		t.Fatal(err)
	}

	var notes []mappingNote
	barrier.SetMappingObserver(func(l, old, new string) {
		// Committed and no lock held: a store read and a guarded device read both complete.
		got, ok, err := st.PhoneForLID(ctx, l)
		if err != nil || !ok || got != new {
			t.Errorf("observer for %s sees %q, %v, %v; want the committed %q", l, got, ok, err, new)
		}
		if _, err := dev.LIDs.GetPNForLID(ctx, types.NewJID(strings.TrimSuffix(l, "@lid"), types.HiddenUserServer)); err != nil {
			t.Errorf("guarded read inside the observer: %v", err)
		}
		notes = append(notes, mappingNote{l, old, new})
	})
	expect := func(step string, want ...mappingNote) {
		t.Helper()
		if !slices.Equal(notes, want) {
			t.Errorf("%s: notes = %v, want %v", step, notes, want)
		}
		notes = nil
	}

	if err := dev.LIDs.PutLIDMapping(ctx, lid("1"), pn("10")); err != nil {
		t.Fatal(err)
	}
	expect("unchanged upsert")
	if err := dev.LIDs.PutLIDMapping(ctx, lid("2"), pn("20")); err != nil {
		t.Fatal(err)
	}
	expect("new row", mappingNote{"2@lid", "", "20@s.whatsapp.net"})
	if err := dev.LIDs.PutLIDMapping(ctx, lid("2"), pn("21")); err != nil {
		t.Fatal(err)
	}
	expect("changed phone", mappingNote{"2@lid", "20@s.whatsapp.net", "21@s.whatsapp.net"})

	err := dev.LIDs.PutManyLIDMappings(ctx, []wastore.LIDMapping{
		{LID: lid("1"), PN: pn("10")}, {LID: lid("3"), PN: pn("30")}, {LID: lid("2"), PN: pn("22")}, {LID: lid("3"), PN: pn("30")},
	})
	if err != nil {
		t.Fatal(err)
	}
	expect("bulk", mappingNote{"3@lid", "", "30@s.whatsapp.net"}, mappingNote{"2@lid", "21@s.whatsapp.net", "22@s.whatsapp.net"})

	if _, err := st.writer.Exec(`CREATE TRIGGER reject_obs_lid BEFORE INSERT ON whatsmeow_lid_map WHEN NEW.lid = '5' BEGIN SELECT RAISE(ABORT, 'synthetic'); END`); err != nil {
		t.Fatal(err)
	}
	if err := dev.LIDs.PutManyLIDMappings(ctx, []wastore.LIDMapping{{LID: lid("4"), PN: pn("40")}, {LID: lid("5"), PN: pn("50")}}); err == nil {
		t.Fatal("expected the synthetic failure")
	}
	expect("failed bulk write")
	if err := dev.LIDs.PutLIDMapping(ctx, lid("5"), pn("50")); err == nil {
		t.Fatal("expected the synthetic failure")
	}
	expect("failed write")

	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dev.LIDs.PutLIDMapping(ctx, lid("6"), pn("60")); err == nil {
		t.Fatal("a write after Retire succeeded")
	}
	expect("refused write")
}
