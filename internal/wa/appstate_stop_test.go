package wa

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
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

// A stop that arrives while the library's app-state fetches are queued on its own lock behind a request the
// server has not answered (as on the real account on 2026-11-09): the queued fetches read the stored version after
// the barrier retired. Those reads are refused and kept apart, so the check passes and the marker is clean; no
// write is refused. WA_APPSTATE_STOP_RUNS repeats it.
func TestSimulatorStopDuringQueuedAppStateFetches(t *testing.T) {
	runs, _ := strconv.Atoi(os.Getenv("WA_APPSTATE_STOP_RUNS"))
	if runs <= 0 {
		runs = 1
	}
	outcomes := map[string]int{}
	for i := 0; i < runs; i++ {
		r := stopDuringQueuedFetches(t)
		outcomes[r.reason]++
		detail := strings.Join(r.lines, "\n  ")
		if r.reason != "passed" || r.marker != "clean" {
			t.Errorf("run %d: check %s, marker %q; want passed and clean\n  %s", i+1, r.reason, r.marker, detail)
		}
		if r.refusedReads == 0 {
			t.Errorf("run %d: no read was refused after the barrier; the stop did not land while fetches were queued\n  %s", i+1, detail)
		}
		for _, l := range r.lines {
			if strings.HasPrefix(l, "store ") && !strings.HasPrefix(l, "store Get") {
				t.Errorf("run %d: a write was refused: %s", i+1, l)
			}
		}
	}
	if runs > 1 {
		t.Logf("outcomes over %d runs: %v", runs, outcomes)
	}
}

// queuedAppStateFetches counts goroutines blocked on a mutex inside the library's app-state fetch, which takes its
// appStateSyncLock first. The library offers no hook for this, so the test reads the goroutine dump.
func queuedAppStateFetches() int {
	buf := make([]byte, 4<<20)
	n := 0
	for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		if strings.Contains(g, "sync.(*Mutex).Lock") && strings.Contains(g, "whatsmeow.(*Client).fetchAppState(") {
			n++
		}
	}
	return n
}

type queuedFetchStop struct {
	reason       string
	marker       string
	refusedReads uint64
	lines        []string
}

func stopDuringQueuedFetches(t *testing.T) queuedFetchStop {
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
	var sess *Session
	o := NewOwner(OwnerConfig{
		Log: logger, Store: st, Versions: bundledProvider(ctx), ShellStartupBudget: true,
		trace: func(step string) { log.add("step " + step) },
		Sessions: func(cfg SessionConfig) (SessionHandle, error) {
			cfg.PreLoginHTTP = srv.Client()
			cfg.WebsocketHTTP = srv.Client()
			s, err := OpenSession(cfg)
			if err == nil {
				s.dev.AppState = &recordingAppState{AppStateStore: s.dev.AppState, log: log}
				sess = s
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
	// One fetch holds the library's lock while its request waits; the other four notifications' fetches must be
	// queued on that lock before the stop begins.
	waitFor(t, func() bool { return queuedAppStateFetches() >= 4 })
	log.add("stop begins")
	o.BeginStop()
	<-o.Done()
	h := o.Health()
	res := queuedFetchStop{reason: "passed", refusedReads: sess.barrier.RefusedReads()}
	if !h.QuietPassed {
		res.reason = h.QuietReason
	}
	state, _, err := st.RunGuard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res.marker = state
	res.lines = log.snapshot()
	for _, l := range mem.lines {
		if s := fmt.Sprint(l); strings.Contains(s, "quiet_stop") {
			res.lines = append(res.lines, "owner "+s)
		}
	}
	return res
}
