package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

// The seeded database of the benchmark and the plan test: 100,000 messages across 1,000 chats (900 direct, 100
// groups), 1,000 contacts and LID mappings in the device store, and one "big" contact who wrote in a group under
// both identities.
const (
	seedMessages     = 100_000
	seedChats        = 1000
	seedGroups       = 100
	seedContactCount = 1000

	bigPhone    = "491540000000"
	bigLID      = "700000009999"
	bigGroup    = "120363000000000900@g.us"
	bigPerSide  = 3000
	seedMediaID = "media-1" // an image in the big group, for MediaInfo
)

var (
	seedOnce sync.Once
	seedPath string // the temporary root, removed when the tests end
	seedData string // the data directory inside it
	seedErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if seedPath != "" {
		os.RemoveAll(seedPath)
	}
	os.Exit(code)
}

func seedChatJID(i int) string {
	if i >= seedChats-seedGroups {
		return fmt.Sprintf("1203630000%08d@g.us", i)
	}
	return phoneJID(fmt.Sprintf("49152%07d", i))
}

// seededDir returns a data directory holding the seeded database, built once per process. Callers open it with
// the options they need and must not write to it.
func seededDir(tb testing.TB) string {
	tb.Helper()
	seedOnce.Do(func() {
		seedPath, seedData, seedErr = buildSeed()
	})
	if seedErr != nil {
		tb.Fatalf("seeding: %v", seedErr)
	}
	return seedData
}

func buildSeed() (root, dir string, err error) {
	root, err = os.MkdirTemp("", "mcp-seed-")
	if err != nil {
		return "", "", err
	}
	dir = root + string(os.PathSeparator) + "data"
	if err := private.MkdirAll(dir); err != nil {
		return root, dir, err
	}
	logger, _ := newTestLogger()
	st, err := open(context.Background(), dir, logger, openOptions{})
	if err != nil {
		return root, dir, err
	}
	defer st.Close(context.Background())

	tb := &seedTB{}
	dev := seedDevice(tb, st)
	contacts := make([]contactSeed, 0, seedContactCount)
	for i := 0; i < seedContactCount; i++ {
		contacts = append(contacts, contactSeed{jid: phoneJID(fmt.Sprintf("49153%07d", i)), full: fmt.Sprintf("Contact %04d", i)})
	}
	seedContacts(tb, dev, contacts...)
	for i := 0; i < seedContactCount; i++ {
		seedLIDMapping(tb, dev, fmt.Sprintf("7%011d", i), fmt.Sprintf("49153%07d", i))
	}
	seedLIDMapping(tb, dev, bigLID, bigPhone)
	seedContacts(tb, dev, contactSeed{jid: phoneJID(bigPhone), full: "Big Contact"})
	if tb.failed != "" {
		return root, dir, fmt.Errorf("%s", tb.failed)
	}

	batch := make([]Message, 0, maxBatchMessages)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := st.StoreMessages(context.Background(), batch)
		batch = batch[:0]
		return err
	}
	add := func(m Message) error {
		if batch = append(batch, m); len(batch) == maxBatchMessages {
			return flush()
		}
		return nil
	}
	for n := 0; n < seedMessages; n++ {
		chat := seedChatJID((n * 7919) % seedChats)
		sender, mine := chat, n%3 == 0
		if strings.HasSuffix(chat, "@g.us") {
			sender = phoneJID(fmt.Sprintf("49153%07d", n%20))
		}
		if mine {
			sender = ownJID
		}
		text := fmt.Sprintf("message %d about topic %d", n, n%97)
		if n%1000 == 0 {
			text += " needle"
		}
		m := Message{ChatJID: chat, ID: fmt.Sprintf("m%06d", n), SenderJID: sender, FromMe: mine, Text: text,
			Time: baseTime.Add(time.Duration(n) * 2 * time.Second)}
		if err := add(m); err != nil {
			return root, dir, err
		}
	}
	for n := 0; n < bigPerSide; n++ {
		for side, sender := range []string{phoneJID(bigPhone), lidJID(bigLID)} {
			m := Message{ChatJID: bigGroup, ID: fmt.Sprintf("big%d-%05d", side, n), SenderJID: sender,
				Text: fmt.Sprintf("big contact message %d", n), Time: baseTime.Add(time.Duration(n)*3*time.Second + time.Duration(side)*time.Second)}
			if err := add(m); err != nil {
				return root, dir, err
			}
		}
	}
	length := int64(2048)
	if err := add(Message{ChatJID: bigGroup, ID: seedMediaID, SenderJID: phoneJID(bigPhone), Caption: "a photo", MediaType: "image",
		Time: baseTime.Add(time.Hour), Media: MediaMeta{URL: "https://mmg.example/f", DirectPath: "/v/f", MediaKey: make([]byte, 32),
			FileSHA256: make([]byte, 32), FileEncSHA256: make([]byte, 32), FileLength: &length, MIMEType: "image/jpeg"}}); err != nil {
		return root, dir, err
	}
	return root, dir, flush()
}

// seedTB is a testing.TB for setup outside a test: it records the first failure instead of stopping.
type seedTB struct {
	testing.TB
	failed string
}

func (s *seedTB) Helper() {}
func (s *seedTB) Fatal(args ...any) {
	if s.failed == "" {
		s.failed = fmt.Sprint(args...)
	}
}
func (s *seedTB) Fatalf(format string, args ...any) {
	if s.failed == "" {
		s.failed = fmt.Sprintf(format, args...)
	}
}

// recordedQuery is a statement a read operation ran, with its arguments.
type recordedQuery struct {
	sql  string
	args []any
}

// recorder wraps a querier and keeps every statement run through it.
type recorder struct {
	q       querier
	mu      sync.Mutex
	queries []recordedQuery
}

func (r *recorder) record(query string, args []any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, recordedQuery{query, args})
}

func (r *recorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.record(query, args)
	return r.q.QueryContext(ctx, query, args...)
}

func (r *recorder) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.record(query, args)
	return r.q.QueryRowContext(ctx, query, args...)
}

// openSeeded opens the seeded database. The store is read-only for the caller's purposes.
func openSeeded(tb testing.TB, rec *recorder) *Store {
	tb.Helper()
	opts := openOptions{}
	if rec != nil {
		opts.hooks.wrapReader = func(q querier) querier {
			rec.q = q
			return rec
		}
	}
	st, _ := openTestWith(tb, seededDir(tb), opts)
	return st
}

// planOf returns the lines of EXPLAIN QUERY PLAN for a recorded statement.
func planOf(tb testing.TB, st *Store, q recordedQuery) []string {
	tb.Helper()
	rows, err := st.reader.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q.sql, q.args...)
	if err != nil {
		tb.Fatalf("EXPLAIN %s: %v", q.sql, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			tb.Fatal(err)
		}
		out = append(out, detail)
	}
	return out
}

// planProblems returns what is wrong with the plan lines of one statement, by the rules every query shares: no
// MULTI-INDEX OR, no scan of the LID map, a scan of messages only on messages_time, a scan of chats only on one of
// the two listing indexes, and a temporary b-tree only where allowTemp says so.
func planProblems(lines []string, allowTemp bool) []string {
	var out []string
	for _, l := range lines {
		switch {
		case strings.Contains(l, "MULTI-INDEX OR"):
			out = append(out, "MULTI-INDEX OR: "+l)
		case strings.HasPrefix(l, "SCAN m") && !strings.HasPrefix(l, "SCAN messages"):
			out = append(out, "scan of the LID map or a join: "+l)
		case strings.HasPrefix(l, "SCAN whatsmeow_lid_map"), strings.HasPrefix(l, "SCAN whatsmeow_contacts"):
			out = append(out, "scan of a device table: "+l)
		case strings.HasPrefix(l, "SCAN messages") && !strings.Contains(l, "INDEX messages_time"):
			out = append(out, "scan of messages not on messages_time: "+l)
		case strings.HasPrefix(l, "SCAN chats") &&
			!strings.Contains(l, "INDEX chats_last_activity") && !strings.Contains(l, "INDEX chats_name"):
			out = append(out, "scan of chats not on a listing index: "+l)
		case strings.Contains(l, "TEMP B-TREE") && !allowTemp:
			out = append(out, "temporary b-tree: "+l)
		}
	}
	return out
}

func TestPlanProblemsRejectsBadPlans(t *testing.T) {
	for name, line := range map[string]string{
		"or":         "MULTI-INDEX OR",
		"lid map":    "SCAN m",
		"messages":   "SCAN messages USING INDEX messages_chat_time",
		"chats":      "SCAN chats",
		"temp":       "USE TEMP B-TREE FOR ORDER BY",
		"no index":   "SCAN messages",
		"device map": "SCAN whatsmeow_lid_map",
	} {
		if len(planProblems([]string{line}, false)) == 0 {
			t.Errorf("%s: %q was accepted", name, line)
		}
	}
	for _, line := range []string{
		"SCAN messages USING COVERING INDEX messages_time", "SCAN messages USING INDEX messages_time",
		"SCAN chats USING COVERING INDEX chats_last_activity", "SCAN chats USING INDEX chats_name",
		"SEARCH messages USING COVERING INDEX messages_sender_time (sender_phone=?)", "SCAN whatsmeow_device",
	} {
		if p := planProblems([]string{line}, false); len(p) != 0 {
			t.Errorf("%q was rejected: %v", line, p)
		}
	}
}

// planCase is one operation of TestQueryPlans: what it runs, what its plans must contain, and what they must not.
type planCase struct {
	name      string
	run       func(ctx context.Context, st *Store) error
	want      []string
	notWant   []string
	allowTemp bool
}

// TestQueryPlans holds the expected plan of every chat, contact and message query on the seeded database: a search on a named index for
// every lookup, a scan only where the data model lists one, and the merge of the two sender indexes for a contact
// under both identities. A query that stops using its index fails here, in CI, where the benchmark does not run.
func TestQueryPlans(t *testing.T) {
	rec := &recorder{}
	st := openSeeded(t, rec)
	ctx := context.Background()
	const (
		phoneSender = "SEARCH messages USING COVERING INDEX messages_sender_time (sender_phone=?"
		lidSender   = "SEARCH messages USING COVERING INDEX messages_sender_jid (sender_jid=?"
		beforeCtx   = "messages_chat_time (chat_jid=? AND (time,id)<(?,?))"
		afterCtx    = "messages_chat_time (chat_jid=? AND (time,id)>(?,?))"
		chatByKey   = "SEARCH chats USING INDEX sqlite_autoindex_chats_1 (jid=?)"
		latest      = "messages_chat_time (chat_jid=?)"
	)
	unmapped := phoneJID("491520000005")
	list := func(f MessageFilter, c Context, p Page) func(context.Context, *Store) error {
		return func(ctx context.Context, st *Store) error { _, err := st.ListMessages(ctx, f, c, p); return err }
	}
	for _, tc := range []planCase{
		{name: "ListChats activity", want: []string{"SCAN chats USING INDEX chats_last_activity", latest},
			run: func(ctx context.Context, st *Store) error {
				_, err := st.ListChats(ctx, "4915200", ByLastActivity, true, page(10, 0))
				return err
			}},
		{name: "ListChats name", want: []string{"SCAN chats USING INDEX chats_name"},
			run: func(ctx context.Context, st *Store) error {
				_, err := st.ListChats(ctx, "", ByName, false, page(10, 0))
				return err
			}},
		{name: "GetChat", want: []string{chatByKey, latest},
			run: func(ctx context.Context, st *Store) error { _, err := st.GetChat(ctx, unmapped); return err }},
		{name: "DirectChat", want: []string{chatByKey, "whatsmeow_lid_map_2 (pn=?)"},
			run: func(ctx context.Context, st *Store) error {
				_, err := st.DirectChat(ctx, "+49 152 0000005")
				return err
			}},
		{name: "ContactChats by phone",
			want: []string{"SCAN chats USING COVERING INDEX chats_last_activity",
				"SEARCH messages USING COVERING INDEX messages_chat_sender_phone (chat_jid=? AND sender_phone=?)",
				"SEARCH messages USING COVERING INDEX messages_chat_sender_jid (chat_jid=? AND sender_jid=?)"},
			run: func(ctx context.Context, st *Store) error {
				_, err := st.ContactChats(ctx, bigPhone, page(10, 0))
				return err
			}},
		{name: "ContactChats by LID", want: []string{"SCAN chats USING COVERING INDEX chats_last_activity", "whatsmeow_lid_map_1 (lid=?)"},
			run: func(ctx context.Context, st *Store) error {
				_, err := st.ContactChats(ctx, lidJID(bigLID), page(10, 0))
				return err
			}},
		{name: "SearchContacts by name", allowTemp: true,
			want: []string{"USE TEMP B-TREE FOR GROUP BY", "USE TEMP B-TREE FOR ORDER BY", "whatsmeow_lid_map_1 (lid=?)"},
			run: func(ctx context.Context, st *Store) error {
				_, err := st.SearchContacts(ctx, "contact 0001", page(10, 0))
				return err
			}},
		{name: "SearchContacts by number", allowTemp: true,
			want: []string{"USE TEMP B-TREE FOR GROUP BY", "whatsmeow_lid_map_1 (lid=?)"},
			run: func(ctx context.Context, st *Store) error {
				_, err := st.SearchContacts(ctx, "+49 153 0000001", page(10, 0))
				return err
			}},
		{name: "ListMessages unfiltered", want: []string{"SCAN messages USING COVERING INDEX messages_time"},
			run: list(MessageFilter{}, Context{}, page(10, 0))},
		{name: "ListMessages chat", want: []string{"SEARCH messages USING COVERING INDEX " + latest},
			run: list(MessageFilter{ChatJID: bigGroup}, Context{}, page(10, 0))},
		{name: "ListMessages after and before", want: []string{"SEARCH messages USING COVERING INDEX messages_time (time>? AND time<?)"},
			run: list(MessageFilter{After: at(1), Before: at(500)}, Context{}, page(10, 0))},
		{name: "ListMessages sender, both identities", want: []string{"MERGE (UNION ALL)", phoneSender, lidSender},
			run: list(MessageFilter{Sender: bigPhone}, Context{}, page(10, 99))},
		{name: "ListMessages sender by LID", want: []string{"MERGE (UNION ALL)", phoneSender, lidSender},
			run: list(MessageFilter{Sender: lidJID(bigLID)}, Context{}, page(10, 0))},
		{name: "ListMessages sender, one identity", want: []string{phoneSender}, notWant: []string{"MERGE", lidSender},
			run: list(MessageFilter{Sender: unmapped}, Context{}, page(10, 0))},
		{name: "ListMessages sender and time",
			want: []string{"MERGE (UNION ALL)", "messages_sender_time (sender_phone=? AND time>?)", "messages_sender_jid (sender_jid=? AND time>?)"},
			run:  list(MessageFilter{Sender: bigPhone, After: baseTime}, Context{}, page(10, 0))},
		{name: "ListMessages sender and chat",
			want: []string{"MERGE (UNION ALL)", "messages_chat_sender_phone (chat_jid=? AND sender_phone=?)",
				"messages_chat_sender_jid (chat_jid=? AND sender_jid=?)"},
			notWant: []string{"INDEX " + latest},
			run:     list(MessageFilter{Sender: bigPhone, ChatJID: bigGroup}, Context{}, page(10, 0))},
		{name: "ListMessages sender with search",
			want: []string{"MERGE (UNION ALL)", "USING INDEX messages_sender_time (sender_phone=?", "USING INDEX messages_sender_jid (sender_jid=?"},
			run:  list(MessageFilter{Sender: bigPhone, Query: "message"}, Context{}, page(10, 0))},
		{name: "ListMessages search with context", want: []string{"SCAN messages USING INDEX messages_time", beforeCtx, afterCtx},
			run: list(MessageFilter{Query: "needle"}, Context{Before: 5, After: 5}, page(5, 0))},
		{name: "MessageContext",
			want: []string{"SEARCH messages USING INDEX sqlite_autoindex_messages_1 (chat_jid=? AND id=?)", beforeCtx, afterCtx},
			run: func(ctx context.Context, st *Store) error {
				_, err := st.MessageContext(ctx, bigGroup, "big0-00100", Context{Before: 5, After: 5})
				return err
			}},
		{name: "GetMessage", want: []string{"SEARCH m USING INDEX sqlite_autoindex_messages_1 (chat_jid=? AND id=?)"},
			run: func(ctx context.Context, st *Store) error {
				_, err := st.GetMessage(ctx, bigGroup, "big0-00100")
				return err
			}},
		{name: "GetMessageByID", want: []string{"SEARCH messages USING INDEX messages_id (id=?)", "sqlite_autoindex_messages_1 (chat_jid=? AND id=?)"},
			run: func(ctx context.Context, st *Store) error { _, err := st.GetMessageByID(ctx, "m000042"); return err }},
		{name: "MediaInfo", want: []string{"SEARCH m USING INDEX sqlite_autoindex_messages_1 (chat_jid=? AND id=?)",
			"SEARCH messages USING INDEX sqlite_autoindex_messages_1 (chat_jid=? AND id=?)"},
			run: func(ctx context.Context, st *Store) error {
				_, _, err := st.MediaInfo(ctx, bigGroup, seedMediaID)
				return err
			}},
		{name: "LastInteraction by phone",
			want: []string{latest, "USING INDEX messages_sender_time (sender_phone=?)", "USING INDEX messages_sender_jid (sender_jid=?)"},
			run:  func(ctx context.Context, st *Store) error { _, err := st.LastInteraction(ctx, bigPhone); return err }},
		{name: "LastInteraction by LID",
			want: []string{latest, "USING INDEX messages_sender_time (sender_phone=?)", "USING INDEX messages_sender_jid (sender_jid=?)"},
			run: func(ctx context.Context, st *Store) error {
				_, err := st.LastInteraction(ctx, lidJID(bigLID))
				return err
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec.queries = nil
			if err := tc.run(ctx, st); err != nil {
				t.Fatal(err)
			}
			var all []string
			for _, q := range rec.queries {
				lines := planOf(t, st, q)
				for _, p := range planProblems(lines, tc.allowTemp) {
					t.Errorf("%.80s...: %s", strings.Join(strings.Fields(q.sql), " "), p)
				}
				all = append(all, lines...)
			}
			joined := strings.Join(all, "\n")
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("plan lacks %q:\n%s", w, joined)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(joined, w) {
					t.Errorf("plan has %q:\n%s", w, joined)
				}
			}
		})
	}
}

// BenchmarkQueries times each chat, contact and message query on the seeded database. It is run locally and is not a CI gate.
func BenchmarkQueries(b *testing.B) {
	st := openSeeded(b, nil)
	ctx := context.Background()
	unmapped := phoneJID("491520000005")
	contextOf := Context{Before: 5, After: 5}
	for _, bc := range []struct {
		name string
		run  func() error
	}{
		{"ListChats/activity", func() error { _, err := st.ListChats(ctx, "", ByLastActivity, true, page(50, 0)); return err }},
		{"ListChats/name", func() error { _, err := st.ListChats(ctx, "", ByName, true, page(50, 0)); return err }},
		{"ListChats/search", func() error { _, err := st.ListChats(ctx, "4915200", ByLastActivity, true, page(50, 0)); return err }},
		{"GetChat", func() error { _, err := st.GetChat(ctx, unmapped); return err }},
		{"DirectChat", func() error { _, err := st.DirectChat(ctx, unmapped); return err }},
		{"ContactChats/phone", func() error { _, err := st.ContactChats(ctx, bigPhone, page(50, 0)); return err }},
		{"ContactChats/lid", func() error { _, err := st.ContactChats(ctx, lidJID(bigLID), page(50, 0)); return err }},
		{"SearchContacts/name", func() error { _, err := st.SearchContacts(ctx, "contact 0001", page(50, 0)); return err }},
		{"SearchContacts/number", func() error { _, err := st.SearchContacts(ctx, "+49 153 0000001", page(50, 0)); return err }},
		{"ListMessages/all", func() error { _, err := st.ListMessages(ctx, MessageFilter{}, Context{}, page(50, 0)); return err }},
		{"ListMessages/chat", func() error {
			_, err := st.ListMessages(ctx, MessageFilter{ChatJID: bigGroup}, Context{}, page(50, 0))
			return err
		}},
		{"ListMessages/sender", func() error {
			_, err := st.ListMessages(ctx, MessageFilter{Sender: bigPhone}, Context{}, page(50, 0))
			return err
		}},
		{"ListMessages/search", func() error {
			_, err := st.ListMessages(ctx, MessageFilter{Query: "needle"}, Context{}, page(50, 0))
			return err
		}},
		{"ListMessages/context", func() error {
			_, err := st.ListMessages(ctx, MessageFilter{Query: "needle"}, contextOf, page(50, 0))
			return err
		}},
		{"MessageContext", func() error { _, err := st.MessageContext(ctx, bigGroup, "big0-00100", contextOf); return err }},
		{"GetMessage", func() error { _, err := st.GetMessage(ctx, bigGroup, "big0-00100"); return err }},
		{"GetMessageByID", func() error { _, err := st.GetMessageByID(ctx, "m000042"); return err }},
		{"MediaInfo", func() error { _, _, err := st.MediaInfo(ctx, bigGroup, seedMediaID); return err }},
		{"LastInteraction/phone", func() error { _, err := st.LastInteraction(ctx, bigPhone); return err }},
		{"LastInteraction/lid", func() error { _, err := st.LastInteraction(ctx, lidJID(bigLID)); return err }},
	} {
		b.Run(bc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if err := bc.run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}

	// Each iteration merges a freshly seeded 20,000-message @lid chat into its phone chat; the seeding is not
	// timed. Above 5 s, half the operation deadline, the merge would need splitting.
	b.Run("MergeLID/20000", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			lid := lidJID(fmt.Sprintf("8800%08d", i))
			phone := phoneJID(fmt.Sprintf("4915800%05d", i))
			for lo := 0; lo < 20_000; lo += 1000 {
				batch := make([]Message, 1000)
				for j := range batch {
					batch[j] = msg(lid, fmt.Sprintf("merge-%d-%d", i, lo+j), lid, at(lo+j))
				}
				if err := st.StoreMessages(ctx, batch); err != nil {
					b.Fatal(err)
				}
			}
			b.StartTimer()
			if err := st.MergeLID(ctx, lid, phone); err != nil {
				b.Fatal(err)
			}
		}
	})
}
