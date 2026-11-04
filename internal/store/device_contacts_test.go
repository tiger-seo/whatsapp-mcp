package store

import (
	"context"
	"errors"
	"testing"

	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

func TestDeviceContactReadsCommittedChangesWithoutCache(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	jid := types.NewJID("491511234567", types.DefaultUserServer)
	if got, err := dev.Contacts.GetContact(ctx, jid); err != nil || got.Found {
		t.Fatalf("initial contact = %+v, %v", got, err)
	}
	if _, err := st.writer.Exec(`INSERT INTO whatsmeow_contacts (our_jid, their_jid, push_name) VALUES (?, ?, ?)`, dev.ID.String(), jid.String(), "fresh"); err != nil {
		t.Fatal(err)
	}
	got, err := dev.Contacts.GetContact(ctx, jid)
	if err != nil || !got.Found || got.PushName != "fresh" {
		t.Fatalf("contact after committed insert: %+v, %v", got, err)
	}
	changed, old, err := dev.Contacts.PutPushName(ctx, jid, "next")
	if err != nil || !changed || old != "fresh" {
		t.Fatalf("updated push name = %v, %q, %v", changed, old, err)
	}
	changed, old, err = dev.Contacts.PutBusinessName(ctx, jid, "business")
	if err != nil || !changed || old != "" {
		t.Fatalf("initial business name = %v, %q, %v", changed, old, err)
	}
	changed, old, err = dev.Contacts.PutBusinessName(ctx, jid, "next business")
	if err != nil || !changed || old != "business" {
		t.Fatalf("updated business name = %v, %q, %v", changed, old, err)
	}
}

func TestDeviceContactBulkRollsBackOnSQLFailure(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	if _, err := st.writer.Exec(`CREATE TRIGGER reject_test_contact BEFORE INSERT ON whatsmeow_contacts WHEN NEW.full_name = 'reject' BEGIN SELECT RAISE(ABORT, 'synthetic'); END`); err != nil {
		t.Fatal(err)
	}
	entries := []wastore.ContactEntry{
		{JID: types.NewJID("491511234567", types.DefaultUserServer), FullName: "first"},
		{JID: types.NewJID("491511234568", types.DefaultUserServer), FullName: "reject"},
	}
	if err := dev.Contacts.PutAllContactNames(context.Background(), entries); err == nil {
		t.Fatal("expected synthetic SQL failure")
	}
	if n := countRows(t, st, "whatsmeow_contacts"); n != 0 {
		t.Fatalf("failed batch retained %d contacts", n)
	}
}

func TestDeviceContactNestedTransactionRollback(t *testing.T) {
	_, dev, _ := loadedDevice(t, newDataDir(t))
	jid := types.NewJID("491511234567", types.DefaultUserServer)
	stop := ErrInvalid
	err := dev.EventBuffer.DoDecryptionTxn(context.Background(), func(tx context.Context) error {
		changed, _, err := dev.Contacts.PutPushName(tx, jid, "temporary")
		if err != nil || !changed {
			t.Errorf("nested write = %v, %v", changed, err)
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("transaction result = %v", err)
	}
	got, err := dev.Contacts.GetContact(context.Background(), jid)
	if err != nil || got.Found {
		t.Fatalf("rolled-back contact = %+v, %v", got, err)
	}
}
