package store

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestRecoveryIdentityPresence(t *testing.T) {
	for _, present := range []bool{false, true} {
		name := "absent"
		if present {
			name = "present"
		}
		t.Run(name, func(t *testing.T) {
			st, _ := openTest(t, newDataDir(t))
			if present {
				seedDevice(t, st)
			}
			mustStore(t, st, msg(alice, "identity-check-sentinel", alice, at(0)))
			if err := st.ArmRunGuard(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := st.RecoveryIdentityPresent(context.Background())
			if err != nil || got != present {
				t.Fatalf("presence=%v err=%v", got, err)
			}
			state, bits, err := st.RunGuard(context.Background())
			if err != nil || state != "unsafe" || bits != 0 || !messageExists(t, st, alice, "identity-check-sentinel") {
				t.Fatalf("state=%q bits=%d err=%v", state, bits, err)
			}
		})
	}
}

func TestRecoveryIdentityPresenceRefusals(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		if _, err := st.RecoveryIdentityPresent(context.Background()); !errors.Is(err, ErrUnsafeRun) {
			t.Fatalf("clean marker: %v", err)
		}
	})
	t.Run("missing marker", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		if _, err := st.writer.Exec("DELETE FROM unsafe_run"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.RecoveryIdentityPresent(context.Background()); !errors.Is(err, ErrUnsafeRun) {
			t.Fatalf("missing marker: %v", err)
		}
	})
	t.Run("malformed marker", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		if _, err := st.writer.Exec("PRAGMA ignore_check_constraints=ON"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.writer.Exec("UPDATE unsafe_run SET state='unsafe', repair_bits=32"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.RecoveryIdentityPresent(context.Background()); !errors.Is(err, ErrUnsafeRun) {
			t.Fatalf("malformed marker: %v", err)
		}
	})
	t.Run("admission cancellation", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		if err := st.ArmRunGuard(context.Background()); err != nil {
			t.Fatal(err)
		}
		held := fill(t, st.gate, maxActive)
		defer func() {
			for _, release := range held {
				release()
			}
		}()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := st.RecoveryIdentityPresent(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("admission cancellation: %v", err)
		}
	})
	t.Run("multiple identities", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		seedDevice(t, st)
		seedDeviceAs(t, st, types.NewJID("19999990000", types.DefaultUserServer), types.JID{})
		if err := st.ArmRunGuard(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := st.RecoveryIdentityPresent(context.Background()); !errors.Is(err, ErrUnsafeStorage) {
			t.Fatalf("multiple identities: %v", err)
		}
	})
	t.Run("cancelled and closed", func(t *testing.T) {
		st, _ := openTest(t, newDataDir(t))
		if err := st.ArmRunGuard(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := st.RecoveryIdentityPresent(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled: %v", err)
		}
		if err := st.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := st.RecoveryIdentityPresent(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatalf("closed: %v", err)
		}
	})
}
