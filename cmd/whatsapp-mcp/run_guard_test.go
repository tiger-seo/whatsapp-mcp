package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wa"
)

type delayedRecoveryReader struct {
	mu      sync.Mutex
	reads   int
	blocked chan struct{}
	release chan struct{}
}

func (r *delayedRecoveryReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	r.reads++
	n := r.reads
	r.mu.Unlock()
	if n == 1 {
		return copy(p, "recover\n"), nil
	}
	if n == 2 {
		close(r.blocked)
		<-r.release
		return copy(p, "unlink\nauthorize-unlink\nverified\n"), nil
	}
	return 0, io.EOF
}

func TestRecoveryCancellationStopsPromptingAfterBlockedRead(t *testing.T) {
	reader := &delayedRecoveryReader{blocked: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := newRecoveryInput(ctx, reader)
	var out syncBuffer
	finished := make(chan bool, 1)
	go func() { finished <- promptRecovery(ctx, input, &out, false) }()
	select {
	case <-reader.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("second read was not reached")
	}
	cancel()
	select {
	case ok := <-finished:
		if ok {
			t.Fatal("cancelled prompt approved")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not return on cancellation")
	}
	before := out.String()
	second := newRecoveryInput(context.Background(), strings.NewReader("recover\n"))
	if second != nil {
		second.cancel()
		<-second.done
		close(reader.release)
		<-input.done
		t.Fatal("a second recovery reader was allowed while the first read was blocked")
	}
	close(reader.release)
	select {
	case <-input.done:
	case <-time.After(5 * time.Second):
		t.Fatal("input worker did not exit after blocked read returned")
	}
	if after := out.String(); after != before {
		t.Fatalf("output changed after cancellation: %q", after[len(before):])
	}
	reader.mu.Lock()
	reads := reader.reads
	reader.mu.Unlock()
	if reads != 2 {
		t.Fatalf("reads = %d, want only the outstanding read", reads)
	}
}

func TestRecoveryCancellationDuringInputLeavesUnsafeMarker(t *testing.T) {
	dir := privateDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ArmRunGuard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	old := recoveryInteractive
	recoveryInteractive = func(*streams) bool { return true }
	defer func() { recoveryInteractive = old }()
	reader := &delayedRecoveryReader{blocked: make(chan struct{}), release: make(chan struct{})}
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan bool, 1)
	go func() { result <- recoverOffline(ctx, st, &streams{stdin: reader, stdout: &out, stderr: io.Discard}) }()
	select {
	case <-reader.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("second read was not reached")
	}
	cancel()
	select {
	case ok := <-result:
		if ok {
			t.Fatal("cancelled recovery succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not return")
	}
	before := out.String()
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" {
		t.Fatalf("marker=%q err=%v", state, err)
	}
	close(reader.release)
	select {
	case recoveryReaderSlot <- struct{}{}:
		<-recoveryReaderSlot
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not release admission")
	}
	if after := out.String(); after != before {
		t.Fatalf("output changed after recovery returned: %q", after[len(before):])
	}
}

func TestOfflineRecoveryConfirmations(t *testing.T) {
	for _, tc := range []struct {
		name, input          string
		interactive, success bool
	}{
		{"approved absent identity", "recover\nabsent\nverified\n", true, true},
		{"manual unlink authorized", "recover\nunlink\nauthorize-unlink\nverified\n", true, true},
		{"manual unlink denied", "recover\nunlink\nno\nverified\n", true, false},
		{"enter", "\n", true, false},
		{"unverified", "recover\nabsent\nno\n", true, false},
		{"eof", "recover\n", true, false},
		{"redirected", "recover\nabsent\nverified\n", false, false},
		{"oversized", strings.Repeat("a", 1025) + "\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := privateDir(t)
			st, err := store.Open(context.Background(), dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.ArmRunGuard(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			so := newScriptedOwner()
			withScriptedOwner(t, so)
			old := recoveryInteractive
			recoveryInteractive = func(*streams) bool { return tc.interactive }
			t.Cleanup(func() { recoveryInteractive = old })
			var out, diagnostic bytes.Buffer
			code := run([]string{"pair", "--data-dir", dir}, strings.NewReader(tc.input), &out, &diagnostic, nil)
			if (code == exitOK) != tc.success {
				t.Fatalf("exit %d: %s", code, diagnostic.String())
			}
			if so.started.Load() || so.requestN.Load() != 0 {
				t.Fatal("account work during recovery")
			}
			st, err = store.Open(context.Background(), dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close(context.Background())
			state, _, err := st.RunGuard(context.Background())
			want := "unsafe"
			if tc.success {
				want = "clean"
			}
			if err != nil || state != want {
				t.Fatalf("marker = %q, %v; want %s", state, err, want)
			}
		})
	}
}

func TestOwnerStopErrorPropagates(t *testing.T) {
	so := newScriptedOwner()
	so.stopErr = store.ErrUnsafeRun
	if err := (&ownerHandle{owner: so}).stopStep()(context.Background()); err == nil {
		t.Fatal("stop error lost")
	}
}

type recoveryFailWriter struct{ writes int }

func (w *recoveryFailWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == 3 {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestRecoveryWarningWriteFailureDoesNotClear(t *testing.T) {
	dir := privateDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ArmRunGuard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	old := recoveryInteractive
	recoveryInteractive = func(*streams) bool { return true }
	defer func() { recoveryInteractive = old }()
	if recoverOffline(context.Background(), st, &streams{stdin: strings.NewReader("recover\nabsent\nverified\n"), stdout: &recoveryFailWriter{}, stderr: io.Discard}) {
		t.Fatal("recovery accepted with missing warning")
	}
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" {
		t.Fatalf("state=%s err=%v", state, err)
	}
}

func TestRecoverySavedIdentitySeparateRemoval(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		success     bool
	}{
		{"no removal approval", "recover\nabsent\nverified\n", false},
		{"deny removal", "recover\nabsent\nverified\nno\n", false},
		{"authorize removal", "recover\nabsent\nverified\nremove\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := privateDir(t)
			seedSavedIdentity(t, dir)
			st, err := store.Open(context.Background(), dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.ArmRunGuard(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			so := newScriptedOwner()
			withScriptedOwner(t, so)
			old := recoveryInteractive
			recoveryInteractive = func(*streams) bool { return true }
			t.Cleanup(func() { recoveryInteractive = old })
			var out, diag bytes.Buffer
			code := run([]string{"pair", "--data-dir", dir}, strings.NewReader(tc.input), &out, &diag, nil)
			if (code == exitOK) != tc.success {
				t.Fatalf("exit %d: %s", code, diag.String())
			}
			if so.started.Load() {
				t.Fatal("owner started")
			}
			st, err = store.Open(context.Background(), dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close(context.Background())
			state, _, err := st.RunGuard(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if tc.success && state != "clean" || !tc.success && state != "unsafe" {
				t.Fatalf("state %s", state)
			}
			if !tc.success {
				present, err := st.RecoveryIdentityPresent(context.Background())
				if err != nil || !present {
					t.Fatalf("identity=%v error=%v", present, err)
				}
			}
			db, err := sql.Open("sqlite", dbPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var count int
			if err := db.QueryRow("SELECT count(*) FROM whatsmeow_device").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if tc.success && count != 0 || !tc.success && count != 1 {
				t.Fatalf("identity count %d", count)
			}
		})
	}
}

func TestRecoveryTerminalRejectsPipesAndMixedStreams(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	for _, s := range []*streams{{stdin: r, stdout: w}, {stdin: r, stdout: io.Discard}, {stdin: strings.NewReader(""), stdout: w}} {
		if terminalStreams(s) {
			t.Fatal("pipe or mixed streams treated as terminal")
		}
	}
}

func TestRecoveryConsentBoundariesPreserveSyntheticData(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		identity    bool
		success     bool
	}{
		{"eof selecting recovery", "", true, false},
		{"eof selecting phone action", "recover\n", true, false},
		{"unknown phone action", "recover\nunknown\n", true, false},
		{"eof authorizing unlink", "recover\nunlink\n", true, false},
		{"unverified after unlink", "recover\nunlink\nauthorize-unlink\nno\n", true, false},
		{"eof after unlink", "recover\nunlink\nauthorize-unlink\n", true, false},
		{"eof local removal", "recover\nabsent\nverified\n", true, false},
		{"orphan cannot be assumed absent", "recover\nunknown\n", false, false},
		{"orphan unlink unverified", "recover\nunlink\nauthorize-unlink\n", false, false},
		{"orphan unlink verified", "recover\nunlink\nauthorize-unlink\nverified\n", false, true},
		{"identity unlink and removal", "recover\nunlink\nauthorize-unlink\nverified\nremove\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := privateDir(t)
			if tc.identity {
				seedSavedIdentity(t, dir)
			}
			st, err := store.Open(context.Background(), dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.ArmRunGuard(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", dbPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO chats (jid, name) VALUES ('synthetic-chat', 'sentinel');
				INSERT INTO messages (chat_jid, id, sender_jid, time, from_me, text) VALUES ('synthetic-chat', 'synthetic-message', 'synthetic-sender', 1, 0, 'sentinel-body')`); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			so := newScriptedOwner()
			withScriptedOwner(t, so)
			old := recoveryInteractive
			recoveryInteractive = func(*streams) bool { return true }
			t.Cleanup(func() { recoveryInteractive = old })
			var out, diag bytes.Buffer
			code := run([]string{"pair", "--data-dir", dir}, strings.NewReader(tc.input), &out, &diag, nil)
			if (code == exitOK) != tc.success {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, out.String(), diag.String())
			}
			if so.started.Load() || so.requestN.Load() != 0 {
				t.Fatal("recovery performed account work")
			}
			if strings.HasPrefix(tc.input, "recover\n") {
				for _, text := range []string{"orphaned phone link", "every potentially matching entry", "unrelated devices", "never unlinks"} {
					if !strings.Contains(out.String(), text) {
						t.Errorf("missing warning %q in %q", text, out.String())
					}
				}
			}
			st, err = store.Open(context.Background(), dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close(context.Background())
			state, _, err := st.RunGuard(context.Background())
			want := "unsafe"
			if tc.success {
				want = "clean"
			}
			if err != nil || state != want {
				t.Errorf("marker=%q err=%v, want %q", state, err, want)
			}
			db, err = sql.Open("sqlite", dbPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var identities int
			if err := db.QueryRow("SELECT count(*) FROM whatsmeow_device").Scan(&identities); err != nil || (identities == 1) != (tc.identity && !tc.success) {
				t.Errorf("identity count=%d err=%v", identities, err)
			}
			var name, body string
			if err := db.QueryRow(`SELECT chats.name, messages.text FROM chats JOIN messages ON messages.chat_jid=chats.jid WHERE chats.jid='synthetic-chat' AND messages.id='synthetic-message'`).Scan(&name, &body); err != nil || name != "sentinel" || body != "sentinel-body" {
				t.Errorf("synthetic data name=%q body=%q err=%v", name, body, err)
			}
		})
	}
}

func TestRecoveryTerminalDetectionFailure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "closed-terminal")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if terminalStreams(&streams{stdin: f, stdout: f}) {
		t.Fatal("closed handles accepted as interactive")
	}
}

func TestRecoveryCancellationRetainsMarker(t *testing.T) {
	dir := privateDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	if err := st.ArmRunGuard(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Reopen because recovery is forbidden in the invocation that armed the marker.
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	old := recoveryInteractive
	recoveryInteractive = func(*streams) bool { return true }
	defer func() { recoveryInteractive = old }()
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if recoverOffline(ctx, st, &streams{stdin: r, stdout: io.Discard, stderr: io.Discard}) {
		t.Fatal("cancelled recovery succeeded")
	}
	state, _, err := st.RunGuard(context.Background())
	if err != nil || state != "unsafe" {
		t.Fatalf("state=%s err=%v", state, err)
	}
}

func TestPairMissingMarkerRefusesRecovery(t *testing.T) {
	dir := privateDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM unsafe_run"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	so := newScriptedOwner()
	withScriptedOwner(t, so)
	old := recoveryInteractive
	recoveryInteractive = func(*streams) bool { t.Error("terminal detection called for invalid marker"); return true }
	defer func() { recoveryInteractive = old }()
	var out, diag bytes.Buffer
	if code := run([]string{"pair", "--data-dir", dir}, strings.NewReader("recover\nabsent\nverified\n"), &out, &diag, nil); code != exitError {
		t.Fatalf("exit %d", code)
	}
	if so.started.Load() || so.requestN.Load() != 0 {
		t.Fatal("account work with missing marker")
	}
}

func TestPairLinkedWithRecoveryRequiredExitsZeroWithNotice(t *testing.T) {
	so := newScriptedOwner()
	so.stopErr = store.ErrUnsafeRun
	so.startHook = func() { so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true}) }
	pr := startPair(t, so, nil, nil, "--data-dir", privateDir(t))
	if code := pr.waitExit(t, 10*time.Second); code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, pr.diagnostics())
	}
	if !strings.Contains(pr.diagnostics(), "Offline recovery is required before the next run") {
		t.Errorf("missing recovery notice: %s", pr.diagnostics())
	}
}

func TestPairLinkedWithStorageFaultStillFails(t *testing.T) {
	so := newScriptedOwner()
	so.stopErr = store.ErrUnsafeRun
	so.health = wa.Health{StorageFault: true}
	so.startHook = func() { so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true}) }
	pr := startPair(t, so, nil, nil, "--data-dir", privateDir(t))
	if code := pr.waitExit(t, 10*time.Second); code != exitError {
		t.Fatalf("exit %d", code)
	}
}

func TestPairLinkedWithOtherStopErrorStillFails(t *testing.T) {
	so := newScriptedOwner()
	so.stopErr = errors.New("synthetic stop failure")
	so.startHook = func() { so.setPhase(wa.PhaseConnected, wa.Status{Connected: true, LoggedIn: true}) }
	pr := startPair(t, so, nil, nil, "--data-dir", privateDir(t))
	if code := pr.waitExit(t, 10*time.Second); code != exitError {
		t.Fatalf("exit %d", code)
	}
}

func TestRunGuardUnsafePairRefusesRedirectedInput(t *testing.T) {
	dir := privateDir(t)
	st, err := store.Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "whatsapp.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS unsafe_run (id INTEGER PRIMARY KEY, state TEXT NOT NULL, repair_bits INTEGER NOT NULL);
		INSERT INTO unsafe_run (id, state, repair_bits) VALUES (1, 'unsafe', 0)
		ON CONFLICT(id) DO UPDATE SET state='unsafe'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	so := newScriptedOwner()
	withScriptedOwner(t, so)
	r := runIn(t, nil, "pair", "--data-dir", dir)
	r.expect(t, exitError)
	if so.started.Load() || so.requestN.Load() != 0 {
		t.Error("redirected input allowed account work on unsafe storage")
	}
	if !strings.Contains(strings.ToLower(r.stderr), "recover") {
		t.Error("missing offline recovery diagnostic")
	}
}

// The post-pair link has no switch: the pair command and the shells build the same owner, and only the shells
// carry the startup budget that denies every retry. A shell never starts a pairing window, so it has no 515 and no
// reconnect of any kind (the shell tests in store_test.go drive that end to end).
func TestProductionOwnerConfigHasNoReconnectSwitch(t *testing.T) {
	pair := productionOwnerConfig(ownerDeps{}, nil, nil)
	shell := productionOwnerConfig(ownerDeps{shell: true}, nil, nil)
	if pair.ShellStartupBudget || !shell.ShellStartupBudget {
		t.Errorf("startup budget: pair=%v shell=%v, want false and true", pair.ShellStartupBudget, shell.ShellStartupBudget)
	}
}

func TestProductionOwnerGetsTheShutdownTimeoutAsItsStopBound(t *testing.T) {
	for _, shell := range []bool{false, true} {
		if cfg := productionOwnerConfig(ownerDeps{shell: shell}, nil, nil); cfg.StopBound != shutdownTimeout {
			t.Errorf("shell=%v: StopBound = %v, want %v", shell, cfg.StopBound, shutdownTimeout)
		}
	}
}
