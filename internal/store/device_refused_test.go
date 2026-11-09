package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func TestRefusedCountsNothingBeforeMarkRetiring(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = dev.Identities.PutIdentity(ctx, sessionPN, synthKey())
	if err := dev.Identities.PutIdentity(context.Background(), sessionPN, synthKey()); err != nil {
		t.Fatalf("PutIdentity: %v", err)
	}
	if n := barrier.Refused(); n != 0 {
		t.Fatalf("Refused before MarkRetiring = %d, want 0", n)
	}
}

func TestRefusedDoesNotCountSuccessfulCalls(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	barrier.MarkRetiring()
	if err := dev.Identities.PutIdentity(context.Background(), sessionPN, synthKey()); err != nil {
		t.Fatalf("a call before the barrier retires should still succeed: %v", err)
	}
	if _, err := dev.Sessions.HasSession(context.Background(), sessionPN); err != nil {
		t.Fatalf("HasSession: %v", err)
	}
	if n := barrier.Refused(); n != 0 {
		t.Fatalf("Refused after successful calls = %d, want 0", n)
	}
}

func TestRefusedCountsCancelledContextAfterMarkRetiring(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	barrier.MarkRetiring()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// The shape that matters most: an app-state key write whose context was cancelled after retirement began.
	err := dev.Identities.PutIdentity(ctx, sessionPN, synthKey())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write: %v, want context.Canceled", err)
	}
	if n := barrier.Refused(); n != 1 {
		t.Fatalf("Refused after one cancelled write = %d, want 1", n)
	}
	expired, stop := context.WithDeadline(context.Background(), deadlineInPast())
	defer stop()
	if err := dev.Sessions.DeleteAllSessions(expired, "491511234568"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired write: %v, want DeadlineExceeded", err)
	}
	if n := barrier.Refused(); n != 2 {
		t.Fatalf("Refused after a deadline error = %d, want 2", n)
	}
}

func TestRefusedCountsEveryRetiredSiteExactlyOnce(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	barrier.MarkRetiring()
	if err := barrier.Retire(ctx); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	// Each refused call is counted once: a write in Refused, a read in RefusedReads.
	var want uint64
	expect := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrRetired) {
			t.Errorf("%s after retire: %v, want ErrRetired", name, err)
			return
		}
		want++
		if got := barrier.Refused() + barrier.RefusedReads(); got != want {
			t.Errorf("Refused+RefusedReads after %s = %d, want %d", name, got, want)
		}
	}
	armFreshDevice(dev, types.NewJID(testOwnPhone, types.DefaultUserServer))
	expect("Save", dev.Save(ctx))
	expect("Delete", dev.Delete(ctx))
	expect("DoDecryptionTxn", dev.EventBuffer.DoDecryptionTxn(ctx, func(context.Context) error { return nil }))
	for _, call := range everyDeviceCall() {
		if call.unsupported {
			continue
		}
		expect(call.name, call.call(ctx, dev))
	}
	_, _, err := barrier.BeginRepair(ctx, repairCollections[0])
	expect("BeginRepair", err)
	_, err = barrier.PendingRepairs(ctx)
	expect("PendingRepairs", err)
	expect("ConfigureRecovery", barrier.ConfigureRecovery(ctx))
}

// A read turned away because the barrier has retired cannot change the store, and by then the generation's
// network is already closed, so it is kept out of Refused (the quiet check's storage condition). A write turned
// away the same way is still counted there.
func TestRefusedKeepsReadsAfterRetireApart(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	barrier.MarkRetiring()
	if err := barrier.Retire(ctx); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if _, _, err := dev.AppState.GetAppStateVersion(ctx, "regular"); !errors.Is(err, ErrRetired) {
		t.Fatalf("GetAppStateVersion after retire: %v, want ErrRetired", err)
	}
	if _, err := dev.Sessions.GetSession(ctx, sessionPN); !errors.Is(err, ErrRetired) {
		t.Fatalf("GetSession after retire: %v, want ErrRetired", err)
	}
	if r, rr := barrier.Refused(), barrier.RefusedReads(); r != 0 || rr != 2 {
		t.Fatalf("after two refused reads: Refused=%d RefusedReads=%d, want 0 and 2", r, rr)
	}
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); !errors.Is(err, ErrRetired) {
		t.Fatalf("PutAppStateVersion after retire: %v, want ErrRetired", err)
	}
	if r, rr := barrier.Refused(), barrier.RefusedReads(); r != 1 || rr != 2 {
		t.Fatalf("after a refused write: Refused=%d RefusedReads=%d, want 1 and 2", r, rr)
	}
}

// A read that fails on its context after retirement began is still counted: it can happen before the network
// is closed, and the library may act on the failure.
func TestRefusedCountsReadsFailingOnTheirContext(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	barrier.MarkRetiring()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dev.Sessions.GetSession(ctx, sessionPN); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v, want context.Canceled", err)
	}
	if r, rr := barrier.Refused(), barrier.RefusedReads(); r != 1 || rr != 0 {
		t.Fatalf("after a cancelled read: Refused=%d RefusedReads=%d, want 1 and 0", r, rr)
	}
}

func TestRefusedCountsStaleLease(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	var stale context.Context
	if err := dev.EventBuffer.DoDecryptionTxn(ctx, func(tx context.Context) error {
		stale = tx
		return nil
	}); err != nil {
		t.Fatalf("DoDecryptionTxn: %v", err)
	}
	barrier.MarkRetiring()
	if err := dev.Identities.PutIdentity(stale, sessionPN, synthKey()); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("a call on a closed lease: %v, want ErrStaleLease", err)
	}
	if n := barrier.Refused(); n != 1 {
		t.Fatalf("Refused after a stale lease = %d, want 1", n)
	}
}

func TestRefusedIgnoresResultsTheDatabaseReturns(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	barrier.MarkRetiring()
	// A lookup of an absent row is the database's answer, not a refusal.
	_, _ = dev.Sessions.GetSession(context.Background(), sessionPN2)
	if n := barrier.Refused(); n != 0 {
		t.Fatalf("Refused after a database answer = %d, want 0", n)
	}
}

func TestRefusedIsMonotonic(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	barrier.MarkRetiring()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var last uint64
	for i := 0; i < 5; i++ {
		_ = dev.Identities.PutIdentity(ctx, sessionPN, synthKey())
		got := barrier.Refused()
		if got <= last {
			t.Fatalf("Refused went %d -> %d, want a rise", last, got)
		}
		last = got
	}
}

func deadlineInPast() time.Time { return time.Now().Add(-time.Second) }
