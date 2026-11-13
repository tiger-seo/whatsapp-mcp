package wa

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"github.com/tiger-seo/whatsapp-mcp/internal/wasim"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// nodeLog records every node the client sends on one simulated connection.
type nodeLog struct {
	mu      sync.Mutex
	nodes   []*waBinary.Node
	changed chan struct{}
}

func recordNodes(ctx context.Context, conn *wasim.Conn) *nodeLog {
	l := &nodeLog{changed: make(chan struct{})}
	go func() {
		for {
			n, err := conn.Recv(ctx)
			if err != nil {
				return
			}
			l.mu.Lock()
			l.nodes = append(l.nodes, n)
			close(l.changed)
			l.changed = make(chan struct{})
			l.mu.Unlock()
		}
	}()
	return l
}

func (l *nodeLog) matching(match func(*waBinary.Node) bool) []*waBinary.Node {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*waBinary.Node
	for _, n := range l.nodes {
		if match(n) {
			out = append(out, n)
		}
	}
	return out
}

// waitFor returns once a node matches, or fails the test at the context's end.
func (l *nodeLog) waitFor(t *testing.T, ctx context.Context, what string, match func(*waBinary.Node) bool) *waBinary.Node {
	t.Helper()
	for {
		l.mu.Lock()
		changed := l.changed
		for _, n := range l.nodes {
			if match(n) {
				l.mu.Unlock()
				return n
			}
		}
		l.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("no %s from the client", what)
		}
	}
}

func nodeFor(tag, id string, kind ...string) func(*waBinary.Node) bool {
	return func(n *waBinary.Node) bool {
		if n.Tag != tag || n.AttrGetter().OptionalString("id") != id {
			return false
		}
		got := n.AttrGetter().OptionalString("type")
		return len(kind) == 0 || got == kind[0]
	}
}

// ingestSimRig is a linked, connected account on the simulator with a peer that can send it messages.
type ingestSimRig struct {
	*ownerRig
	sessions *sessionLog
	conn     *wasim.Conn
	nodes    *nodeLog
	peer     *wasim.Peer
	keys     wasim.DeviceKeys
	stores   *faultyStore
}

// newIngestSimRig links the account; wrap, when set, sits between ingest and its store in every generation.
func newIngestSimRig(t *testing.T, ctx context.Context, wrap func(ingestStore) ingestStore) *ingestSimRig {
	t.Helper()
	r, sessions := newRecordedRig(t, func(cfg *OwnerConfig) {
		inner := cfg.Sessions
		cfg.Sessions = func(sc SessionConfig) (SessionHandle, error) {
			sc.wrapIngestStore = wrap
			return inner(sc)
		}
	})
	rig := &ingestSimRig{ownerRig: r, sessions: sessions}
	rig.conn = r.pairAndConnect(ctx)
	rig.nodes = recordNodes(ctx, rig.conn)
	var err error
	if rig.peer, err = wasim.NewPeer(peerJID()); err != nil {
		t.Fatal(err)
	}
	rig.keys = wasim.DeviceKeysOf(rig.session().dev)
	return rig
}

func (r *ingestSimRig) session() *Session { return r.sessions.get(r.sessions.count() - 1) }

func (r *ingestSimRig) send(ctx context.Context, n waBinary.Node) {
	r.t.Helper()
	if err := r.conn.Send(ctx, n); err != nil {
		r.t.Fatalf("send %s: %v", n.Tag, err)
	}
}

func (r *ingestSimRig) sendMessage(ctx context.Context, id string, msg *waE2E.Message) {
	r.t.Helper()
	n, err := r.peer.MessageNode(ctx, r.keys, id, time.Now(), msg)
	if err != nil {
		r.t.Fatal(err)
	}
	r.send(ctx, n)
}

func TestSimulatorIngestStoresAndAcknowledgesLiveMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := newIngestSimRig(t, ctx, nil)
	r.sendMessage(ctx, "SIM-TEXT", &waE2E.Message{Conversation: proto.String("hello from the simulator")})
	r.sendMessage(ctx, "SIM-IMG", imageMessage("a captioned image"))
	r.nodes.waitFor(t, ctx, "receipt for the text", nodeFor("receipt", "SIM-TEXT"))
	r.nodes.waitFor(t, ctx, "receipt for the image", nodeFor("receipt", "SIM-IMG"))

	text := getMessage(t, r.st, peerJID().String(), "SIM-TEXT")
	if text.Text != "hello from the simulator" || text.SenderJID != peerJID().String() || text.FromMe {
		t.Errorf("text stored as %+v", text)
	}
	img := getMessage(t, r.st, peerJID().String(), "SIM-IMG")
	if img.Caption != "a captioned image" || img.Text != "" || img.MediaType != "image" {
		t.Errorf("image stored as %+v", img)
	}
	if err := r.stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := r.runGuardState(); got != "clean" {
		t.Fatalf("marker %q after the stop, want clean\n%s", got, strings.Join(r.log.lines, "\n"))
	}
}

func TestSimulatorIngestStoreFailureIsNotAcknowledged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	f := &faultyStore{}
	f.setErr(fmt.Errorf("synthetic: %w", store.ErrDiskLow))
	r := newIngestSimRig(t, ctx, func(s ingestStore) ingestStore { f.ingestStore = s; return f })
	r.sendMessage(ctx, "SIM-FAIL", &waE2E.Message{Conversation: proto.String("lost")})
	select {
	case <-r.owner.Terminal():
	case <-ctx.Done():
		t.Fatal("the owner's terminal signal did not close after a failed write")
	}
	if !r.owner.Health().IngestFailed {
		t.Error("Health().IngestFailed = false")
	}
	// A placeholder is acknowledged by the next handler call, so every node for the failed message was sent by now.
	r.send(ctx, r.peer.UnavailableNode("SIM-AFTER", time.Now(), ""))
	r.nodes.waitFor(t, ctx, "ack for the placeholder", nodeFor("ack", "SIM-AFTER"))
	if got := r.nodes.matching(func(n *waBinary.Node) bool { return n.AttrGetter().OptionalString("id") == "SIM-FAIL" }); len(got) != 0 {
		t.Fatalf("the failed message was answered: %v", got)
	}
	if err := r.stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := r.runGuardState(); got != "clean" {
		t.Fatalf("marker %q, want clean: an ingest failure is not a device fault", got)
	}
}

func TestSimulatorIngestSendsNothingToThePhoneForOtherAccounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := newIngestSimRig(t, ctx, nil)
	r.send(ctx, r.peer.UndecryptableNode("SIM-BAD", time.Now()))
	r.send(ctx, r.peer.UnavailableNode("SIM-VO", time.Now(), "view_once"))
	// The handler runs one node at a time, so the sentinel's receipt comes after everything the first two caused.
	r.sendMessage(ctx, "SIM-SENTINEL", &waE2E.Message{Conversation: proto.String("sentinel")})
	r.nodes.waitFor(t, ctx, "receipt for the sentinel", nodeFor("receipt", "SIM-SENTINEL"))

	if got := r.nodes.matching(nodeFor("receipt", "SIM-BAD", "retry")); len(got) != 1 {
		t.Errorf("%d retry receipts for the undecryptable stanza, want 1", len(got))
	}
	if got := r.nodes.matching(nodeFor("ack", "SIM-BAD")); len(got) != 1 {
		t.Errorf("%d acks for the undecryptable stanza, want 1", len(got))
	}
	if got := r.nodes.matching(nodeFor("ack", "SIM-VO")); len(got) != 1 {
		t.Errorf("%d acks for the placeholder, want 1", len(got))
	}
	if got := r.nodes.matching(nodeFor("receipt", "SIM-VO")); len(got) != 0 {
		t.Errorf("the placeholder got receipts: %v", got)
	}
	if got := r.nodes.matching(func(n *waBinary.Node) bool { return n.Tag == "message" }); len(got) != 0 {
		t.Fatalf("the client sent %d messages, want none: %v", len(got), got)
	}
	if c := r.session().ingest.Counters(); c.Unavailable != 1 {
		t.Errorf("unavailable = %d, want 1", c.Unavailable)
	}
}

// blockingStore holds the first message write until it is released.
type blockingStore struct {
	ingestStore
	entered, release chan struct{}
	once             sync.Once
}

func (b *blockingStore) StoreMessage(ctx context.Context, m store.Message) error {
	return b.StoreMessages(ctx, []store.Message{m})
}

func (b *blockingStore) StoreMessages(ctx context.Context, msgs []store.Message) error {
	first := false
	b.once.Do(func() { first = true })
	if first {
		close(b.entered)
		<-b.release
	}
	return b.ingestStore.StoreMessages(ctx, msgs)
}

func TestSimulatorIngestReceiveOverloadIsReported(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	b := &blockingStore{entered: make(chan struct{}), release: make(chan struct{})}
	r := newIngestSimRig(t, ctx, func(s ingestStore) ingestStore { b.ingestStore = s; return b })
	released := false
	defer func() {
		if !released {
			close(b.release)
		}
	}()
	r.sendMessage(ctx, "SIM-BLOCK", &waE2E.Message{Conversation: proto.String("held")})
	select {
	case <-b.entered:
	case <-ctx.Done():
		t.Fatal("the handler never reached the store")
	}
	dials := r.srv.Dials()
	// The handler is held, so the library's queue of 2,048 items fills and the next item closes the connection.
	for i := 0; i < 2100; i++ {
		err := r.conn.Send(ctx, waBinary.Node{Tag: "receipt", Attrs: waBinary.Attrs{
			"from": peerJID(), "id": fmt.Sprintf("FILL-%d", i), "t": fmt.Sprint(time.Now().Unix()),
		}})
		if err != nil {
			break
		}
	}
	select {
	case <-r.conn.Done():
	case <-ctx.Done():
		t.Fatal("the client did not close the connection at the queue limit")
	}
	for r.owner.Health().ReceiveOverload == "" {
		select {
		case <-ctx.Done():
			t.Fatalf("Health().ReceiveOverload stayed empty\n%s", strings.Join(r.log.lines, "\n"))
		case <-time.After(5 * time.Millisecond):
		}
	}
	if got := r.owner.Health().ReceiveOverload; got != "item_limit" {
		t.Errorf("ReceiveOverload = %q, want item_limit", got)
	}
	close(b.release)
	released = true
	if err := r.stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	var overloads []string
	for _, l := range r.log.lines {
		if strings.Contains(l, "event=receive_overload") {
			overloads = append(overloads, l)
		}
	}
	if len(overloads) != 1 || !strings.Contains(overloads[0], "reason=item_limit") || !strings.Contains(overloads[0], "messages may be missing") {
		t.Errorf("receive_overload lines: %q", overloads)
	}
	if got := r.srv.Dials(); got != dials {
		t.Errorf("%d dials after the overload, want none: the connection must not be reopened", got-dials)
	}
}
