package store

import (
	"context"
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
