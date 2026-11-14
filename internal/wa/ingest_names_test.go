package wa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// nameStore is the real store with the contact table, name writes and message writes under the test's control, and
// every name read counted.
type nameStore struct {
	ingestStore
	mu         sync.Mutex
	contacts   map[string][2]string // jid -> saved, real
	noMessages bool                 // message writes succeed without reaching the store
	updateErr  error                // returned by every chat update while set
	chatReads  map[string]int
	contactRds map[string]int
	updates    []store.ChatUpdate
}

func (n *nameStore) ChatName(ctx context.Context, jid string) (string, store.NameQuality, time.Time, bool, error) {
	n.mu.Lock()
	if n.chatReads == nil {
		n.chatReads = map[string]int{}
	}
	n.chatReads[jid]++
	n.mu.Unlock()
	if n.noMessages {
		return "", store.NameNone, time.Time{}, false, nil
	}
	return n.ingestStore.ChatName(ctx, jid)
}

func (n *nameStore) ContactNames(_ context.Context, jid string) (string, string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.contactRds == nil {
		n.contactRds = map[string]int{}
	}
	n.contactRds[jid]++
	c := n.contacts[jid]
	return c[0], c[1], nil
}

func (n *nameStore) StoreMessage(ctx context.Context, m store.Message) error {
	if n.noMessages {
		return nil
	}
	return n.ingestStore.StoreMessage(ctx, m)
}

func (n *nameStore) UpdateChats(ctx context.Context, u []store.ChatUpdate) error {
	n.mu.Lock()
	err := n.updateErr
	n.updates = append(n.updates, u...)
	n.mu.Unlock()
	if err != nil {
		return err
	}
	if n.noMessages {
		return nil
	}
	return n.ingestStore.UpdateChats(ctx, u)
}

func (n *nameStore) nameWrites() []store.ChatUpdate {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []store.ChatUpdate
	for _, u := range n.updates {
		if u.Name != "" {
			out = append(out, u)
		}
	}
	return out
}

func (n *nameStore) reads(jid string) (chat, contact int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.chatReads[jid], n.contactRds[jid]
}

func newNameRig(t *testing.T, ns *nameStore, opts ...func(*IngestConfig)) *ingestRig {
	t.Helper()
	return newIngestRig(t, append([]func(*IngestConfig){func(cfg *IngestConfig) {
		ns.ingestStore = cfg.Store
		cfg.store = ns
	}}, opts...)...)
}

var nameBase = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// nameEvent is a text message in chat from sender with a push name, minute minutes after nameBase.
func nameEvent(id string, chat, sender types.JID, push string, minute int) *events.Message {
	evt := liveEvent(id, &waE2E.Message{Conversation: proto.String("text " + id)})
	evt.Info.Chat, evt.Info.Sender = chat, sender
	evt.Info.IsGroup = chat.Server == types.GroupServer
	evt.Info.PushName = push
	evt.Info.Timestamp = nameBase.Add(time.Duration(minute) * time.Minute)
	return evt
}

func chatNameOf(t *testing.T, st *store.Store, jid string) (string, store.NameQuality, time.Time) {
	t.Helper()
	name, q, at, _, err := st.ChatName(context.Background(), jid)
	if err != nil {
		t.Fatalf("ChatName(%s): %v", jid, err)
	}
	return name, q, at
}

func handle(t *testing.T, r *ingestRig, evt *events.Message) {
	t.Helper()
	if !r.in.HandleMessage(evt) {
		t.Fatalf("HandleMessage(%s) = false", evt.Info.ID)
	}
}

func TestIngestNameStoredNameIsReadOnceAndWorseIsNeverWritten(t *testing.T) {
	ns := &nameStore{}
	r := newNameRig(t, ns)
	peer := peerJID().String()
	// The stored name is newer than every message below, so no push name can replace it.
	if err := r.st.UpdateChat(context.Background(), store.ChatUpdate{JID: peer, Name: "Alice", NameQuality: store.NameReal,
		NameTime: nameBase.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		handle(t, r, nameEvent(fmt.Sprintf("M%d", i), peerJID(), peerJID(), "Zed", i))
	}
	if w := ns.nameWrites(); len(w) != 0 {
		t.Errorf("name writes = %+v, want none: the stored name is newer", w)
	}
	if chat, _ := ns.reads(peer); chat != 1 {
		t.Errorf("stored name reads = %d for 3 messages, want 1", chat)
	}
	if name, q, _ := chatNameOf(t, r.st, peer); name != "Alice" || q != store.NameReal {
		t.Errorf("stored name = %q (%v), want Alice", name, q)
	}
}

func TestIngestNameDirectOrder(t *testing.T) {
	peer := peerJID().String()
	cases := []struct {
		name     string
		contact  [2]string
		push     string
		fromMe   bool
		want     string
		wantQual store.NameQuality
	}{
		{"saved name wins", [2]string{"Mum", "Bank"}, "Pushy", false, "Mum", store.NameSaved},
		{"real contact name next", [2]string{"", "Bank"}, "Pushy", false, "Bank", store.NameReal},
		{"push name of the message next", [2]string{}, "Pushy", false, "Pushy", store.NameReal},
		{"an own message's push name is the owner's", [2]string{}, "Owner Name", true, testPeer, store.NameFallback},
		{"the JID user part is the last resort", [2]string{}, "", false, testPeer, store.NameFallback},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ns := &nameStore{contacts: map[string][2]string{peer: c.contact}}
			r := newNameRig(t, ns)
			evt := nameEvent("D1", peerJID(), peerJID(), c.push, 0)
			if c.fromMe {
				evt.Info.IsFromMe = true
				evt.Info.Sender = ownJID()
			}
			handle(t, r, evt)
			name, q, _ := chatNameOf(t, r.st, peer)
			if name != c.want || q != c.wantQual {
				t.Errorf("stored name = %q (%v), want %q (%v)", name, q, c.want, c.wantQual)
			}
			if w := ns.nameWrites(); len(w) != 1 {
				t.Errorf("name writes = %d, want 1 (only the best offer is written)", len(w))
			}
		})
	}
}

func TestIngestNameOfferedOnlyWhenBetterOrNewer(t *testing.T) {
	ns := &nameStore{}
	r := newNameRig(t, ns)
	peer := peerJID().String()
	handle(t, r, nameEvent("A", peerJID(), peerJID(), "", 0)) // fallback
	handle(t, r, nameEvent("B", peerJID(), peerJID(), "P1", 5))
	handle(t, r, nameEvent("C", peerJID(), peerJID(), "P1", 6)) // same name: nothing to change
	handle(t, r, nameEvent("D", peerJID(), peerJID(), "", 7))   // no push name: the fallback is worse
	handle(t, r, nameEvent("E", peerJID(), peerJID(), "P0", 4)) // older at the same quality
	handle(t, r, nameEvent("F", peerJID(), peerJID(), "P2", 8)) // newer at the same quality
	var got []string
	for _, u := range ns.nameWrites() {
		got = append(got, u.Name)
	}
	if want := []string{testPeer, "P1", "P2"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("name writes = %v, want %v", got, want)
	}
	if name, _, at := chatNameOf(t, r.st, peer); name != "P2" || !at.Equal(nameBase.Add(8*time.Minute)) {
		t.Errorf("stored name = %q at %v, want P2 at the message's time", name, at)
	}
}

func TestIngestNameSavedNameIsNeverReplacedByLaterOffers(t *testing.T) {
	peer := peerJID().String()
	ns := &nameStore{contacts: map[string][2]string{peer: {"Mum", ""}}}
	r := newNameRig(t, ns)
	handle(t, r, nameEvent("A", peerJID(), peerJID(), "Pushy", 0))
	handle(t, r, nameEvent("B", peerJID(), peerJID(), "Newer Push", 9))
	if w := ns.nameWrites(); len(w) != 1 || w[0].Name != "Mum" {
		t.Errorf("name writes = %+v, want one: Mum", w)
	}
	if _, contact := ns.reads(peer); contact != 1 {
		t.Errorf("contact table reads = %d, want 1 per chat per generation", contact)
	}
}

func groupInfo(name string) *types.GroupInfo {
	return &types.GroupInfo{JID: groupJID(), GroupName: types.GroupName{Name: name}}
}

func TestIngestGroupLookedUpOnceForManyMessages(t *testing.T) {
	ns := &nameStore{}
	r := newNameRig(t, ns)
	release := make(chan struct{})
	var libOK atomic.Bool
	var deadline atomic.Int64
	r.client.group = func(ctx context.Context, jid types.JID) (*types.GroupInfo, error) {
		libOK.Store(isLibContext(ctx))
		if d, ok := ctx.Deadline(); ok {
			deadline.Store(int64(time.Until(d)))
		}
		<-release
		return groupInfo("Team"), nil
	}
	group := groupJID().String()
	for i := range 100 {
		handle(t, r, nameEvent(fmt.Sprintf("G%d", i), groupJID(), peerJID(), "Pushy", i))
	}
	if name, q, _ := chatNameOf(t, r.st, group); name != "Group "+testGroup || q != store.NameFallback {
		t.Errorf("name while the lookup runs = %q (%v), want %q as a fallback", name, q, "Group "+testGroup)
	}
	close(release)
	waitFor(t, func() bool { n, _, _ := chatNameOf(t, r.st, group); return n == "Team" })
	if calls := r.client.groupCalls(); len(calls) != 1 || calls[0] != groupJID() {
		t.Errorf("group lookups = %v for 100 messages, want exactly one", calls)
	}
	if !libOK.Load() {
		t.Error("the lookup did not run under the library context")
	}
	if d := time.Duration(deadline.Load()); d <= 9*time.Second || d > 10*time.Second {
		t.Errorf("lookup deadline = %v away, want about 10 s", d)
	}
	if _, q, _ := chatNameOf(t, r.st, group); q != store.NameReal {
		t.Errorf("subject quality = %v, want real", q)
	}
	// A message after the success never asks again.
	handle(t, r, nameEvent("G-late", groupJID(), peerJID(), "Pushy", 200))
	if calls := r.client.groupCalls(); len(calls) != 1 {
		t.Errorf("group lookups after success = %d, want 1", len(calls))
	}
}

func TestIngestGroupWithAStoredNameNeverLooksUp(t *testing.T) {
	r := newNameRig(t, &nameStore{})
	group := groupJID().String()
	if err := r.st.UpdateChat(context.Background(), store.ChatUpdate{JID: group, Name: "From history", NameQuality: store.NameReal,
		NameTime: nameBase}); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		handle(t, r, nameEvent(fmt.Sprintf("H%d", i), groupJID(), peerJID(), "Pushy", i))
	}
	if calls := r.client.groupCalls(); len(calls) != 0 {
		t.Errorf("group lookups = %v, want none for a group with a history name", calls)
	}
	if name, _, _ := chatNameOf(t, r.st, group); name != "From history" {
		t.Errorf("name = %q, want the history name kept", name)
	}
}

func TestIngestFailedGroupLookupIsNotRepeatedForTenMinutes(t *testing.T) {
	r := newNameRig(t, &nameStore{})
	now := nameBase
	var nowMu sync.Mutex
	r.in.now = func() time.Time { nowMu.Lock(); defer nowMu.Unlock(); return now }
	advance := func(d time.Duration) { nowMu.Lock(); now = now.Add(d); nowMu.Unlock() }
	r.client.group = func(context.Context, types.JID) (*types.GroupInfo, error) { return nil, errors.New("not in group") }
	send := func(id string, minute int) {
		handle(t, r, nameEvent(id, groupJID(), peerJID(), "", minute))
	}
	send("F1", 0)
	waitFor(t, func() bool { return len(r.client.groupCalls()) == 1 })
	advance(9 * time.Minute)
	for i := range 3 {
		send(fmt.Sprintf("F-wait%d", i), 1+i)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(r.client.groupCalls()); n != 1 {
		t.Fatalf("lookups within 10 minutes of a failure = %d, want 1", n)
	}
	advance(2 * time.Minute)
	send("F2", 10)
	waitFor(t, func() bool { return len(r.client.groupCalls()) == 2 })
	if r.failed.Load() != 0 {
		t.Error("a failed lookup made ingest terminal; it must only be remembered")
	}
}

func TestFailureCacheHoldsOneThousandAndTheOldestGoesFirst(t *testing.T) {
	c := newFailureCache()
	now := nameBase
	for i := range 1100 {
		c.add(fmt.Sprintf("g%d", i), now.Add(time.Duration(i)*time.Millisecond))
	}
	if c.len() != failureCacheSize || failureCacheSize != 1024 {
		t.Fatalf("entries = %d (limit %d), want 1024", c.len(), failureCacheSize)
	}
	if c.recent("g0", now.Add(time.Minute)) || c.recent("g75", now.Add(time.Minute)) {
		t.Error("the oldest entries were kept")
	}
	if !c.recent("g1099", now.Add(time.Minute)) || !c.recent("g76", now.Add(time.Minute)) {
		t.Error("a recent entry was forgotten")
	}
	if c.recent("g1099", now.Add(2*time.Second+failureTTL)) {
		t.Error("an entry older than 10 minutes still counts")
	}
}

func TestIngestLookupSetHoldsSixtyFourGroups(t *testing.T) {
	r := newNameRig(t, &nameStore{})
	release := make(chan struct{})
	var started atomic.Int64
	r.client.group = func(ctx context.Context, jid types.JID) (*types.GroupInfo, error) {
		started.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, errors.New("gone")
	}
	for i := range 70 {
		chat := types.NewJID(fmt.Sprintf("1203630000000%05d", i), types.GroupServer)
		handle(t, r, nameEvent(fmt.Sprintf("S%d", i), chat, peerJID(), "", i))
	}
	if got := r.in.Counters().LookupsDropped; got != 6 {
		t.Errorf("lookups dropped = %d for 70 groups, want 6 (64 are held)", got)
	}
	close(release)
	waitFor(t, func() bool { return len(r.client.groupCalls()) == 64 })
}

func TestIngestNameStateStaysBoundedForTenThousandGroups(t *testing.T) {
	ns := &nameStore{noMessages: true}
	r := newNameRig(t, ns)
	r.client.group = func(context.Context, types.JID) (*types.GroupInfo, error) { return nil, errors.New("gone") }
	for i := range 10000 {
		chat := types.NewJID(fmt.Sprintf("1203630%08d", i), types.GroupServer)
		handle(t, r, nameEvent(fmt.Sprintf("B%d", i), chat, peerJID(), "", i%1000))
	}
	waitFor(t, func() bool { return r.in.namePending() == 0 })
	if n := r.in.nameCacheLen(); n > nameCacheSize || nameCacheSize != 4096 {
		t.Errorf("name cache holds %d chats, limit %d, want 4096", n, nameCacheSize)
	}
	if n := r.in.failureCacheLen(); n > failureCacheSize {
		t.Errorf("failure cache holds %d groups, want at most 1024", n)
	}
	if n := r.in.nameWorkersStarted(); n != 1 {
		t.Errorf("group-name workers started = %d, want 1", n)
	}
	if n := r.in.namePendingMax(); n > groupLookupSetSize {
		t.Errorf("lookup set reached %d groups, want at most 64", n)
	}
}

func TestIngestFailedNameWriteIsTerminalButTheMessageStays(t *testing.T) {
	ns := &nameStore{updateErr: errors.New("disk on fire")}
	r := newNameRig(t, ns)
	evt := nameEvent("W1", peerJID(), peerJID(), "Pushy", 0)
	if !r.in.HandleMessage(evt) {
		t.Fatal("HandleMessage = false, want true: the message was stored")
	}
	if got := getMessage(t, r.st, peerJID().String(), "W1"); got.Text != "text W1" {
		t.Errorf("stored message = %+v", got)
	}
	if r.failed.Load() != 1 || r.in.State() != ingestFailed {
		t.Errorf("failed callbacks = %d, state = %v; want one failure", r.failed.Load(), r.in.State())
	}
	ev := r.logs.events("ingest_failed")
	if len(ev) != 1 || ev[0]["operation"] != "name" {
		t.Errorf("ingest_failed records = %v, want one with operation name", ev)
	}
	if r.in.HandleMessage(nameEvent("W2", peerJID(), peerJID(), "", 1)) {
		t.Error("a message after the terminal failure was accepted")
	}
}

// A chat that only history created is named like any other: the conversation's own name, the saved contact name, the
// contact's own name, then the number; a group without a name gets "Group <id>" and one lookup.
func historyNameRig(t *testing.T, ns *nameStore, hist ...*waHistorySync.HistorySync) *historyRig {
	t.Helper()
	h := newHistoryRig(t, hist...)
	ns.ingestStore = h.in.st
	h.in.st = ns
	return h
}

func TestIngestHistoryNamesDirectChats(t *testing.T) {
	peer := peerJID().String()
	cases := []struct {
		name     string
		display  string
		contact  [2]string
		want     string
		wantQual store.NameQuality
	}{
		{"saved contact name for a chat without a conversation name", "", [2]string{"Mum", "Bank"}, "Mum", store.NameSaved},
		{"real contact name", "", [2]string{"", "Bank"}, "Bank", store.NameReal},
		{"the number as the last resort", "", [2]string{}, testPeer, store.NameFallback},
		{"a saved contact name beats the conversation's own name", "Chat Name", [2]string{"Mum", ""}, "Mum", store.NameSaved},
		{"the conversation's name is kept when nothing is better", "Chat Name", [2]string{}, "Chat Name", store.NameReal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ns := &nameStore{contacts: map[string][2]string{peer: c.contact}}
			h := historyNameRig(t, ns, historyOf(conversation(peerJID(), c.display, webText("HN1", "x", histTime), webText("HN2", "y", histTime.Add(time.Second)))))
			h.deliver(t, 1)
			if got, q, _ := chatNameOf(t, h.st, peer); got != c.want || q != c.wantQual {
				t.Errorf("stored name = %q (%v), want %q (%v)", got, q, c.want, c.wantQual)
			}
			if _, contact := ns.reads(peer); contact != 1 {
				t.Errorf("contact table reads = %d for two messages, want 1", contact)
			}
		})
	}
}

// groupText is a history message in a group; the library's parser needs the participant.
func groupText(id, text string, ts time.Time) *waWeb.WebMessageInfo {
	m := webText(id, text, ts)
	m.Key.Participant = proto.String(peerJID().String())
	return m
}

func TestIngestHistoryNamesGroups(t *testing.T) {
	t.Run("a group without a name gets the fallback and one lookup", func(t *testing.T) {
		h := historyNameRig(t, &nameStore{}, historyOf(conversation(groupJID(), "", groupText("HG1", "x", histTime), groupText("HG2", "y", histTime.Add(time.Second)))))
		h.client.group = func(context.Context, types.JID) (*types.GroupInfo, error) { return groupInfo("Team"), nil }
		h.deliver(t, 1)
		waitFor(t, func() bool { n, _, _ := chatNameOf(t, h.st, groupJID().String()); return n == "Team" })
		if calls := h.client.groupCalls(); len(calls) != 1 {
			t.Errorf("group lookups = %v, want 1", calls)
		}
	})
	t.Run("a group with a conversation name is never looked up", func(t *testing.T) {
		h := historyNameRig(t, &nameStore{}, historyOf(conversation(groupJID(), "History Name", groupText("HG3", "x", histTime))))
		h.deliver(t, 1)
		if got, q, _ := chatNameOf(t, h.st, groupJID().String()); got != "History Name" || q != store.NameReal {
			t.Errorf("name = %q (%v), want the conversation's name", got, q)
		}
		time.Sleep(50 * time.Millisecond)
		if calls := h.client.groupCalls(); len(calls) != 0 {
			t.Errorf("group lookups = %v, want none", calls)
		}
	})
}

func TestIngestHistoryNameWritesAreBatchedAndFailuresAreTerminal(t *testing.T) {
	var convs []*waHistorySync.Conversation
	ns := &nameStore{contacts: map[string][2]string{}}
	for i := range 40 {
		jid := types.NewJID(fmt.Sprintf("4915100000%02d", i), types.DefaultUserServer)
		ns.contacts[jid.String()] = [2]string{fmt.Sprintf("Contact %d", i), ""}
		convs = append(convs, conversation(jid, "", webText(fmt.Sprintf("HB%d", i), "x", histTime)))
	}
	h := historyNameRig(t, ns, historyOf(convs...))
	h.deliver(t, 1)
	if w := ns.nameWrites(); len(w) != 40 {
		t.Fatalf("name writes = %d, want 40", len(w))
	}
	ns.mu.Lock()
	calls := 0
	for range ns.updates {
		calls++
	}
	ns.mu.Unlock()
	if calls != 40 {
		t.Fatalf("recorded %d updates", calls)
	}

	failing := &nameStore{updateErr: errors.New("disk on fire")}
	h2 := historyNameRig(t, failing, historyOf(conversation(peerJID(), "", webText("HF1", "x", histTime))))
	h2.deliver(t, 1)
	if h2.failed.Load() != 1 {
		t.Errorf("failed callbacks = %d, want 1: a failed name write is terminal", h2.failed.Load())
	}
	if ev := h2.logs.events("ingest_failed"); len(ev) != 1 || ev[0]["operation"] != "name" {
		t.Errorf("ingest_failed records = %v, want one with operation name", ev)
	}
}

// A name offer that reaches the store after a run-time merge moved the LID chat away must not bring the empty LID chat
// back.
func TestIngestNameOfferNeverRecreatesAMergedAwayChat(t *testing.T) {
	ctx := context.Background()
	lid := lidJID()
	phone := peerJID().String()
	for _, path := range []string{"live", "history"} {
		t.Run(path, func(t *testing.T) {
			ns := &nameStore{contacts: map[string][2]string{lid.String(): {"Mum", ""}}}
			r := newNameRig(t, ns)
			m := store.Message{ChatJID: lid.String(), ID: "ML1", SenderJID: lid.String(), Time: nameBase, Text: "x"}
			if err := r.st.StoreMessage(ctx, m); err != nil {
				t.Fatal(err)
			}
			if err := r.st.MergeLID(ctx, lid.String(), phone); err != nil {
				t.Fatal(err)
			}
			if _, err := r.st.GetChat(ctx, lid.String()); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("the merge left the LID chat: %v", err)
			}
			// The offer arrives late, after the merge.
			evt := nameEvent("ML1", lid, lid, "Pushy", 0)
			switch path {
			case "live":
				if !r.in.offerName(evt, m) {
					t.Fatal("offerName reported a terminal failure")
				}
			default:
				if !r.in.offerHistoryNames(map[string]time.Time{lid.String(): nameBase}) {
					t.Fatal("offerHistoryNames reported a terminal failure")
				}
			}
			if _, err := r.st.GetChat(ctx, lid.String()); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("a late name offer recreated the merged-away LID chat: %v", err)
			}
		})
	}
}
