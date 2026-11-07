package store

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"
	"time"

	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

func TestRepairMarkerBeforeAppStateWrite(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 7, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	var revision int64
	var target []byte
	if err := st.reader.QueryRow("SELECT revision, target_version FROM device_repair WHERE device_jid=? AND collection=?", dev.ID.String(), "regular").Scan(&revision, &target); err != nil {
		t.Fatalf("marker after version write: %v", err)
	}
	if revision != 1 || len(target) != 8 || target[7] != 7 {
		t.Errorf("marker revision=%d target=%x, want revision 1 and version 7", revision, target)
	}
	if err := dev.AppState.DeleteAppStateVersion(ctx, "regular"); err != nil {
		t.Fatal(err)
	}
	if err := st.reader.QueryRow("SELECT revision, target_version FROM device_repair WHERE device_jid=? AND collection=?", dev.ID.String(), "regular").Scan(&revision, &target); err != nil {
		t.Fatal(err)
	}
	if revision != 2 || target != nil {
		t.Errorf("marker revision=%d target=%x, want revision 2 and reset", revision, target)
	}
}

func TestRepairTicketFencesLaterMutation(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	tagged, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(tagged, "regular", 2, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 3, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := barrier.CompleteRepair(ctx, ticket); err == nil {
		t.Fatal("superseded ticket cleared marker")
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair WHERE collection='regular'").Scan(&count); err != nil || count != 1 {
		t.Errorf("marker count = %d, err = %v; want 1", count, err)
	}
	if err := dev.AppState.PutAppStateVersion(tagged, "regular", 4, [128]byte{}); err == nil {
		t.Fatal("superseded repair admitted a late write")
	}
}

func TestRepairTicketSuccessAndDiscardedFault(t *testing.T) {
	dir := newDataDir(t)
	st, dev, barrier := loadedDevice(t, dir)
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	tagged, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.writer.Exec(`CREATE TRIGGER fail_repair_update BEFORE UPDATE ON whatsmeow_app_state_version BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(tagged, "regular", 2, [128]byte{}); err == nil {
		t.Fatal("injected write succeeded")
	}
	if _, err := st.writer.Exec("DROP TRIGGER fail_repair_update"); err != nil {
		t.Fatal(err)
	}
	if err := barrier.CompleteRepair(ctx, ticket); err == nil {
		t.Fatal("discarded guarded failure cleared marker")
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair WHERE collection='regular'").Scan(&count); err != nil || count != 1 {
		t.Errorf("marker count = %d, err = %v", count, err)
	}
	if !barrier.StorageFault() {
		t.Fatal("guarded write fault was not latched")
	}
	if _, _, err := barrier.BeginRepair(ctx, "regular"); !errors.Is(err, ErrRepairFault) {
		t.Fatalf("same-generation recovery = %v, want restart", err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = openTest(t, dir)
	dev, barrier = loadDevice(t, st)
	tagged, ticket, err = barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(tagged, "regular", 3, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := barrier.CompleteRepair(ctx, ticket); err != nil {
		t.Fatal(err)
	}
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair WHERE collection='regular'").Scan(&count); err != nil || count != 0 {
		t.Errorf("markers after success = %d, err = %v", count, err)
	}
}

func TestRepairStartupAdmissionDefersOrdinaryProgress(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dev.AppState.GetAppStateVersion(ctx, "regular"); err != nil {
		t.Fatalf("normal dirty marker incorrectly blocked healthy sync: %v", err)
	}
	if err := barrier.ConfigureRecovery(ctx); err != nil {
		t.Fatal(err)
	}
	held := fill(t, st.gate, maxActive)
	defer func() {
		for _, release := range held {
			release()
		}
	}()
	for i := 0; i < 2; i++ {
		if _, _, err := dev.AppState.GetAppStateVersion(ctx, "regular_high"); !errors.Is(err, ErrRecoveryPending) {
			t.Fatalf("untagged progress: %v", err)
		}
	}
	if st.gate.activeCount() != maxActive || st.gate.waitingCount() != 0 {
		t.Fatal("deferred calls consumed shared admission")
	}
	for _, release := range held {
		release()
	}
	if barrier.StorageFault() {
		t.Fatal("expected sync deferral was treated as storage fault")
	}
	select {
	case <-barrier.StorageFaulted():
		t.Fatal("deferral signalled a storage fault")
	default:
	}
	contacts := []wastore.ContactEntry{{JID: types.NewJID("123456789", types.DefaultUserServer), FirstName: "Test"}}
	if err := dev.Contacts.PutAllContactNames(ctx, contacts); !errors.Is(err, ErrRecoveryPending) {
		t.Fatalf("untagged snapshot = %v, want recovery pending", err)
	}
	if got := barrier.DeferredCollections(); len(got) != 2 || got[0] != "critical_unblock_low" || got[1] != "regular_high" {
		t.Fatalf("deferred collections = %v", got)
	}
	if _, err := dev.AppStateKeys.GetLatestAppStateSyncKeyID(ctx); err != nil {
		t.Fatalf("key delivery path blocked: %v", err)
	}
	tagged, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(tagged, "regular", 2, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := barrier.CompleteRepair(ctx, ticket); err != nil {
		t.Fatal(err)
	}
	if got := barrier.DeferredCollections(); len(got) != 2 || got[0] != "critical_unblock_low" || got[1] != "regular_high" {
		t.Fatalf("replay demand after recovery = %v", got)
	}
	if got := barrier.DeferredCollections(); len(got) != 0 {
		t.Fatalf("replay demand was not consumed: %v", got)
	}
	if _, _, err := dev.AppState.GetAppStateVersion(ctx, "regular_high"); err != nil {
		t.Fatalf("ordinary progress after repair: %v", err)
	}
	_ = st
}

func TestRepairConfigurationOnFreshDevice(t *testing.T) {
	_, _, barrier := freshDevice(t, newDataDir(t))
	if err := barrier.ConfigureRecovery(context.Background()); err != nil {
		t.Fatalf("configure unpaired device: %v", err)
	}
	if pending, err := barrier.PendingRepairs(context.Background()); err != nil || len(pending) != 0 {
		t.Errorf("fresh pending = %v, err = %v", pending, err)
	}
}

func TestRepairCancellationAndRetirement(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	tagged, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	barrier.CancelRepair(ticket)
	if err := barrier.CompleteRepair(ctx, ticket); err == nil {
		t.Fatal("cancelled ticket cleared marker")
	}
	if err := dev.AppState.PutAppStateVersion(tagged, "regular", 2, [128]byte{}); err == nil {
		t.Fatal("cancelled ticket admitted mutation")
	}
	tagged, ticket, err = barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := barrier.CompleteRepair(ctx, ticket); err == nil {
		t.Fatal("retired ticket cleared marker")
	}
	if err := dev.AppState.PutAppStateVersion(tagged, "regular", 2, [128]byte{}); err == nil {
		t.Fatal("retired generation admitted mutation")
	}
}

func TestPendingRepairsBoundedAndCopied(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	for _, name := range repairCollections {
		if err := dev.AppState.PutAppStateVersion(ctx, name, 1, [128]byte{}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := barrier.PendingRepairs(ctx)
	if err != nil || len(rows) != 5 {
		t.Fatalf("pending = %v, err = %v", rows, err)
	}
	rows[0] = "tampered"
	rows, err = barrier.PendingRepairs(ctx)
	if err != nil || len(rows) != 5 || rows[0] == "tampered" {
		t.Fatalf("second pending = %v, err = %v", rows, err)
	}
}

func TestRepairFaultLatchesForGuardedSessionWrite(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	if _, err := st.writer.Exec(`CREATE TRIGGER refuse_session BEFORE INSERT ON whatsmeow_sessions BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := dev.Sessions.PutSession(context.Background(), sessionPN, sessionBlob(3)); err == nil {
		t.Fatal("injected session failure succeeded")
	}
	if !barrier.StorageFault() {
		t.Fatal("guarded session write failure was not latched")
	}
	select {
	case <-barrier.StorageFaulted():
	default:
		t.Fatal("guarded write fault did not notify owner")
	}
}

func TestRepairCancelledContextCannotClear(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	repairCtx, cancel := context.WithCancel(ctx)
	_, ticket, err := barrier.BeginRepair(repairCtx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := barrier.CompleteRepair(ctx, ticket); err == nil {
		t.Fatal("cancelled repair context cleared marker")
	}
}

func TestRepairTracksDiscardedOtherGuardedWriteError(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	tagged, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.Sessions.PutSession(tagged, "", nil); err == nil {
		t.Fatal("invalid guarded session write succeeded")
	}
	if err := barrier.CompleteRepair(ctx, ticket); err == nil {
		t.Fatal("ticket cleared after discarded guarded write error")
	}
}

func TestRepairCancelWhileClearWaits(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	tagged, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	barrier.sess.repairFence.Lock()
	done := make(chan error, 1)
	go func() { done <- barrier.CompleteRepair(ctx, ticket) }()
	barrier.CancelRepair(ticket)
	barrier.sess.repairFence.Unlock()
	if err := <-done; err == nil {
		t.Fatal("cancelled clear succeeded")
	}
	if err := dev.AppState.PutAppStateVersion(context.WithoutCancel(tagged), "regular", 2, [128]byte{}); err == nil {
		t.Fatal("detached cancelled ticket admitted progress")
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair").Scan(&count); err != nil || count != 1 {
		t.Errorf("marker count=%d, err=%v", count, err)
	}
}

func TestRepairClearRejectsChangedMarkerFields(t *testing.T) {
	for name, update := range map[string]string{
		"revision": "UPDATE device_repair SET revision=revision+1",
		"target":   "UPDATE device_repair SET target_version=x'0000000000000009'",
		"ticket":   "UPDATE device_repair SET repair_ticket=zeroblob(16)",
	} {
		t.Run(name, func(t *testing.T) {
			st, dev, barrier := loadedDevice(t, newDataDir(t))
			ctx := context.Background()
			if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
				t.Fatal(err)
			}
			_, ticket, err := barrier.BeginRepair(ctx, "regular")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.writer.Exec(update); err != nil {
				t.Fatal(err)
			}
			if err := barrier.CompleteRepair(ctx, ticket); err == nil {
				t.Fatal("changed marker cleared")
			}
			if barrier.StorageFault() {
				t.Fatal("expected marker conflict was classified as a storage fault")
			}
			var count int
			if err := st.reader.QueryRow("SELECT count(*) FROM device_repair").Scan(&count); err != nil || count != 1 {
				t.Errorf("marker count=%d err=%v", count, err)
			}
		})
	}
}

func TestRepairRejectsForeignDeviceAndCollection(t *testing.T) {
	_, dev, barrier := loadedDevice(t, newDataDir(t))
	_, other, otherBarrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := other.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	tagged, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	if err := otherBarrier.CompleteRepair(ctx, ticket); !errors.Is(err, ErrRepairInvalid) {
		t.Errorf("foreign completion = %v", err)
	}
	if err := other.AppState.PutAppStateVersion(tagged, "regular", 2, [128]byte{}); !errors.Is(err, ErrRepairInvalid) {
		t.Errorf("foreign mutation = %v", err)
	}
	if err := dev.AppState.PutAppStateVersion(tagged, "regular_high", 2, [128]byte{}); !errors.Is(err, ErrRepairInvalid) {
		t.Errorf("wrong collection = %v", err)
	}
	if err := barrier.CompleteRepair(ctx, ticket); err == nil {
		t.Fatal("ticket cleared after guarded collection error")
	}
}

func TestRepairClearWaitsForProtectedMutation(t *testing.T) {
	st, dev, barrier := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 1, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	tagged, ticket, err := barrier.BeginRepair(ctx, "regular")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	mutation := make(chan error, 1)
	go func() {
		mutation <- barrier.sess.protected(tagged, "regular", "test mutation", true, nil, func(c context.Context) error {
			version := uint64(2)
			if err := barrier.sess.markProgress(c, "regular", &version); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	clearStarted := make(chan struct{})
	clearDone := make(chan error, 1)
	go func() { close(clearStarted); clearDone <- barrier.CompleteRepair(ctx, ticket) }()
	<-clearStarted
	select {
	case err := <-clearDone:
		close(release)
		<-mutation
		t.Fatalf("clear completed while mutation held: %v", err)
	default:
	}
	close(release)
	if err := <-mutation; err != nil {
		t.Fatal(err)
	}
	if err := <-clearDone; err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 3, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair WHERE collection='regular'").Scan(&count); err != nil || count != 1 {
		t.Errorf("late mutation marker count=%d err=%v", count, err)
	}
}

func TestRepairMarkerBeforeContactSnapshot(t *testing.T) {
	dir := newDataDir(t)
	st, dev, barrier := loadedDevice(t, dir)
	ctx := context.Background()
	contacts := []wastore.ContactEntry{{JID: types.NewJID("123456789", types.DefaultUserServer), FirstName: "Test"}}
	if _, err := st.writer.Exec(`CREATE TRIGGER refuse_contact_marker BEFORE INSERT ON device_repair BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := dev.Contacts.PutAllContactNames(ctx, contacts); err == nil {
		t.Fatal("contact snapshot succeeded when marker insertion failed")
	}
	info, err := dev.Contacts.GetContact(ctx, contacts[0].JID)
	if err != nil || info.Found {
		t.Fatalf("contact changed after marker refusal: %+v, %v", info, err)
	}
	if _, err := st.writer.Exec("DROP TRIGGER refuse_contact_marker"); err != nil {
		t.Fatal(err)
	}
	if err := dev.Contacts.PutAllContactNames(ctx, contacts); !errors.Is(err, ErrRepairFault) {
		t.Fatalf("same-generation snapshot = %v, want restart", err)
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = openTest(t, dir)
	dev, _ = loadDevice(t, st)
	if err := dev.Contacts.PutAllContactNames(ctx, contacts); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err := st.reader.QueryRow("SELECT revision FROM device_repair WHERE device_jid=? AND collection='critical_unblock_low'", dev.ID.String()).Scan(&revision); err != nil || revision != 1 {
		t.Errorf("contact marker revision = %d, err = %v; want 1", revision, err)
	}
}

func TestRepairMarkerBeforeMACMutation(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 7, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	index := bytes.Repeat([]byte{1}, 32)
	mutations := []wastore.AppStateMutationMAC{{IndexMAC: index, ValueMAC: bytes.Repeat([]byte{2}, 32)}}
	if err := dev.AppState.PutAppStateMutationMACs(ctx, "regular", 7, mutations); err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.DeleteAppStateMutationMACs(ctx, "regular", [][]byte{index}); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err := st.reader.QueryRow("SELECT revision FROM device_repair WHERE device_jid=? AND collection='regular'", dev.ID.String()).Scan(&revision); err != nil || revision != 3 {
		t.Errorf("MAC marker revision = %d, err = %v; want 3", revision, err)
	}
}

func TestRepairMarkerFailurePreventsMutation(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if _, err := st.writer.Exec(`CREATE TRIGGER refuse_repair BEFORE INSERT ON device_repair BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 7, [128]byte{}); err == nil {
		t.Fatal("version write succeeded when marker insertion failed")
	}
	var versions int
	if err := st.reader.QueryRow("SELECT count(*) FROM whatsmeow_app_state_version WHERE name='regular'").Scan(&versions); err != nil || versions != 0 {
		t.Errorf("versions after marker failure = %d, err = %v; want zero", versions, err)
	}
}

func TestRepairMarkerRejectsUnknownCollection(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	if err := dev.AppState.PutAppStateVersion(context.Background(), "unknown", 7, [128]byte{}); err == nil {
		t.Fatal("unknown collection accepted")
	}
	var markers, versions int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair").Scan(&markers); err != nil {
		t.Fatal(err)
	}
	if err := st.reader.QueryRow("SELECT count(*) FROM whatsmeow_app_state_version WHERE name='unknown'").Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if markers != 0 || versions != 0 {
		t.Errorf("rejected collection wrote %d markers and %d versions", markers, versions)
	}
}

func TestRepairMarkerSurvivesReopen(t *testing.T) {
	dir := newDataDir(t)
	st, dev, barrier := loadedDevice(t, dir)
	ctx := context.Background()
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 9, [128]byte{}); err != nil {
		t.Fatal(err)
	}
	jid := dev.ID.String()
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, _ := openTest(t, dir)
	var revision int64
	if err := reopened.reader.QueryRow("SELECT revision FROM device_repair WHERE device_jid=? AND collection='regular'", jid).Scan(&revision); err != nil || revision != 1 {
		t.Errorf("reopened repair revision = %d, err = %v; want 1", revision, err)
	}
}

func TestRepairMarkerOverflowPreventsMutation(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	ctx := context.Background()
	if _, err := st.writer.Exec(`INSERT INTO device_repair (device_jid, collection, revision, updated_at_ms) VALUES (?, 'regular', ?, 1)`, dev.ID.String(), int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(ctx, "regular", 9, [128]byte{}); err == nil {
		t.Fatal("version write succeeded after marker revision overflow")
	}
	var revision int64
	if err := st.reader.QueryRow("SELECT revision FROM device_repair WHERE device_jid=? AND collection='regular'", dev.ID.String()).Scan(&revision); err != nil || revision != math.MaxInt64 {
		t.Errorf("revision after overflow = %d, err = %v", revision, err)
	}
	var versions int
	if err := st.reader.QueryRow("SELECT count(*) FROM whatsmeow_app_state_version WHERE name='regular'").Scan(&versions); err != nil || versions != 0 {
		t.Errorf("versions after overflow = %d, err = %v; want zero", versions, err)
	}
}

func TestRepairMarkerRetainedAfterProtectedWriteFails(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	if _, err := st.writer.Exec(`CREATE TRIGGER refuse_version BEFORE INSERT ON whatsmeow_app_state_version BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := dev.AppState.PutAppStateVersion(context.Background(), "regular", 7, [128]byte{}); err == nil {
		t.Fatal("protected write succeeded despite injected failure")
	}
	var revision int
	if err := st.reader.QueryRow("SELECT revision FROM device_repair WHERE device_jid=? AND collection='regular'", dev.ID.String()).Scan(&revision); err != nil || revision != 1 {
		t.Errorf("marker after failed write = %d, err = %v; want revision 1", revision, err)
	}
}

func TestRepairInterruptedSiblingWritesSurviveReopen(t *testing.T) {
	for _, part := range []string{"mac", "contact"} {
		t.Run(part, func(t *testing.T) {
			dir := newDataDir(t)
			st, dev, barrier := loadedDevice(t, dir)
			ctx := context.Background()
			mustStore(t, st, msg(alice, "repair-sentinel", alice, at(0)))
			if err := dev.AppState.PutAppStateVersion(ctx, "critical_unblock_low", 1, [128]byte{}); err != nil {
				t.Fatal(err)
			}
			if part == "mac" {
				if _, err := st.writer.Exec(`CREATE TRIGGER interrupt_sibling BEFORE INSERT ON whatsmeow_app_state_mutation_macs BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
					t.Fatal(err)
				}
				m := []wastore.AppStateMutationMAC{{IndexMAC: bytes.Repeat([]byte{1}, 32), ValueMAC: bytes.Repeat([]byte{2}, 32)}}
				if err := dev.AppState.PutAppStateMutationMACs(ctx, "critical_unblock_low", 1, m); err == nil {
					t.Fatal("injected MAC write succeeded")
				}
			} else {
				if _, err := st.writer.Exec(`CREATE TRIGGER interrupt_sibling BEFORE INSERT ON whatsmeow_contacts BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
					t.Fatal(err)
				}
				entries := []wastore.ContactEntry{{JID: types.NewJID("123456789", types.DefaultUserServer), FirstName: "Test"}}
				if err := dev.Contacts.PutAllContactNames(ctx, entries); err == nil {
					t.Fatal("injected contact write succeeded")
				}
			}
			jid := dev.ID.String()
			if err := barrier.Retire(ctx); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(ctx); err != nil {
				t.Fatal(err)
			}
			reopened, _ := openTest(t, dir)
			var revision int
			if err := reopened.reader.QueryRow("SELECT revision FROM device_repair WHERE device_jid=? AND collection='critical_unblock_low'", jid).Scan(&revision); err != nil || revision != 2 {
				t.Errorf("marker after interrupted %s = %d, err=%v", part, revision, err)
			}
			if !messageExists(t, reopened, alice, "repair-sentinel") {
				t.Error("application message lost")
			}
			var deviceCount int
			if err := reopened.reader.QueryRow("SELECT count(*) FROM whatsmeow_device WHERE jid=?", jid).Scan(&deviceCount); err != nil || deviceCount != 1 {
				t.Errorf("device count=%d err=%v", deviceCount, err)
			}
		})
	}
}

func TestRepairMarkerRejectsNestedMutation(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	err := dev.EventBuffer.DoDecryptionTxn(context.Background(), func(ctx context.Context) error {
		return dev.AppState.PutAppStateVersion(ctx, "regular", 7, [128]byte{})
	})
	if err == nil {
		t.Fatal("nested protected write succeeded")
	}
	var count int
	if err := st.reader.QueryRow("SELECT count(*) FROM device_repair").Scan(&count); err != nil || count != 0 {
		t.Errorf("markers after rejected nested write = %d, err = %v", count, err)
	}
}

func TestRepairNestedRejectionDoesNotWaitForMarkerFence(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	background := make(chan error, 1)
	err := dev.EventBuffer.DoDecryptionTxn(context.Background(), func(tx context.Context) error {
		go func() { background <- dev.AppState.PutAppStateVersion(context.Background(), "regular", 1, [128]byte{}) }()
		eventually(t, "concurrent marker write admitted", func() bool { return st.gate.activeCount() == 2 })
		done := make(chan error, 1)
		go func() { done <- dev.AppState.PutAppStateVersion(tx, "regular", 2, [128]byte{}) }()
		select {
		case err := <-done:
			if err == nil {
				t.Error("nested marker work was accepted")
			}
		case <-time.After(time.Second):
			t.Error("nested rejection waited on marker fence while holding the writer")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-background; err != nil {
		t.Fatal(err)
	}
}

func TestRepairNestedProgressReadReusesLease(t *testing.T) {
	_, dev, _ := loadedDevice(t, newDataDir(t))
	err := dev.EventBuffer.DoDecryptionTxn(context.Background(), func(tx context.Context) error {
		_, _, err := dev.AppState.GetAppStateVersion(tx, "regular")
		return err
	})
	if err != nil {
		t.Fatalf("nested read: %v", err)
	}
}
