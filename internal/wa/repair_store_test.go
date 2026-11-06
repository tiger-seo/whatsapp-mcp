package wa

import (
	"context"
	"errors"
	"testing"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
)

// These run the repair controller against the real guarded store: an interrupted ordinary write
// leaves a durable marker, a reopened generation defers untagged app-state work, and only a
// successful fenced repair clears the marker and releases ordinary admission.

const repairCollection = "critical_block"

// seedInterruptedProgress saves an identity, then makes a protected progress write that leaves a
// durable repair marker, and closes the store so the next open sees recovery state.
func seedInterruptedProgress(t *testing.T, dir string) {
	t.Helper()
	st := openWADirAt(t, dir)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := newSessionTestConfig(st, lifetime)
	s, err := OpenSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	armAndSave(t, s)
	if err := s.dev.AppState.PutAppStateVersion(context.Background(), repairCollection, 3, [128]byte{1}); err != nil {
		t.Fatalf("progress write: %v", err)
	}
	if err := s.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func reopenSession(t *testing.T, dir string) (*Session, func()) {
	t.Helper()
	st := openWADirAt(t, dir)
	lifetime, cancel := context.WithCancel(context.Background())
	s, err := OpenSession(newSessionTestConfig(st, lifetime))
	if err != nil {
		t.Fatal(err)
	}
	return s, func() {
		_ = s.Retire(context.Background())
		cancel()
		_ = st.Close(context.Background())
	}
}

func TestRepairAgainstStoreClearsMarkerAndReleasesAdmission(t *testing.T) {
	dir := waDataDir(t)
	seedInterruptedProgress(t, dir)
	s, closeAll := reopenSession(t, dir)
	defer closeAll()
	if !s.RecoveryPending() {
		t.Fatal("the interrupted write left no pending repair")
	}
	// While recovery is pending, ordinary progress writes are deferred before any SQL.
	err := s.dev.AppState.PutAppStateVersion(context.Background(), repairCollection, 9, [128]byte{9})
	if !errors.Is(err, store.ErrRecoveryPending) {
		t.Fatalf("untagged progress write during recovery = %v, want recovery pending", err)
	}
	var fullCalls, replays int
	s.repair.fetch = func(ctx context.Context, name string, full bool) error {
		if name != repairCollection {
			t.Errorf("unexpected collection %s", name)
		}
		if !full {
			// The demand deferred by the rejected write above is replayed after recovery. An
			// ordinary replay is incremental and, in this test, writes nothing.
			replays++
			return nil
		}
		fullCalls++
		// The tagged repair context may write the collection it is repairing.
		return s.dev.AppState.PutAppStateVersion(ctx, name, 4, [128]byte{4})
	}
	if err := s.RunRepairs(context.Background()); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if fullCalls != 1 {
		t.Errorf("full resyncs = %d, want 1", fullCalls)
	}
	if replays != 1 {
		t.Errorf("deferred replays = %d, want 1 for the one deferred demand", replays)
	}
	if s.RecoveryPending() {
		t.Error("recovery still pending after a successful repair")
	}
	pending, err := s.barrier.PendingRepairs(context.Background())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after repair = %v, %v", pending, err)
	}
	// Ordinary admission is released.
	if err := s.dev.AppState.PutAppStateVersion(context.Background(), repairCollection, 10, [128]byte{10}); err != nil {
		t.Errorf("ordinary write after recovery: %v", err)
	}
}

func TestRepairAgainstStoreFailureKeepsMarkerAcrossRestart(t *testing.T) {
	dir := waDataDir(t)
	seedInterruptedProgress(t, dir)
	s, closeAll := reopenSession(t, dir)
	s.repair.fetch = func(context.Context, string, bool) error { return errors.New("synthetic failure") }
	if err := s.RunRepairs(context.Background()); err == nil {
		t.Fatal("a failed repair reported success")
	}
	if !s.RecoveryPending() {
		t.Error("a failed repair cleared the pending flag")
	}
	closeAll()

	// After a restart the marker is still there and a new generation can retry.
	s2, closeAll2 := reopenSession(t, dir)
	defer closeAll2()
	if !s2.RecoveryPending() {
		t.Fatal("the marker was lost across the restart")
	}
	s2.repair.fetch = func(context.Context, string, bool) error { return nil }
	if err := s2.RunRepairs(context.Background()); err != nil {
		t.Fatalf("retry in the new generation: %v", err)
	}
	if s2.RecoveryPending() {
		t.Error("the retry did not clear the marker")
	}
}

// A timed-out repair keeps its marker even if the abandoned call later succeeds.
func TestRepairAgainstStoreLateSuccessDoesNotClear(t *testing.T) {
	dir := waDataDir(t)
	seedInterruptedProgress(t, dir)
	s, closeAll := reopenSession(t, dir)
	defer closeAll()
	s.repair.deadline = 50 * 1000 * 1000 // 50 ms
	release := make(chan struct{})
	returned := make(chan struct{})
	s.repair.fetch = func(ctx context.Context, name string, full bool) error {
		defer close(returned)
		<-release
		return s.dev.AppState.PutAppStateVersion(ctx, name, 4, [128]byte{4})
	}
	if err := s.RunRepairs(context.Background()); !errors.Is(err, errRepairDeadline) {
		t.Fatalf("repair = %v, want the deadline", err)
	}
	close(release)
	<-returned
	if !s.RecoveryPending() {
		t.Error("the late success cleared recovery")
	}
	pending, err := s.barrier.PendingRepairs(context.Background())
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %v, %v; the marker must remain", pending, err)
	}
}

// The real pinned FetchAppState on a client that was never connected fails without any network
// use and leaves the marker: the production call path is wired to the controller.
func TestRepairRealFetchOnUnconnectedClientKeepsMarker(t *testing.T) {
	dir := waDataDir(t)
	seedInterruptedProgress(t, dir)
	s, closeAll := reopenSession(t, dir)
	defer closeAll()
	if err := s.RunRepairs(context.Background()); err == nil {
		t.Fatal("a repair over an unconnected client reported success")
	}
	if !s.RecoveryPending() {
		t.Error("the marker was cleared without a successful fetch")
	}
}
