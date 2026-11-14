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
	log     *appStopLog
	mu      sync.Mutex
	gate    <-chan struct{}
	entered chan<- struct{}
}

func (r *recordingAppState) note(op, name string, err error) {
	if err != nil {
		r.log.add(fmt.Sprintf("store %s(%s) err=%v", op, name, err))
	}
}

func (r *recordingAppState) GetAppStateVersion(ctx context.Context, name string) (uint64, [128]byte, error) {
	r.mu.Lock()
	gate, entered := r.gate, r.entered
	r.gate, r.entered = nil, nil
	r.mu.Unlock()
	if gate != nil {
		close(entered)
		<-gate
	}
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

// A stop that arrives while one app-state fetch waits for a request the server has not answered and more
// server_sync notifications wait behind it (as on the real account on 2026-11-09). With synchronous
// acknowledgements the library runs each fetch on its event handler, so the waiting notifications sit in the
// handler queue rather than in goroutines queued on the library's lock. The fetches that run after the stop read
// the stored version after the barrier retired. Those reads are refused and kept apart, so the check passes and
// the marker is clean; no write is refused. WA_APPSTATE_STOP_RUNS repeats it.
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

// appStateFetchGoroutines counts goroutines inside the library's app-state fetch, and how many of them wait on a
// mutex. The library offers no hook for this, so the test reads the goroutine dump.
func appStateFetchGoroutines() (fetching, onLock int) {
	buf := make([]byte, 4<<20)
	for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		if strings.Contains(g, "whatsmeow.(*Client).fetchAppState(") {
			fetching++
			if strings.Contains(g, "sync.(*Mutex).Lock") {
				onLock++
			}
		}
	}
	return fetching, onLock
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
	var appState *recordingAppState
	readEntered, readRelease := make(chan struct{}), make(chan struct{})
	var releaseRead sync.Once
	defer releaseRead.Do(func() { close(readRelease) })
	o := NewOwner(OwnerConfig{
		Log: logger, Store: st, Versions: bundledProvider(ctx), ShellStartupBudget: true,
		trace: func(step string) {
			log.add("step " + step)
			if step == "barrier_retired" {
				releaseRead.Do(func() { close(readRelease) })
			}
		},
		Sessions: func(cfg SessionConfig) (SessionHandle, error) {
			cfg.PreLoginHTTP = srv.Client()
			cfg.WebsocketHTTP = srv.Client()
			s, err := OpenSession(cfg)
			if err == nil {
				appState = &recordingAppState{AppStateStore: s.dev.AppState, log: log}
				s.dev.AppState = appState
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

	// The server's read loop waits with the first app-state request, as a slow server would, and answers it with an
	// error once five more notifications are on the socket.
	held1, answer1 := make(chan struct{}), make(chan struct{})
	var heldOnce sync.Once
	conn.SetIQHandler(func(n *waBinary.Node) *waBinary.Node {
		if ns, _ := n.Attrs["xmlns"].(string); ns != "w:sync:app:state" {
			return nil
		}
		first := false
		heldOnce.Do(func() { first = true })
		if !first {
			return nil
		}
		close(held1)
		select {
		case <-answer1:
		case <-conn.Done():
			return nil
		}
		id, _ := n.Attrs["id"].(string)
		return &waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{"from": types.ServerJID, "id": id, "type": "error"},
			Content: []waBinary.Node{{Tag: "error", Attrs: waBinary.Attrs{"code": "500", "text": "internal-server-error"}}}}
	})
	notify := func(i int, name string) {
		t.Helper()
		if err := conn.Send(ctx, waBinary.Node{Tag: "notification", Attrs: waBinary.Attrs{
			"from": types.ServerJID, "type": "server_sync", "id": "APPSTOP" + strconv.Itoa(i),
			"t": strconv.FormatInt(time.Now().Unix(), 10),
		}, Content: []waBinary.Node{{Tag: "collection", Attrs: waBinary.Attrs{"name": name, "version": "1"}}}}); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"regular_low", "regular_high", "regular", "critical_block", "critical_unblock_low", "regular_low"}
	notify(0, names[0])
	select {
	case <-held1:
	case <-ctx.Done():
		t.Fatal("no app-state request reached the server")
	}
	// The handler is inside the first fetch, so these five can only wait in the handler queue.
	for i := 1; i < len(names); i++ {
		notify(i, names[i])
	}
	// The next fetch's version read waits at the gate until the barrier has retired. The answer is written after the
	// five notifications, so by the time it reaches the waiting fetch they have been read and queued; the handler
	// then takes the second notification and stops inside its read, with four behind it.
	appState.mu.Lock()
	appState.gate, appState.entered = readRelease, readEntered
	appState.mu.Unlock()
	close(answer1)
	select {
	case <-readEntered:
	case <-ctx.Done():
		t.Fatal("the second fetch never read its version")
	}
	// One fetch is in progress on the handler and four notifications wait behind it; none waits on the library's
	// lock in a goroutine of its own.
	if fetching, onLock := appStateFetchGoroutines(); fetching != 1 || onLock != 0 {
		t.Fatalf("app-state fetch goroutines = %d (%d on a lock), want one in progress and none queued on the lock", fetching, onLock)
	}
	log.add("stop begins")
	o.BeginStop()
	select {
	case <-o.Done():
	case <-ctx.Done():
		t.Fatal("stop did not finish before timeout")
	}
	select {
	case <-readEntered:
	default:
		t.Error("no queued app-state version read entered the gate")
	}
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
