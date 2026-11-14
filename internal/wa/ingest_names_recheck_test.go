package wa

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/types"
)

// recheckClock replaces the rig's clock with one the test moves.
type recheckClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *recheckClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *recheckClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newRecheckClock(in *ingest) *recheckClock {
	c := &recheckClock{t: nameBase}
	in.now = c.now
	return c
}

func otherPeer() types.JID { return types.NewJID("491555000111", types.DefaultUserServer) }

func TestIngestNameLateSavedNameIsOfferedAfterTheDelay(t *testing.T) {
	peer := peerJID().String()
	ns := &nameStore{}
	r := newNameRig(t, ns)
	clock := newRecheckClock(r.in)
	handle(t, r, nameEvent("A", peerJID(), peerJID(), "Pushy", 0)) // read: no contact yet, real name from the push name
	if name, q, _ := chatNameOf(t, r.st, peer); name != "Pushy" || q != store.NameReal {
		t.Fatalf("first name = %q (%v), want Pushy at real", name, q)
	}
	ns.setContact(peer, "Mum", "") // the contact sync has stored the saved name

	clock.advance(30 * time.Second)
	handle(t, r, nameEvent("B", otherPeer(), otherPeer(), "", 1))
	if _, contact := ns.reads(peer); contact != 1 {
		t.Fatalf("contact reads after 30 s = %d, want 1: the chat is not due yet", contact)
	}

	clock.advance(31 * time.Second)
	// Any message triggers the recheck, not only one in the chat.
	handle(t, r, nameEvent("C", otherPeer(), otherPeer(), "", 2))
	if _, contact := ns.reads(peer); contact != 2 {
		t.Errorf("contact reads after 61 s = %d, want 2", contact)
	}
	if name, q, _ := chatNameOf(t, r.st, peer); name != "Mum" || q != store.NameSaved {
		t.Errorf("stored name = %q (%v), want Mum at saved", name, q)
	}
	clock.advance(time.Hour)
	handle(t, r, nameEvent("D", otherPeer(), otherPeer(), "", 3))
	if _, contact := ns.reads(peer); contact != 2 {
		t.Errorf("contact reads after a saved name = %d, want no more", contact)
	}
}

func TestIngestNameChatWithSavedNameAtFirstReadIsNeverRechecked(t *testing.T) {
	peer := peerJID().String()
	ns := &nameStore{contacts: map[string][2]string{peer: {"Mum", ""}}}
	r := newNameRig(t, ns)
	clock := newRecheckClock(r.in)
	handle(t, r, nameEvent("A", peerJID(), peerJID(), "", 0))
	if n := r.in.nm.recheckLen(); n != 0 {
		t.Errorf("recheck queue = %d, want 0 for a chat at the saved quality", n)
	}
	clock.advance(24 * time.Hour)
	handle(t, r, nameEvent("B", otherPeer(), otherPeer(), "", 1))
	if _, contact := ns.reads(peer); contact != 1 {
		t.Errorf("contact reads = %d, want 1", contact)
	}
}

func TestIngestNameChatWithoutASavedNameIsRecheckedThreeTimesAtMost(t *testing.T) {
	peer := peerJID().String()
	ns := &nameStore{}
	r := newNameRig(t, ns)
	clock := newRecheckClock(r.in)
	handle(t, r, nameEvent("A", peerJID(), peerJID(), "", 0))
	trigger := func(id string) { handle(t, r, nameEvent(id, otherPeer(), otherPeer(), "", 1)) }
	steps := []struct {
		advance time.Duration
		reads   int
	}{
		{59 * time.Second, 1},
		{2 * time.Second, 2},  // 61 s after the first read
		{4 * time.Minute, 2},  // not yet 5 min after the second
		{2 * time.Minute, 3},  // 5 min after the second
		{29 * time.Minute, 3}, // not yet 30 min after the third
		{2 * time.Minute, 4},  // 30 min after the third
		{48 * time.Hour, 4},   // never again
	}
	for i, s := range steps {
		clock.advance(s.advance)
		trigger(fmt.Sprintf("T%d", i))
		if _, contact := ns.reads(peer); contact != s.reads {
			t.Fatalf("step %d: contact reads = %d, want %d", i, contact, s.reads)
		}
	}
	if n := r.in.nm.recheckLen(); n != 0 {
		t.Errorf("recheck queue = %d after the last attempt, want 0", n)
	}
}

func TestIngestNameRecheckTakesAFewChatsPerCall(t *testing.T) {
	ns := &nameStore{}
	r := newNameRig(t, ns)
	clock := newRecheckClock(r.in)
	const chats = 20
	for i := 0; i < chats; i++ {
		jid := fmt.Sprintf("4915%08d@%s", i, types.DefaultUserServer)
		r.in.nm.put(nameEntry{jid: jid, contactChecked: true, quality: store.NameFallback, name: "x"})
		r.in.queueRecheck(jid, 0, clock.now().Add(time.Second))
	}
	clock.advance(time.Minute)
	r.in.recheckNames()
	total := 0
	for i := 0; i < chats; i++ {
		_, c := ns.reads(fmt.Sprintf("4915%08d@%s", i, types.DefaultUserServer))
		total += c
	}
	if total != recheckPerCall {
		t.Errorf("contact reads in one call = %d, want %d", total, recheckPerCall)
	}
	if left := r.in.nm.recheckLen(); left != chats {
		// The eight taken are queued again with a later due time.
		t.Errorf("recheck queue = %d, want %d", left, chats)
	}
}

func TestIngestNameRecheckQueueIsBoundedAndHoldsAChatOnce(t *testing.T) {
	r := newNameRig(t, &nameStore{})
	due := nameBase.Add(time.Minute)
	for i := 0; i < nameCacheSize+50; i++ {
		r.in.queueRecheck(fmt.Sprintf("c%d@%s", i, types.DefaultUserServer), 0, due)
	}
	if n := r.in.nm.recheckLen(); n != nameCacheSize {
		t.Errorf("recheck queue = %d, want the cap %d", n, nameCacheSize)
	}
	r2 := newNameRig(t, &nameStore{})
	r2.in.queueRecheck("same@s.whatsapp.net", 0, due)
	r2.in.queueRecheck("same@s.whatsapp.net", 1, due)
	if n := r2.in.nm.recheckLen(); n != 1 {
		t.Errorf("a chat queued twice = %d entries, want 1", n)
	}
}

func TestIngestNameRecheckForgetsAnEvictedChat(t *testing.T) {
	ns := &nameStore{}
	r := newNameRig(t, ns)
	clock := newRecheckClock(r.in)
	jid := peerJID().String()
	r.in.queueRecheck(jid, 0, clock.now().Add(time.Second)) // never cached
	clock.advance(time.Minute)
	r.in.recheckNames()
	if _, c := ns.reads(jid); c != 0 {
		t.Errorf("contact reads for a chat no longer cached = %d, want 0", c)
	}
	if n := r.in.nm.recheckLen(); n != 0 {
		t.Errorf("recheck queue = %d, want 0", n)
	}
}

func TestIngestNameRecheckNeverHoldsTheIngestLock(t *testing.T) {
	peer := peerJID().String()
	ns := &nameStore{}
	r := newNameRig(t, ns)
	clock := newRecheckClock(r.in)
	handle(t, r, nameEvent("A", peerJID(), peerJID(), "", 0))
	held := false
	ns.contactHook = func() {
		if len(r.in.lock) != 0 {
			held = true
		}
	}
	ns.setContact(peer, "Mum", "")
	clock.advance(2 * time.Minute)
	r.in.recheckNames()
	if held {
		t.Error("the ingest lock was held during a contact table read")
	}
	if name, _, _ := chatNameOf(t, r.st, peer); name != "Mum" {
		t.Errorf("stored name = %q, want Mum", name)
	}
}

func TestIngestNameLateSavedNameIsOfferedAfterAHistoryFlush(t *testing.T) {
	peer := peerJID().String()
	ns := &nameStore{}
	h := historyNameRig(t, ns, historyOf(conversation(peerJID(), "", webText("HR1", "x", histTime))),
		historyOf(conversation(otherPeer(), "", webText("HR2", "y", histTime))))
	clock := newRecheckClock(h.in)
	h.deliver(t, 1)
	if _, q, _ := chatNameOf(t, h.st, peer); q != store.NameFallback {
		t.Fatalf("first quality = %v, want fallback", q)
	}
	ns.setContact(peer, "Mum", "")
	clock.advance(2 * time.Minute)
	h.deliver(t, 2)
	if name, q, _ := chatNameOf(t, h.st, peer); name != "Mum" || q != store.NameSaved {
		t.Errorf("stored name = %q (%v), want Mum at saved", name, q)
	}
}
