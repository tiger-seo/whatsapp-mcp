package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
)

var reconcileTime = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// seedLIDData stores data under two mapped LIDs, a direct chat and a group-only sender, a phone chat that shares a
// message with the LID chat, and one unmapped LID chat.
func seedLIDData(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(ctx)
	msgs := []store.Message{
		{ChatJID: "100000000001@lid", ID: "L1", SenderJID: "100000000001@lid", Time: reconcileTime, Text: "lid chat"},
		{ChatJID: "100000000001@lid", ID: "SHARED", SenderJID: "100000000001@lid", Time: reconcileTime, Text: "lid copy"},
		{ChatJID: "491511111111@s.whatsapp.net", ID: "SHARED", SenderJID: "491511111111@s.whatsapp.net", Time: reconcileTime, Text: "phone copy"},
		{ChatJID: "120363000000000001@g.us", ID: "G1", SenderJID: "100000000002@lid", Time: reconcileTime, Text: "group"},
		{ChatJID: "100000000003@lid", ID: "U1", SenderJID: "100000000003@lid", Time: reconcileTime, Text: "unmapped"},
	}
	if err := st.StoreMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	dev, barrier, err := st.LoadDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	own := types.NewJID("490000000001", types.DefaultUserServer)
	dev.ID = &own
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("details"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := dev.Save(ctx); err != nil {
		t.Fatal(err)
	}
	for lid, pn := range map[string]string{"100000000001": "491511111111", "100000000002": "491522222222"} {
		if err := dev.LIDs.PutLIDMapping(ctx, types.NewJID(lid, types.HiddenUserServer), types.NewJID(pn, types.DefaultUserServer)); err != nil {
			t.Fatal(err)
		}
	}
	if err := barrier.Retire(ctx); err != nil {
		t.Fatal(err)
	}
}

func messageAt(t *testing.T, dir, chat, id string) (store.MessageView, error) {
	t.Helper()
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	return st.GetMessage(context.Background(), chat, id)
}

// The shell merges mapped LID data before the owner starts; a second start merges nothing.
func TestShellReconcileMergesBeforeTheOwner(t *testing.T) {
	dir := privateDir(t)
	seedLIDData(t, dir)
	for round, want := range []string{"merged=2", "merged=0"} {
		so := newScriptedOwner()
		var deps ownerDeps
		prev := newOwner
		newOwner = func(d ownerDeps) (*ownerHandle, error) {
			deps = d
			return &ownerHandle{owner: so}, nil
		}
		r := startIngestShell(t, "stdio", strings.NewReader(""), dir)
		code := r.wait(t)
		newOwner = prev
		if code != exitOK {
			t.Fatalf("round %d: exit %d\n%s", round, code, r.stderr.String())
		}
		line := ""
		for _, l := range strings.Split(r.stderr.String(), "\n") {
			if strings.Contains(l, "event=lid_reconcile") {
				line = l
			}
		}
		if !strings.Contains(line, want) || !strings.Contains(line, "failed=0") || !strings.Contains(line, "status=completed") {
			t.Fatalf("round %d: lid_reconcile line %q, want %s and completed", round, line, want)
		}
		if deps.reconcileFrom != nil {
			t.Fatalf("round %d: a completed pass handed a cursor to the owner", round)
		}
	}
	got, err := messageAt(t, dir, "491511111111@s.whatsapp.net", "L1")
	if err != nil || got.SenderJID != "491511111111@s.whatsapp.net" {
		t.Fatalf("LID chat message after the merge: %+v, %v", got, err)
	}
	if got, _ := messageAt(t, dir, "491511111111@s.whatsapp.net", "SHARED"); got.Text != "phone copy" {
		t.Fatalf("the shared message kept %q, want the phone chat's copy", got.Text)
	}
	if got, _ := messageAt(t, dir, "120363000000000001@g.us", "G1"); got.SenderJID != "491522222222@s.whatsapp.net" {
		t.Fatalf("group-only LID sender now %q", got.SenderJID)
	}
	if got, err := messageAt(t, dir, "100000000003@lid", "U1"); err != nil || got.SenderJID != "100000000003@lid" {
		t.Fatalf("the unmapped LID changed: %+v, %v", got, err)
	}
}

// failOneMerge fails the merge of one LID and passes everything else to the store.
type failOneMerge struct {
	*store.Store
	lid string
}

func (f failOneMerge) MergeLID(ctx context.Context, lid, pn string) error {
	if lid == f.lid {
		return fmt.Errorf("synthetic: %w", store.ErrDiskLow)
	}
	return f.Store.MergeLID(ctx, lid, pn)
}

func TestReconcileFailedMergeLeavesBothSidesAndIsReported(t *testing.T) {
	dir := privateDir(t)
	seedLIDData(t, dir)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	res := wa.ReconcileLIDs(context.Background(), failOneMerge{st, "100000000001@lid"}, slog.New(slog.DiscardHandler), "")
	if res.Merged != 1 || res.Failed != 1 || !res.Completed {
		t.Fatalf("result %+v, want one merged, one failed, completed", res)
	}
	if got, err := st.GetMessage(context.Background(), "100000000001@lid", "L1"); err != nil || got.Text != "lid chat" {
		t.Fatalf("the LID side of the failed merge: %+v, %v", got, err)
	}
}

// cancelAfterMerges ends the budget after n merges.
type cancelAfterMerges struct {
	*store.Store
	n      int
	cancel context.CancelFunc
}

func (c *cancelAfterMerges) MergeLID(ctx context.Context, lid, pn string) error {
	err := c.Store.MergeLID(ctx, lid, pn)
	if c.n--; c.n == 0 {
		c.cancel()
	}
	return err
}

func TestReconcileBudgetExhaustedHandsOverItsCursor(t *testing.T) {
	dir := privateDir(t)
	seedLIDData(t, dir)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger, buf := bufferLogger()
	cursor := startupReconcile(ctx, &cancelAfterMerges{Store: st, n: 1, cancel: cancel}, logger)
	if cursor == nil || cursor.After != "100000000001@lid" {
		t.Fatalf("cursor %+v, want the last LID reached", cursor)
	}
	if !strings.Contains(buf.String(), "status=budget_exhausted") || !strings.Contains(buf.String(), "merged=1") {
		t.Fatalf("log %q", buf.String())
	}
	// The running session continues from the cursor.
	res := wa.ReconcileLIDs(context.Background(), st, slog.New(slog.DiscardHandler), cursor.After)
	if res.Merged != 1 || !res.Completed {
		t.Fatalf("continuation %+v, want the remaining mapping merged", res)
	}
}

// An exhausted pass still starts the command, and its cursor reaches the owner for the first generation.
func TestShellPassesTheReconcileCursorToTheOwner(t *testing.T) {
	prevPass := startupReconcile
	startupReconcile = func(context.Context, wa.LIDMergeStore, *slog.Logger) *wa.LIDCursor {
		return &wa.LIDCursor{After: "100000000009@lid"}
	}
	t.Cleanup(func() { startupReconcile = prevPass })
	so := newScriptedOwner()
	var deps ownerDeps
	prev := newOwner
	newOwner = func(d ownerDeps) (*ownerHandle, error) {
		deps = d
		return &ownerHandle{owner: so}, nil
	}
	t.Cleanup(func() { newOwner = prev })
	r := startIngestShell(t, "stdio", strings.NewReader(""), privateDir(t))
	if code := r.wait(t); code != exitOK {
		t.Fatalf("exit %d\n%s", code, r.stderr.String())
	}
	if deps.reconcileFrom == nil || deps.reconcileFrom.After != "100000000009@lid" {
		t.Fatalf("owner deps cursor %+v", deps.reconcileFrom)
	}
	if cfg := productionOwnerConfig(deps, nil, nil); cfg.ReconcileFrom == nil || cfg.ReconcileFrom.After != "100000000009@lid" {
		t.Fatalf("owner config cursor %+v", cfg.ReconcileFrom)
	}
}

// bufferLogger records text log lines for assertions.
func bufferLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

// A stop signal during a long startup reconciliation stops the command like a signal at any other point of the
// start: no owner is started, no error is printed, and the exit is the clean one.
func TestShellStopDuringReconciliationExitsCleanly(t *testing.T) {
	for _, kind := range []string{"serve", "stdio"} {
		t.Run(kind, func(t *testing.T) {
			signals := make(chan os.Signal, 2)
			withTestLifecycleSignals(t, signals)
			started := make(chan struct{})
			prevReconcile := startupReconcile
			t.Cleanup(func() { startupReconcile = prevReconcile })
			startupReconcile = func(ctx context.Context, _ wa.LIDMergeStore, _ *slog.Logger) *wa.LIDCursor {
				close(started)
				signals <- os.Interrupt
				<-ctx.Done() // the pass ends with the stop, as a real one does at its budget
				return &wa.LIDCursor{After: "100000000001@lid"}
			}
			ownerStarted := make(chan struct{}, 1)
			prevOwner := newOwner
			t.Cleanup(func() { newOwner = prevOwner })
			newOwner = func(ownerDeps) (*ownerHandle, error) {
				ownerStarted <- struct{}{}
				return &ownerHandle{owner: newScriptedOwner()}, nil
			}
			stdin, keep := io.Pipe()
			defer keep.Close()
			r := startIngestShell(t, kind, stdin, privateDir(t))
			code := r.wait(t)
			select {
			case <-started:
			default:
				t.Fatal("the reconciliation never ran")
			}
			if code != exitOK {
				t.Errorf("exit code = %d, want %d after a stop signal\nstderr:\n%s", code, exitOK, r.stderr.String())
			}
			if strings.Contains(r.stderr.String(), "whatsapp-mcp: "+kind+":") {
				t.Errorf("an error was printed for a stop signal:\n%s", r.stderr.String())
			}
			select {
			case <-ownerStarted:
				t.Error("the owner was started after the stop signal")
			default:
			}
		})
	}
}
