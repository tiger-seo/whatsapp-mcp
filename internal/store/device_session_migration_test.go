package store

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestDeviceMigrationRepeatsWithoutLifetimeCache(t *testing.T) {
	_, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	pn := types.NewJID("491511234567", types.DefaultUserServer)
	lid := types.NewJID("100000000042", types.HiddenUserServer)
	if err := dev.Sessions.MigratePNToLID(ctx, pn, lid); err != nil {
		t.Fatal(err)
	}
	if err := dev.Sessions.PutSession(ctx, sessionPN, sessionBlob(1)); err != nil {
		t.Fatal(err)
	}
	if err := dev.Sessions.MigratePNToLID(ctx, pn, lid); err != nil {
		t.Fatal(err)
	}
	got, err := dev.Sessions.GetSession(ctx, sessionLID)
	if err != nil || string(got) != string(sessionBlob(1)) {
		t.Fatalf("repeated migration = %x, %v", got, err)
	}
}

func TestDeviceMigrationReusesOuterTransaction(t *testing.T) {
	_, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	pn := types.NewJID("491511234567", types.DefaultUserServer)
	lid := types.NewJID("100000000042", types.HiddenUserServer)
	if err := dev.Sessions.PutSession(ctx, sessionPN, sessionBlob(1)); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("rollback migration")
	err := dev.EventBuffer.DoDecryptionTxn(ctx, func(tx context.Context) error {
		if err := dev.Sessions.MigratePNToLID(tx, pn, lid); err != nil {
			return err
		}
		return stop
	})
	if err == nil {
		t.Fatalf("transaction result = %v", err)
	}
	if got, err := dev.Sessions.GetSession(ctx, sessionPN); err != nil || string(got) != string(sessionBlob(1)) {
		t.Fatalf("PN session after rollback = %x, %v", got, err)
	}
	if got, err := dev.Sessions.GetSession(ctx, sessionLID); err != nil || got != nil {
		t.Fatalf("LID session after rollback = %x, %v", got, err)
	}
}
