package wa

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
)

// appStopLog records the retirement steps and the app-state store calls in one ordered list.
type appStopLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *appStopLog) add(s string) {
	l.mu.Lock()
	l.lines = append(l.lines, s)
	l.mu.Unlock()
}

func (l *appStopLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// recordingAppState passes every call through and records the ones that fail.
type recordingAppState struct {
	wastore.AppStateStore
	log *appStopLog
}

func (r *recordingAppState) note(op, name string, err error) {
	if err != nil {
		r.log.add(fmt.Sprintf("store %s(%s) err=%v", op, name, err))
	}
}

func (r *recordingAppState) GetAppStateVersion(ctx context.Context, name string) (uint64, [128]byte, error) {
	v, h, err := r.AppStateStore.GetAppStateVersion(ctx, name)
	r.note("GetAppStateVersion", name, err)
	return v, h, err
}

func (r *recordingAppState) DeleteAppStateVersion(ctx context.Context, name string) error {
	err := r.AppStateStore.DeleteAppStateVersion(ctx, name)
	r.note("DeleteAppStateVersion", name, err)
	return err
}

func (r *recordingAppState) PutAppStateVersion(ctx context.Context, name string, version uint64, hash [128]byte) error {
	err := r.AppStateStore.PutAppStateVersion(ctx, name, version, hash)
	r.note("PutAppStateVersion", name, err)
	return err
}

func (r *recordingAppState) PutAppStateMutationMACs(ctx context.Context, name string, version uint64, m []wastore.AppStateMutationMAC) error {
	err := r.AppStateStore.PutAppStateMutationMACs(ctx, name, version, m)
	r.note("PutAppStateMutationMACs", name, err)
	return err
}

// Reproduces the real-account stop of 2026-11-09 that failed with storage_refused: the library's app-state fetches
// queue on its own lock behind one request the server has not answered, and the stop arrives. The test records
// which store calls are refused and the check's decision. Diagnostic: run with WA_APPSTATE_STOP=1.
func TestSimulatorStopDuringQueuedAppStateFetches(t *testing.T) {
	if os.Getenv("WA_APPSTATE_STOP") == "" {
		t.Skip("diagnostic; set WA_APPSTATE_STOP=1")
	}
	runs, _ := strconv.Atoi(os.Getenv("WA_APPSTATE_STOP_RUNS"))
	if runs <= 0 {
		runs = 10
	}
	outcomes := map[string]int{}
	for i := 0; i < runs; i++ {
		reason, lines := stopDuringQueuedFetches(t)
		outcomes[reason]++
		if i == 0 || reason != "passed" {
			t.Logf("run %d: %s\n  %s", i+1, reason, strings.Join(lines, "\n  "))
		}
	}
	t.Logf("outcomes over %d runs: %v", runs, outcomes)
}

func stopDuringQueuedFetches(t *testing.T) (string, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := waDataDir(t)
	st, err := store.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var first capture
	o1 := NewOwner(OwnerConfig{Log: slog.New(slog.DiscardHandler), Store: st, Versions: bundledProvider(ctx), Sessions: first.factory()})
	if err := o1.Start(ctx); err != nil {
		t.Fatal(err)
	}
	armAndSave(t, first.handle.(*Session))
	if err := o1.stopForTest(ctx); err != nil {
		t.Fatal(err)
	}
	st.Close(ctx)
	st, err = store.Open(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	srv := wasim.Start(t)
	defer srv.Close()

	log := &appStopLog{}
	logger, mem := newMemLog()
	o := NewOwner(OwnerConfig{
		Log: logger, Store: st, Versions: bundledProvider(ctx), ShellStartupBudget: true,
		trace: func(step string) { log.add("step " + step) },
		Sessions: func(cfg SessionConfig) (SessionHandle, error) {
			cfg.PreLoginHTTP = srv.Client()
			cfg.WebsocketHTTP = srv.Client()
			s, err := OpenSession(cfg)
			if err == nil {
				s.dev.AppState = &recordingAppState{AppStateStore: s.dev.AppState, log: log}
			}
			return s, err
		},
	})
	if err := o.Start(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := srv.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SendSuccess(ctx, types.NewJID(testOwnPhone, types.HiddenUserServer)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return o.Snapshot().Connected })

	// Hold the first app-state request unanswered; the server's read loop waits with it, as a slow server would.
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer close(release)
	conn.SetIQHandler(func(n *waBinary.Node) *waBinary.Node {
		if ns, _ := n.Attrs["xmlns"].(string); ns == "w:sync:app:state" {
			once.Do(func() { close(held) })
			select {
			case <-release:
			case <-conn.Done():
			}
		}
		return nil
	})
	for i, name := range []string{"regular_low", "regular_high", "regular", "critical_block", "critical_unblock_low"} {
		if err := conn.Send(ctx, waBinary.Node{Tag: "notification", Attrs: waBinary.Attrs{
			"from": types.ServerJID, "type": "server_sync", "id": "APPSTOP" + strconv.Itoa(i),
			"t": strconv.FormatInt(time.Now().Unix(), 10),
		}, Content: []waBinary.Node{{Tag: "collection", Attrs: waBinary.Attrs{"name": name, "version": "1"}}}}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-held:
	case <-ctx.Done():
		t.Fatal("no app-state request reached the server")
	}
	time.Sleep(200 * time.Millisecond) // let the other fetches queue on the library's lock
	log.add("stop begins")
	o.BeginStop()
	<-o.Done()
	h := o.Health()
	reason := "passed"
	if !h.QuietPassed {
		reason = h.QuietReason
	}
	lines := log.snapshot()
	for _, l := range mem.lines {
		if s := fmt.Sprint(l); strings.Contains(s, "quiet_stop") {
			lines = append(lines, "owner "+s)
		}
	}
	return reason, lines
}
