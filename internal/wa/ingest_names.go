package wa

import (
	"container/list"
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

const (
	// nameCacheSize is how many chats' stored names one generation remembers, least recently used first out.
	nameCacheSize = 4096
	// groupLookupSetSize is how many distinct groups wait for, or are in, a name lookup; one more is dropped and
	// counted.
	groupLookupSetSize = 64
	// groupLookupTimeout bounds one group lookup.
	groupLookupTimeout = 10 * time.Second
	// failureTTL is how long a failed lookup keeps the group from being asked again, and failureCacheSize how
	// many such groups are remembered; the oldest goes first.
	failureTTL       = 10 * time.Minute
	failureCacheSize = 1024
	// recheckPerCall is how many due chats one message or one history flush re-reads from the contact table.
	recheckPerCall = 8
)

// recheckDelays are the waits before the first, second and third re-read of a direct chat's contact names, counted
// from the previous read. A chat still below the saved name after the third is not read again in this generation.
var recheckDelays = [...]time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute}

// nameEntry is what ingest knows of one chat's stored name. contactChecked, saved and real hold the device
// contact table's names, read once per chat per generation, and readAt when they were read.
type nameEntry struct {
	jid            string
	name           string
	quality        store.NameQuality
	at             time.Time
	contactChecked bool
	saved, real    string
	readAt         time.Time
}

// nameState is one generation's chat-name memory: the cache, the failed group lookups and the lookup set.
type nameState struct {
	mu    sync.Mutex
	cache map[string]*list.Element // jid -> element holding *nameEntry
	lru   *list.List               // front = most recently used

	failures *failureCache
	// pending holds the groups queued for or in a lookup; queue is their order. Both hold at most
	// groupLookupSetSize groups.
	pending    map[string]struct{}
	queue      chan string
	started    bool
	workers    int
	pendingMax int

	// recheck holds, per attempt, the chats whose contact names are due for a re-read, oldest due first (a delay is
	// the same for every chat of an attempt); recheckSet holds each queued chat once. Together they hold at most
	// nameCacheSize chats.
	recheck    [len(recheckDelays)][]recheckItem
	recheckSet map[string]struct{}
}

// recheckItem is one chat waiting for its attempt'th re-read of the contact table.
type recheckItem struct {
	jid     string
	attempt int
	due     time.Time
}

func (n *nameState) init() {
	if n.cache == nil {
		n.cache = map[string]*list.Element{}
		n.lru = list.New()
		n.failures = newFailureCache()
		n.pending = map[string]struct{}{}
		n.queue = make(chan string, groupLookupSetSize)
		n.recheckSet = map[string]struct{}{}
	}
}

// get returns the cached entry and marks it recently used.
func (n *nameState) get(jid string) (nameEntry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.init()
	if el, ok := n.cache[jid]; ok {
		n.lru.MoveToFront(el)
		return *el.Value.(*nameEntry), true
	}
	return nameEntry{}, false
}

// put stores e, forgetting the least recently used chat beyond the limit.
func (n *nameState) put(e nameEntry) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.init()
	if el, ok := n.cache[e.jid]; ok {
		*el.Value.(*nameEntry) = e
		n.lru.MoveToFront(el)
		return
	}
	n.cache[e.jid] = n.lru.PushFront(&e)
	for n.lru.Len() > nameCacheSize {
		old := n.lru.Back()
		n.lru.Remove(old)
		delete(n.cache, old.Value.(*nameEntry).jid)
	}
}

// raise records a name that was written, when the chat is cached and the name is better than the cached one.
func (n *nameState) raise(jid, name string, q store.NameQuality, at time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.init()
	if el, ok := n.cache[jid]; ok {
		if e := el.Value.(*nameEntry); better(*e, name, q, at) {
			e.name, e.quality, e.at = name, q, at
		}
	}
}

// better reports whether offering name at quality q and time at can change what the store keeps: a higher quality,
// or the same quality and newer, or the same time and the greater name. The same name at the same quality is
// never better, whatever its time.
func better(e nameEntry, name string, q store.NameQuality, at time.Time) bool {
	switch {
	case name == "" || q <= store.NameNone:
		return false
	case e.quality == store.NameNone || e.name == "":
		return true
	case name == e.name && q == e.quality:
		return false
	case q != e.quality:
		return q > e.quality
	case !at.Equal(e.at):
		return at.After(e.at)
	}
	return name > e.name
}

// failureCache remembers the groups whose lookup failed. At most failureCacheSize are kept; the one added
// longest ago goes first.
type failureCache struct {
	at    map[string]time.Time
	order *list.List // front = oldest
	elems map[string]*list.Element
}

func newFailureCache() *failureCache {
	return &failureCache{at: map[string]time.Time{}, order: list.New(), elems: map[string]*list.Element{}}
}

func (c *failureCache) add(key string, at time.Time) {
	if el, ok := c.elems[key]; ok {
		c.order.MoveToBack(el)
	} else {
		c.elems[key] = c.order.PushBack(key)
	}
	c.at[key] = at
	for c.order.Len() > failureCacheSize {
		front := c.order.Front()
		c.order.Remove(front)
		k := front.Value.(string)
		delete(c.elems, k)
		delete(c.at, k)
	}
}

// recent reports a failure within failureTTL of now.
func (c *failureCache) recent(key string, now time.Time) bool {
	at, ok := c.at[key]
	return ok && now.Sub(at) < failureTTL
}

func (c *failureCache) len() int { return c.order.Len() }

// msTime drops what the store does not keep, so a cached time compares as the stored one does.
func msTime(t time.Time) time.Time { return time.UnixMilli(t.UnixMilli()).UTC() }

func isGroupChat(jid string) bool { return strings.HasSuffix(jid, "@"+types.GroupServer) }

// userPart is the part of a JID before the "@".
func userPart(jid string) string {
	user, _, _ := strings.Cut(jid, "@")
	return user
}

// namePlan is what ingest decides to do about one chat's name: offer a name, ask for a group's subject, or neither.
type namePlan struct {
	update store.ChatUpdate
	offer  bool
	lookup bool
}

// offerName is step 6 of HandleMessage: it offers the stored message's chat a name when that is better than the
// one known, and asks for a group's subject when the group has no name. It reports false when a store error made
// ingest terminal.
func (in *ingest) offerName(evt *events.Message, m store.Message) bool {
	push := ""
	if !m.FromMe {
		// An own message carries the owner's push name, never the other person's.
		push = evt.Info.PushName
	}
	plan, ok := in.planName(m.ChatJID, push, m.Time)
	if !ok {
		return false
	}
	if plan.offer && !in.writeName(plan.update.JID, plan.update.Name, plan.update.NameQuality, plan.update.NameTime) {
		return false
	}
	if plan.lookup {
		in.requestGroupLookup(m.ChatJID)
	}
	return in.recheckNames()
}

// offerHistoryNames gives every chat of a committed history batch the same naming as a live message: the stored
// name is read once per chat, the best better name is offered, and a group without a name is asked for. The offers
// of a batch go to the store in one write. at is the time of the chat's first message in the batch. It reports false
// when a store error made ingest terminal.
func (in *ingest) offerHistoryNames(chats map[string]time.Time) bool {
	var updates []store.ChatUpdate
	var lookups []string
	for chat, at := range chats {
		plan, ok := in.planName(chat, "", at)
		if !ok {
			return false
		}
		if plan.offer {
			updates = append(updates, plan.update)
		}
		if plan.lookup {
			lookups = append(lookups, chat)
		}
	}
	if len(updates) > 0 {
		if err := in.st.UpdateChats(in.cfg.Lib, updates); err != nil {
			in.fail("name", err, "chats", len(updates))
			return false
		}
		for _, u := range updates {
			in.nm.raise(u.JID, u.Name, u.NameQuality, u.NameTime)
		}
	}
	for _, chat := range lookups {
		in.requestGroupLookup(chat)
	}
	return in.recheckNames()
}

// planName decides the name offer for a chat: it reads the stored name and, for a direct chat, the contact table,
// once per chat per generation, and compares the best candidate with what is known. push is the sender's own name
// from the message ("" when there is none or it is not the other person's). It reports false when a store error
// made ingest terminal.
func (in *ingest) planName(chat, push string, msgTime time.Time) (namePlan, bool) {
	ent, ok := in.nm.get(chat)
	if !ok {
		name, q, at, found, err := in.st.ChatName(in.cfg.Lib, chat)
		if err != nil {
			in.fail("name", err, "chat", escapeLog(chat))
			return namePlan{}, false
		}
		ent = nameEntry{jid: chat}
		if found {
			ent.name, ent.quality, ent.at = name, q, msTime(at)
		}
		in.nm.put(ent)
	}
	if isGroupChat(chat) {
		return in.planGroupName(ent, msgTime), true
	}
	return in.planDirectName(ent, push, msgTime)
}

// planDirectName picks a direct chat's best name: the saved contact name, the real contact name, the message's push
// name, then the JID's user part.
func (in *ingest) planDirectName(ent nameEntry, push string, msgTime time.Time) (namePlan, bool) {
	if !ent.contactChecked {
		saved, real, ok := in.readContactNames(ent.jid)
		if !ok {
			return namePlan{}, false
		}
		ent.contactChecked, ent.saved, ent.real, ent.readAt = true, saved, real, msTime(in.now())
		in.nm.put(ent)
		if saved == "" {
			// The device's contact sync may store the saved name later.
			in.queueRecheck(ent.jid, 0, ent.readAt.Add(recheckDelays[0]))
		}
	}
	var name string
	var q store.NameQuality
	var at time.Time
	switch push = usableName(push); {
	case ent.saved != "":
		name, q, at = ent.saved, store.NameSaved, ent.readAt
	case ent.real != "":
		name, q, at = ent.real, store.NameReal, ent.readAt
	case push != "":
		name, q, at = push, store.NameReal, msTime(msgTime)
	default:
		name, q, at = userPart(ent.jid), store.NameFallback, msTime(msgTime)
	}
	if !better(ent, name, q, at) {
		return namePlan{}, true
	}
	return namePlan{update: store.ChatUpdate{JID: ent.jid, Name: name, NameQuality: q, NameTime: at, ExistingOnly: true}, offer: true}, true
}

// planGroupName gives a group without a name the fallback "Group <id>" and asks for its subject; a group that has a
// real name, from history or an earlier lookup, is left alone.
func (in *ingest) planGroupName(ent nameEntry, msgTime time.Time) namePlan {
	if ent.quality >= store.NameReal {
		return namePlan{}
	}
	plan := namePlan{lookup: true}
	if ent.quality == store.NameNone {
		plan.offer = true
		plan.update = store.ChatUpdate{JID: ent.jid, Name: "Group " + userPart(ent.jid), NameQuality: store.NameFallback, NameTime: msTime(msgTime), ExistingOnly: true}
	}
	return plan
}

// writeName writes one name offer and updates the cache; a failure is terminal.
func (in *ingest) writeName(jid, name string, q store.NameQuality, at time.Time) bool {
	err := in.st.UpdateChats(in.cfg.Lib, []store.ChatUpdate{{JID: jid, Name: name, NameQuality: q, NameTime: at, ExistingOnly: true}})
	if err != nil {
		in.fail("name", err, "chat", escapeLog(jid))
		return false
	}
	in.nm.raise(jid, name, q, at)
	return true
}

// usableName is name when the store would accept it, else "".
func usableName(name string) string {
	if name == "" || len(name) > maxChatNameBytes || !utf8.ValidString(name) {
		return ""
	}
	return name
}

// requestGroupLookup queues a group for a subject lookup unless it is already queued, failed recently, or the set
// is full, in which case the request is dropped and counted.
func (in *ingest) requestGroupLookup(jid string) {
	n := &in.nm
	n.mu.Lock()
	n.init()
	if _, ok := n.pending[jid]; ok || n.failures.recent(jid, in.now()) {
		n.mu.Unlock()
		return
	}
	if len(n.pending) >= groupLookupSetSize {
		n.mu.Unlock()
		in.lookupsDropped.Add(1)
		return
	}
	n.pending[jid] = struct{}{}
	n.pendingMax = max(n.pendingMax, len(n.pending))
	n.queue <- jid // never blocks: the queue holds at most the set's size
	start := !n.started
	n.started = true
	n.mu.Unlock()
	if start {
		if in.goWorker(in.groupNameWorker) {
			n.mu.Lock()
			n.workers++
			n.mu.Unlock()
		}
	}
}

// groupNameWorker looks up one group at a time until ingest stops. It is the generation's only group-name
// goroutine.
func (in *ingest) groupNameWorker() {
	for {
		select {
		case <-in.stop:
			return
		case jid := <-in.nm.queue:
			in.lookupGroup(jid)
			in.nm.mu.Lock()
			delete(in.nm.pending, jid)
			in.nm.mu.Unlock()
		}
	}
}

// lookupGroup asks for a group's subject under the library context and a deadline, and writes it as a real name.
// A failed or empty answer is remembered for failureTTL; a failed write is terminal.
func (in *ingest) lookupGroup(jid string) {
	if in.stopping() {
		return
	}
	parsed, err := types.ParseJID(jid)
	if err == nil {
		ctx, cancel := context.WithTimeout(in.cfg.Lib, groupLookupTimeout)
		var info *types.GroupInfo
		info, err = in.cfg.Client.GetGroupInfo(ctx, parsed)
		cancel()
		if err == nil {
			name := ""
			if info != nil {
				name = usableName(info.Name)
			}
			if name != "" {
				at := msTime(in.now())
				in.writeName(jid, name, store.NameReal, at)
				return
			}
		}
	}
	if in.stopping() {
		return
	}
	in.nm.mu.Lock()
	in.nm.init()
	in.nm.failures.add(jid, in.now())
	in.nm.mu.Unlock()
}

// readContactNames reads a chat's saved and real contact names, "" for one the store will not take. It reports false
// when a store error made ingest terminal.
func (in *ingest) readContactNames(jid string) (saved, real string, ok bool) {
	saved, real, err := in.st.ContactNames(in.cfg.Lib, jid)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrResourceLimit), errors.Is(err, store.ErrInvalid):
		// A contact name the store will not take is no name.
		return "", "", true
	default:
		in.fail("name", err, "chat", escapeLog(jid))
		return "", "", false
	}
	return usableName(saved), usableName(real), true
}

func (n *nameState) recheckLen() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.recheckSet)
}

// queueRecheck queues a direct chat for its attempt'th contact table re-read at due. A chat already queued, an
// attempt beyond the last and a full queue are dropped.
func (in *ingest) queueRecheck(jid string, attempt int, due time.Time) {
	n := &in.nm
	n.mu.Lock()
	defer n.mu.Unlock()
	n.init()
	if attempt < 0 || attempt >= len(recheckDelays) || len(n.recheckSet) >= nameCacheSize {
		return
	}
	if _, ok := n.recheckSet[jid]; ok {
		return
	}
	n.recheckSet[jid] = struct{}{}
	n.recheck[attempt] = append(n.recheck[attempt], recheckItem{jid: jid, attempt: attempt, due: due})
}

// takeDue removes and returns up to limit queued chats that are due at now.
func (n *nameState) takeDue(now time.Time, limit int) []recheckItem {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.init()
	var out []recheckItem
	for a := range n.recheck {
		q := n.recheck[a]
		for len(q) > 0 && len(out) < limit && !q[0].due.After(now) {
			out = append(out, q[0])
			delete(n.recheckSet, q[0].jid)
			q = q[1:]
		}
		if len(q) == 0 {
			q = nil // release the backing array
		}
		n.recheck[a] = q
	}
	return out
}

// recheckNames re-reads the contact table for up to recheckPerCall due chats whose name is below the saved quality
// and offers a better name through the normal name write. It runs after a live message or a history flush, holds no
// lock during the reads and starts no goroutine. It reports false when a store error made ingest terminal.
func (in *ingest) recheckNames() bool {
	for _, it := range in.nm.takeDue(in.now(), recheckPerCall) {
		if !in.recheckOne(it) {
			return false
		}
	}
	return true
}

func (in *ingest) recheckOne(it recheckItem) bool {
	ent, ok := in.nm.get(it.jid)
	if !ok || ent.quality >= store.NameSaved {
		return true // forgotten, or already as good as a contact can make it
	}
	saved, real, ok := in.readContactNames(it.jid)
	if !ok {
		return false
	}
	ent.contactChecked, ent.saved, ent.real, ent.readAt = true, saved, real, msTime(in.now())
	in.nm.put(ent)
	name, q := saved, store.NameSaved
	if saved == "" {
		name, q = real, store.NameReal
	}
	if better(ent, name, q, ent.readAt) && !in.writeName(ent.jid, name, q, ent.readAt) {
		return false
	}
	if saved == "" {
		in.queueRecheck(it.jid, it.attempt+1, ent.readAt.Add(recheckDelays[min(it.attempt+1, len(recheckDelays)-1)]))
	}
	return true
}
