package store

import (
	"context"
	"errors"
	"testing"

	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

func TestFirstSaveTransferredRepairBlocksRacingProgress(t *testing.T) {
	st, dev, barrier := freshDevice(t, newDataDir(t))
	ctx := context.Background()
	if _, err := st.writer.Exec("UPDATE unsafe_run SET repair_bits=8 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := barrier.ConfigureRecovery(ctx); err != nil {
		t.Fatal(err)
	}
	armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
	committed, release := make(chan struct{}), make(chan struct{})
	st.hooks.devicePutCommitted = func() { close(committed); <-release }
	saved := make(chan error, 1)
	go func() { saved <- dev.Save(ctx) }()
	<-committed
	err := dev.AppState.PutAppStateVersion(ctx, "regular", 7, [128]byte{})
	close(release)
	if saveErr := <-saved; saveErr != nil {
		t.Fatal(saveErr)
	}
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("progress before readiness publication: %v", err)
	}
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 8, [128]byte{}); !errors.Is(err, ErrRecoveryPending) {
		t.Fatalf("post-save progress: %v", err)
	}
	if err := dev.AppStateKeys.PutAppStateSyncKey(ctx, []byte("synthetic-key"), wastore.AppStateSyncKey{Data: []byte("synthetic"), Fingerprint: []byte("synthetic"), Timestamp: 1}); err != nil {
		t.Fatalf("key delivery blocked: %v", err)
	}
	var revision int
	var target []byte
	if err := st.reader.QueryRow("SELECT revision, target_version FROM device_repair WHERE collection='regular'").Scan(&revision, &target); err != nil || revision != 1 || target != nil {
		t.Fatalf("repair marker revision=%d target=%x err=%v", revision, target, err)
	}
}

func TestLaterSaveCannotRestoreClearedRecovery(t *testing.T) {
	st, dev, barrier := freshDevice(t, newDataDir(t))
	ctx := context.Background()
	if _, err := st.writer.Exec("UPDATE unsafe_run SET repair_bits=8 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
	if err := dev.Save(ctx); err != nil {
		t.Fatal(err)
	}
	_, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	committed, release := make(chan struct{}), make(chan struct{})
	st.hooks.devicePutCommitted = func() { close(committed); <-release }
	saved := make(chan error, 1)
	go func() { saved <- dev.Save(ctx) }()
	<-committed
	if barrier.sess.repairFence.TryLock() {
		barrier.sess.repairFence.Unlock()
		if err := barrier.CompleteRepair(ctx, ticket); err != nil {
			close(release)
			t.Fatal(err)
		}
		close(release)
		if err := <-saved; err != nil {
			t.Fatal(err)
		}
		if _, _, err := dev.AppState.GetAppStateVersion(ctx, "regular"); err != nil {
			t.Fatalf("stale save closed recovery after repair: %v", err)
		}
		t.Fatal("save did not serialize repair completion")
	}
	completed := make(chan error, 1)
	go func() { completed <- barrier.CompleteRepair(ctx, ticket) }()
	close(release)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	if _, _, err := dev.AppState.GetAppStateVersion(ctx, "regular"); err != nil {
		t.Fatalf("progress after repair: %v", err)
	}
	_, bits, err := st.RunGuard(ctx)
	if err != nil || bits != 0 {
		t.Fatalf("repair bits=%d err=%v", bits, err)
	}
}

func TestSaveFencePreservesNestedAndKeyDelivery(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.EventBuffer.DoDecryptionTxn(ctx, func(tx context.Context) error {
		return dev.Save(tx)
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nested save: %v", err)
	}
	barrier.sess.repairFence.Lock()
	err := dev.AppStateKeys.PutAppStateSyncKey(ctx, []byte("synthetic"), wastore.AppStateSyncKey{Data: []byte("synthetic"), Fingerprint: []byte("synthetic"), Timestamp: 1})
	barrier.sess.repairFence.Unlock()
	if err != nil {
		t.Fatalf("key delivery while fence held: %v", err)
	}
	if got := st.gate.activeCount(); got != 0 {
		t.Fatalf("leaked admission slots: %d", got)
	}
}
