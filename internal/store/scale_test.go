package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// scaleShape is one size of the scale dataset. The full size is ten times the small one, except the 1,000 small
// chats, which stay the same.
type scaleShape struct {
	name       string
	group      int // messages in the big group
	big        int // of those, from the big contact, half under the phone number and half under a mapped LID
	contacts   int // contacts in the device store
	mappings   int // LID mappings
	deepOffset int // offset of the deep sender listing: the contact's messages minus one page
}

const (
	scaleChats   = 1000
	scalePerChat = 5
	scalePage    = 200

	scaleBigPhone  = "491550000000"
	scaleBigLID    = "900000009999"
	scaleRarePhone = "491570000000" // two messages in the big group
	scaleGroup     = "120363000000000777@g.us"
	scaleHits      = 100 // messages of the big group that contain the search text
)

var (
	scaleTenth = scaleShape{name: "tenth", group: 30_000, big: 12_000, contacts: 500, mappings: 1000, deepOffset: 9800}
	scaleFull  = scaleShape{name: "full", group: 300_000, big: 120_000, contacts: 5000, mappings: 10_000, deepOffset: 99_800}
)

func scaleChatJID(i int) string { return phoneJID(fmt.Sprintf("49158%07d", i)) }

func groupMessageID(n int) string { return fmt.Sprintf("g%07d", n) }

// seedScale builds a database of the given shape with raw batched inserts, which keeps seeding to seconds: the
// store's own write path is checked elsewhere, and this test only needs the rows.
func seedScale(t *testing.T, sh scaleShape) *Store {
	t.Helper()
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	ctx := context.Background()

	exec := func(tx *sql.Tx, query string, args ...any) {
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	inTx := func(fn func(tx *sql.Tx)) {
		tx, err := st.writer.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		fn(tx)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	baseMS := baseTime.UnixMilli()
	inTx(func(tx *sql.Tx) {
		exec(tx, "INSERT INTO chats (jid, phone, last_message_time) VALUES (?, '', ?)", scaleGroup, baseMS+int64(sh.group)*1000)
		for i := 0; i < scaleChats; i++ {
			jid := scaleChatJID(i)
			exec(tx, "INSERT INTO chats (jid, phone, last_message_time) VALUES (?, ?, ?)",
				jid, strings.TrimSuffix(jid, "@s.whatsapp.net"), baseMS+int64(sh.group+i)*1000)
		}
	})

	const insertMessage = `INSERT INTO messages (chat_jid, id, sender_jid, sender_phone, time, from_me, text, text_fold)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?)`
	insertBatch := func(from, to int, row func(n int) []any) {
		for lo := from; lo < to; lo += 10_000 {
			inTx(func(tx *sql.Tx) {
				stmt, err := tx.PrepareContext(ctx, insertMessage)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for n := lo; n < min(lo+10_000, to); n++ {
					if _, err := stmt.ExecContext(ctx, row(n)...); err != nil {
						t.Fatalf("seed: %v", err)
					}
				}
			})
		}
	}
	// The big group: 40% from the big contact, alternating between its two identities; the rest from 20 members;
	// scaleHits of the messages contain "needle"; two come from the rare sender.
	insertBatch(0, sh.group, func(n int) []any {
		var sender string
		switch {
		case n == 1000 || n == 2000:
			sender = phoneJID(scaleRarePhone)
		case n%5 < 2:
			sender = phoneJID(scaleBigPhone)
			if n%2 == 1 {
				sender = lidJID(scaleBigLID)
			}
		default:
			sender = phoneJID(fmt.Sprintf("49156%07d", n%20))
		}
		text := fmt.Sprintf("group message %d", n)
		if n%(sh.group/scaleHits) == 7 {
			text += " needle"
		}
		phone := ""
		if strings.HasSuffix(sender, "@s.whatsapp.net") {
			phone = strings.TrimSuffix(sender, "@s.whatsapp.net")
		}
		return []any{scaleGroup, groupMessageID(n), sender, phone, baseMS + int64(n)*1000, text, text}
	})
	insertBatch(0, scaleChats*scalePerChat, func(n int) []any {
		chat := scaleChatJID(n / scalePerChat)
		text := fmt.Sprintf("small chat message %d", n)
		return []any{chat, fmt.Sprintf("s%06d", n), chat, strings.TrimSuffix(chat, "@s.whatsapp.net"),
			baseMS + int64(sh.group+n/scalePerChat)*1000 + int64(n%scalePerChat), text, text}
	})

	inTx(func(tx *sql.Tx) {
		for i := 0; i < sh.contacts; i++ {
			exec(tx, "INSERT INTO whatsmeow_contacts (our_jid, their_jid, full_name) VALUES (?, ?, ?)",
				dev.ID.String(), phoneJID(fmt.Sprintf("49159%07d", i)), fmt.Sprintf("Contact %05d", i))
		}
		for i := 0; i < sh.mappings; i++ {
			phone := fmt.Sprintf("49159%07d", i) // the first mappings belong to contacts, the rest to numbers without one
			if i >= sh.contacts {
				phone = fmt.Sprintf("49160%07d", i)
			}
			exec(tx, "INSERT INTO whatsmeow_lid_map (lid, pn) VALUES (?, ?)", fmt.Sprintf("8%011d", i), phone)
		}
		exec(tx, "INSERT INTO whatsmeow_lid_map (lid, pn) VALUES (?, ?)", scaleBigLID, scaleBigPhone)
	})
	return st
}

// pageMeter counts the page-cache lookups (hits and misses) of the reads that run on a store while it is armed.
type pageMeter struct{ pages int }

func cachePages(conn *sql.Conn) int {
	total := 0
	err := conn.Raw(func(dc any) error {
		status, ok := dc.(sqlite.DBStatus)
		if !ok {
			return fmt.Errorf("driver connection has no DBStatus")
		}
		for _, op := range []sqlite.DBStatusOp{sqlite.DBStatusCacheHit, sqlite.DBStatusCacheMiss} {
			cur, _, err := status.Status(op, false)
			if err != nil {
				return err
			}
			total += cur
		}
		return nil
	})
	if err != nil {
		panic("store: reading the page counters: " + err.Error())
	}
	return total
}

func (m *pageMeter) arm(st *Store) {
	st.hooks.onRead = func(conn *sql.Conn) func() {
		before := cachePages(conn)
		return func() { m.pages += cachePages(conn) - before }
	}
}

type scaleKind int

const (
	constantCost scaleKind = iota // pages must not grow with the data
	linearCost                    // pages per row walked must not grow
	backstopOnly                  // linear over messages by design: a time limit only
)

type scaleCase struct {
	name string
	kind scaleKind
	rows func(sh scaleShape) int // linear cases: the rows the query must walk
	run  func(ctx context.Context, st *Store, sh scaleShape) error
}

func scaleCases() []scaleCase {
	listing := func(f func(sh scaleShape) MessageFilter, c Context, p func(sh scaleShape) Page) func(context.Context, *Store, scaleShape) error {
		return func(ctx context.Context, st *Store, sh scaleShape) error {
			_, err := st.ListMessages(ctx, f(sh), c, p(sh))
			return err
		}
	}
	filter := func(f MessageFilter) func(scaleShape) MessageFilter {
		return func(scaleShape) MessageFilter { return f }
	}
	first := func(limit int) func(scaleShape) Page { return func(scaleShape) Page { return page(limit, 0) } }
	deep := func(sh scaleShape) Page { return page(scalePage, sh.deepOffset) }
	contextAt := func(name string, at func(sh scaleShape) int) scaleCase {
		return scaleCase{name: name, kind: constantCost, run: func(ctx context.Context, st *Store, sh scaleShape) error {
			_, err := st.MessageContext(ctx, scaleGroup, groupMessageID(at(sh)), Context{Before: 10, After: 10})
			return err
		}}
	}
	contactRows := func(sh scaleShape) int { return sh.contacts + sh.mappings }
	return []scaleCase{
		// Constant cost: lookups, and every listing at offset 0.
		{name: "GetChat", kind: constantCost, run: func(ctx context.Context, st *Store, _ scaleShape) error {
			_, err := st.GetChat(ctx, scaleGroup)
			return err
		}},
		{name: "DirectChat", kind: constantCost, run: func(ctx context.Context, st *Store, _ scaleShape) error {
			_, err := st.DirectChat(ctx, scaleChatJID(5))
			return err
		}},
		{name: "LastInteraction by phone", kind: constantCost, run: func(ctx context.Context, st *Store, _ scaleShape) error {
			_, err := st.LastInteraction(ctx, scaleBigPhone)
			return err
		}},
		{name: "LastInteraction by LID", kind: constantCost, run: func(ctx context.Context, st *Store, _ scaleShape) error {
			_, err := st.LastInteraction(ctx, lidJID(scaleBigLID))
			return err
		}},
		contextAt("MessageContext oldest", func(scaleShape) int { return 0 }),
		contextAt("MessageContext middle", func(sh scaleShape) int { return sh.group / 2 }),
		contextAt("MessageContext newest", func(sh scaleShape) int { return sh.group - 1 }),
		{name: "ListChats by activity", kind: constantCost, run: func(ctx context.Context, st *Store, _ scaleShape) error {
			_, err := st.ListChats(ctx, "", ByLastActivity, true, page(50, 0))
			return err
		}},
		{name: "ListChats by name", kind: constantCost, run: func(ctx context.Context, st *Store, _ scaleShape) error {
			_, err := st.ListChats(ctx, "", ByName, true, page(50, 0))
			return err
		}},
		{name: "ListMessages unfiltered", kind: constantCost, run: listing(filter(MessageFilter{}), Context{}, first(50))},
		{name: "ListMessages big group", kind: constantCost,
			run: listing(filter(MessageFilter{ChatJID: scaleGroup}), Context{}, first(50))},
		{name: "ListMessages big contact", kind: constantCost,
			run: listing(filter(MessageFilter{Sender: scaleBigPhone}), Context{}, first(50))},
		{name: "ListMessages big group, rare sender", kind: constantCost,
			run: listing(filter(MessageFilter{ChatJID: scaleGroup, Sender: scaleRarePhone}), Context{}, first(10))},
		{name: "ListMessages big contact, group and sender", kind: constantCost,
			run: listing(filter(MessageFilter{ChatJID: scaleGroup, Sender: scaleBigPhone}), Context{}, first(50))},
		{name: "GetMessage", kind: constantCost, run: func(ctx context.Context, st *Store, sh scaleShape) error {
			_, err := st.GetMessage(ctx, scaleGroup, groupMessageID(sh.group/2))
			return err
		}},
		{name: "GetMessageByID", kind: constantCost, run: func(ctx context.Context, st *Store, sh scaleShape) error {
			_, err := st.GetMessageByID(ctx, groupMessageID(sh.group/3))
			return err
		}},

		// Linear by design, normalised by the rows that must be walked.
		{name: "SearchContacts by name", kind: linearCost, rows: contactRows, run: func(ctx context.Context, st *Store, _ scaleShape) error {
			_, err := st.SearchContacts(ctx, "contact 00042", page(50, 0))
			return err
		}},
		{name: "SearchContacts by number", kind: linearCost, rows: contactRows, run: func(ctx context.Context, st *Store, _ scaleShape) error {
			_, err := st.SearchContacts(ctx, "+49 159 0000042", page(50, 0))
			return err
		}},
		{name: "ContactChats", kind: linearCost, rows: func(scaleShape) int { return scaleChats + 1 },
			run: func(ctx context.Context, st *Store, _ scaleShape) error {
				_, err := st.ContactChats(ctx, scaleBigPhone, page(50, 0))
				return err
			}},
		{name: "ListMessages big contact, deep", kind: linearCost, rows: func(sh scaleShape) int { return sh.deepOffset + scalePage },
			run: listing(filter(MessageFilter{Sender: scaleBigPhone}), Context{}, deep)},

		// Linear over messages by design: a backstop only.
		{name: "ListMessages text search", kind: backstopOnly,
			run: listing(filter(MessageFilter{Query: "needle"}), Context{Before: 10, After: 10}, first(scaleHits))},
		{name: "ListMessages search in the big group", kind: backstopOnly,
			run: listing(filter(MessageFilter{ChatJID: scaleGroup, Query: "needle"}), Context{Before: 10, After: 10}, first(scaleHits))},
		{name: "ListMessages unfiltered, deep", kind: backstopOnly,
			run: listing(filter(MessageFilter{}), Context{}, func(sh scaleShape) Page { return page(scalePage, min(sh.deepOffset, maxOffset-scalePage)) })},
		{name: "ListMessages big contact, deep with search", kind: backstopOnly,
			run: listing(filter(MessageFilter{Sender: scaleBigPhone, Query: "message"}), Context{}, deep)},
	}
}

type scaleResult struct {
	pages  int
	median time.Duration
}

// measureScale runs every case on a store of the given shape: once to warm the cache, once to count pages, then
// five times for the median time.
func measureScale(t *testing.T, sh scaleShape) map[string]scaleResult {
	t.Helper()
	st := seedScale(t, sh)
	var meter pageMeter
	meter.arm(st)
	ctx := context.Background()
	out := map[string]scaleResult{}
	for _, c := range scaleCases() {
		if err := c.run(ctx, st, sh); err != nil {
			t.Fatalf("%s at %s: %v", c.name, sh.name, err)
		}
		meter.pages = 0
		if err := c.run(ctx, st, sh); err != nil {
			t.Fatalf("%s at %s: %v", c.name, sh.name, err)
		}
		pages := meter.pages
		times := make([]time.Duration, 5)
		for i := range times {
			start := time.Now()
			if err := c.run(ctx, st, sh); err != nil {
				t.Fatalf("%s at %s: %v", c.name, sh.name, err)
			}
			times[i] = time.Since(start)
		}
		slices.Sort(times)
		out[c.name] = scaleResult{pages: pages, median: times[len(times)/2]}
	}
	return out
}

// TestQueryScale checks that no query reads more than its design allows as the data grows tenfold. It counts
// pages touched, not time, so it holds on any runner: a lookup must cost about the same on 30,000 messages as on
// 300,000, and a listing that must walk rows must cost about the same per row. A plan that reads a chat or a
// contact's whole history instead of seeking grows about tenfold and fails.
func TestQueryScale(t *testing.T) {
	small := measureScale(t, scaleTenth)
	full := measureScale(t, scaleFull)
	for _, c := range scaleCases() {
		s, f := small[c.name], full[c.name]
		switch c.kind {
		case constantCost:
			t.Logf("%-46s constant: %5d pages at a tenth, %5d at full, %v", c.name, s.pages, f.pages, f.median)
			if limit := 2*s.pages + 16; f.pages > limit {
				t.Errorf("%s: %d pages at full size, more than 2 x %d + 16: the cost grows with the data", c.name, f.pages, s.pages)
			}
		case linearCost:
			rs, rf := float64(c.rows(scaleTenth)), float64(c.rows(scaleFull))
			ps, pf := float64(s.pages)/rs, float64(f.pages)/rf
			t.Logf("%-46s linear: %5d pages / %6.0f rows at a tenth, %6d / %7.0f at full (%.4f vs %.4f per row), %v",
				c.name, s.pages, rs, f.pages, rf, ps, pf, f.median)
			if pf > 1.5*ps+0.01 {
				t.Errorf("%s: %.4f pages per row at full size, more than 1.5 x %.4f + 0.01: it walks more than it must", c.name, pf, ps)
			}
		case backstopOnly:
			t.Logf("%-46s backstop: %5d pages at a tenth, %5d at full, %v", c.name, s.pages, f.pages, f.median)
			if f.median > 5*time.Second {
				t.Errorf("%s: median %v at full size, over the 5 s backstop", c.name, f.median)
			}
		}
	}
}
