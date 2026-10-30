package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// heavyEnv switches on the tests that take minutes on a slow disk: the 500 MiB listing and the 400,000-message
// point of the memory test. They check a limit once and are not run on pull requests; the nightly job sets it.
const heavyEnv = "WA_HEAVY_TESTS"

func heavyTests() bool { return os.Getenv(heavyEnv) == "1" }

// memoryUsed returns the bytes SQLite has allocated now.
func memoryUsed() int64 {
	tls := libc.NewTLS()
	defer tls.Close()
	return sqlite3.Xsqlite3_memory_used(tls)
}

// releaseCaches asks SQLite to give back every page of cache it can. Near the heap limit SQLite does this on its
// own, which would hide a query's own need, so the heap-limit test measures from the emptied state.
func releaseCaches() int {
	tls := libc.NewTLS()
	defer tls.Close()
	return int(sqlite3.Xsqlite3_release_memory(tls, 1<<30))
}

func sizeOfFile(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func hitsBytes(hits []Hit) int {
	n := 0
	add := func(m MessageView) { n += len(m.Text) + len(m.Caption) + len(m.ID) + len(m.ChatJID) + len(m.SenderJID) }
	for _, h := range hits {
		add(h.Message)
		for _, m := range h.Before {
			add(m)
		}
		for _, m := range h.After {
			add(m)
		}
	}
	return n
}

// observe runs one limit case and logs what it cost: elapsed time, result bytes, live-heap growth, SQLite's own
// memory high-water mark, the gate's counts and the size of the database and its -wal. The heap growth must stay
// below twice the approved result budget and SQLite's mark below the 256 MiB limit.
func observe(t *testing.T, st *Store, label string, fn func() (resultBytes int, err error)) error {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	memoryHighwater(true)
	// A call that builds a large result and then fails leaves little behind, so the heap is sampled while it runs.
	var peak atomic.Uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	start := time.Now()
	n, err := fn()
	elapsed := time.Since(start)
	close(stop)
	<-sampled
	runtime.GC()
	runtime.ReadMemStats(&after)
	growth := int64(peak.Load()) - int64(before.HeapAlloc)
	hw := memoryHighwater(false)
	t.Logf("%-44s %9v result=%-9d heap-peak+=%-9d sqlite-high=%-9d active=%d waiting=%d db=%d wal=%d -> %v",
		label, elapsed.Round(time.Microsecond), n, growth, hw, st.gate.activeCount(), st.gate.waitingCount(),
		sizeOfFile(st.path), sizeOfFile(st.path+"-wal"), codeOf(err))
	if growth > 2*maxResultBytes {
		t.Errorf("%s: the heap peaked %d above its start, want below 2 x %d", label, growth, maxResultBytes)
	}
	if hw >= maxHeapBytes {
		t.Errorf("%s: SQLite's high-water mark is %d, want below %d", label, hw, maxHeapBytes)
	}
	return err
}

// limitCase is one constant: ok sits on the boundary and must succeed, over sits one beyond and must fail with
// want and write nothing.
type limitCase struct {
	name     string
	ok, over func() error
	want     error
}

func TestLimitsAtTheBoundary(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t, newDataDir(t))
	var seq atomic.Int64
	uniq := func() string { return fmt.Sprintf("u%d", seq.Add(1)) }

	store := func(mut func(m *Message)) func() error {
		return func() error {
			return st.StoreMessage(ctx, msg(alice, uniq(), alice, at(0), mut))
		}
	}
	text := func(n int) string { return strings.Repeat("t", n) }
	groupOf := func(n int) string { return strings.Repeat("1", n-len("@g.us")) + "@g.us" }
	lidOf := func(n int) string { return strings.Repeat("7", n-len("@lid")) + "@lid" }
	media := func(mut func(m *MediaMeta)) func() error {
		return store(func(m *Message) { m.MediaType = "image"; mut(&m.Media) })
	}
	length := func(v int64) func(m *MediaMeta) { return func(m *MediaMeta) { m.FileLength = &v } }
	hash := func(n int) func(m *MediaMeta) {
		return func(m *MediaMeta) { m.MediaKey = make([]byte, n) }
	}

	// A batch of n messages with the given text length each.
	batch := func(n int, textLen int) []Message {
		out := make([]Message, n)
		for i := range out {
			out[i] = msg(alice, uniq(), alice, at(i%50), withText(text(textLen)))
		}
		return out
	}
	// A batch whose field bytes are exactly total: whole 256 KiB messages and one that takes the rest.
	exactBatch := func(total int) []Message {
		one := batch(1, maxTextBytes)
		_, perMessage, err := one[0].validate()
		if err != nil {
			t.Fatal(err)
		}
		_, empty, _ := msg(alice, uniq(), alice, at(0), withText("")).validate()
		whole := (total - empty) / perMessage
		out := batch(whole, maxTextBytes)
		rest := total - whole*perMessage
		last := msg(alice, uniq(), alice, at(0), withText(text(rest-empty)))
		return append(out, last)
	}
	updates := func(n int) []ChatUpdate {
		out := make([]ChatUpdate, n)
		for i := range out {
			out[i] = ChatUpdate{JID: fmt.Sprintf("49%010d@s.whatsapp.net", i)}
		}
		return out
	}

	cases := []limitCase{
		{"chat JID 256 B", store(func(m *Message) { m.ChatJID = groupOf(256) }), store(func(m *Message) { m.ChatJID = groupOf(257) }), ErrInvalid},
		{"sender JID 256 B", store(func(m *Message) { m.SenderJID = lidOf(256) }), store(func(m *Message) { m.SenderJID = lidOf(257) }), ErrInvalid},
		{"message ID 256 B", store(func(m *Message) { m.ID = text(256) }), store(func(m *Message) { m.ID = text(257) }), ErrInvalid},
		{"text 256 KiB", store(withText(text(maxTextBytes))), store(withText(text(maxTextBytes + 1))), ErrInvalid},
		{"caption 256 KiB", store(withCaption(text(maxCaptionBytes))), store(withCaption(text(maxCaptionBytes + 1))), ErrInvalid},
		{"media URL 4 KiB", media(func(m *MediaMeta) { m.URL = text(maxURLBytes) }), media(func(m *MediaMeta) { m.URL = text(maxURLBytes + 1) }), ErrInvalid},
		{"media path 4 KiB", media(func(m *MediaMeta) { m.DirectPath = text(maxPathBytes) }), media(func(m *MediaMeta) { m.DirectPath = text(maxPathBytes + 1) }), ErrInvalid},
		{"MIME type 255 B", media(func(m *MediaMeta) { m.MIMEType = text(maxMIMEBytes) }), media(func(m *MediaMeta) { m.MIMEType = text(maxMIMEBytes + 1) }), ErrInvalid},
		{"file name 1 KiB", media(func(m *MediaMeta) { m.FileName = text(maxFileNameBytes) }), media(func(m *MediaMeta) { m.FileName = text(maxFileNameBytes + 1) }), ErrInvalid},
		{"media key 32 B", media(hash(mediaHashBytes)), media(hash(mediaHashBytes + 1)), ErrInvalid},
		{"media key 31 B", media(hash(mediaHashBytes)), media(hash(mediaHashBytes - 1)), ErrInvalid},
		{"file length 4 GiB", media(length(maxFileLength)), media(length(maxFileLength + 1)), ErrInvalid},
		{"chat name 1 KiB",
			func() error {
				return st.UpdateChat(ctx, ChatUpdate{JID: alice, Name: text(maxNameBytes), NameQuality: NameReal, NameTime: at(0)})
			},
			func() error {
				return st.UpdateChat(ctx, ChatUpdate{JID: alice, Name: text(maxNameBytes + 1), NameQuality: NameReal, NameTime: at(0)})
			}, ErrInvalid},
		{"chat search 1 KiB",
			func() error {
				_, err := st.ListChats(ctx, text(maxSearchBytes), ByName, false, page(10, 0))
				return err
			},
			func() error {
				_, err := st.ListChats(ctx, text(maxSearchBytes+1), ByName, false, page(10, 0))
				return err
			}, ErrInvalid},
		{"message search 1 KiB",
			func() error {
				_, err := st.ListMessages(ctx, MessageFilter{Query: text(maxSearchBytes)}, Context{}, page(10, 0))
				return err
			},
			func() error {
				_, err := st.ListMessages(ctx, MessageFilter{Query: text(maxSearchBytes + 1)}, Context{}, page(10, 0))
				return err
			}, ErrInvalid},
		{"batch of 1,000 messages",
			func() error { return st.StoreMessages(ctx, batch(maxBatchMessages, 1)) },
			func() error { return st.StoreMessages(ctx, batch(maxBatchMessages+1, 1)) }, ErrResourceLimit},
		{"batch of 16 MiB",
			func() error { return st.StoreMessages(ctx, exactBatch(maxBatchBytes)) },
			func() error { return st.StoreMessages(ctx, exactBatch(maxBatchBytes+1)) }, ErrResourceLimit},
		{"1,000 chat updates",
			func() error { return st.UpdateChats(ctx, updates(maxChatUpdates)) },
			func() error { return st.UpdateChats(ctx, updates(maxChatUpdates+1)) }, ErrResourceLimit},
		{"list size 200",
			func() error { _, err := st.ListChats(ctx, "", ByName, false, page(maxListSize, 0)); return err },
			func() error { _, err := st.ListChats(ctx, "", ByName, false, page(maxListSize+1, 0)); return err }, ErrInvalid},
		{"offset 100,000",
			func() error { _, err := st.ListChats(ctx, "", ByName, false, page(10, maxOffset)); return err },
			func() error { _, err := st.ListChats(ctx, "", ByName, false, page(10, maxOffset+1)); return err }, ErrInvalid},
		{"context 20 per side",
			func() error {
				_, err := st.ListMessages(ctx, MessageFilter{}, Context{maxContextSide, maxContextSide}, page(1, 0))
				return err
			},
			func() error {
				_, err := st.ListMessages(ctx, MessageFilter{}, Context{maxContextSide + 1, 0}, page(1, 0))
				return err
			}, ErrInvalid},
		{"math.MaxInt offset",
			func() error { _, err := st.ListChats(ctx, "", ByName, false, page(10, 0)); return err },
			func() error { _, err := st.ListChats(ctx, "", ByName, false, page(10, math.MaxInt)); return err }, ErrInvalid},
		{"math.MaxInt limit",
			func() error { _, err := st.ListChats(ctx, "", ByName, false, page(10, 0)); return err },
			func() error { _, err := st.ListChats(ctx, "", ByName, false, page(math.MaxInt, 0)); return err }, ErrInvalid},
		{"math.MaxInt context",
			func() error { _, err := st.ListMessages(ctx, MessageFilter{}, Context{}, page(1, 0)); return err },
			func() error {
				_, err := st.ListMessages(ctx, MessageFilter{}, Context{math.MaxInt, math.MaxInt}, page(1, 0))
				return err
			}, ErrInvalid},
		{"math.MinInt offset and context",
			func() error { _, err := st.ListMessages(ctx, MessageFilter{}, Context{}, page(1, 0)); return err },
			func() error {
				_, err := st.ListMessages(ctx, MessageFilter{}, Context{math.MinInt, 0}, page(1, math.MinInt))
				return err
			}, ErrInvalid},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := observe(t, st, c.name+" (at the limit)", func() (int, error) { return 0, c.ok() }); err != nil {
				t.Fatalf("at the limit: %v", err)
			}
			before := tableDump(t, st)
			err := observe(t, st, c.name+" (beyond)", func() (int, error) { return 0, c.over() })
			if !errors.Is(err, c.want) {
				t.Fatalf("beyond the limit: %v, want %v", err, c.want)
			}
			if got := tableDump(t, st); got != before {
				t.Error("a rejected call changed the tables")
			}
		})
	}
}

// TestLimitsOfTheListings seeds enough rows to reach the limits that depend on stored data: aggregate context
// rows, the ambiguous-ID chats and the result bytes.
func TestLimitsOfTheListings(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t, newDataDir(t))

	t.Run("aggregate context rows", func(t *testing.T) {
		var msgs []Message
		for i := 0; i < 250; i++ {
			msgs = append(msgs, msg(alice, fmt.Sprintf("c%03d", i), alice, at(i)))
		}
		mustStore(t, st, msgs...)
		var hits []Hit
		err := observe(t, st, "100 hits x 10/10", func() (int, error) {
			var err error
			hits, err = st.ListMessages(ctx, MessageFilter{}, Context{10, 10}, page(100, 0))
			return hitsBytes(hits), err
		})
		if err != nil || len(hits) != 100 {
			t.Fatalf("100 hits with 10/10 context: %d hits, %v", len(hits), err)
		}
		err = observe(t, st, "200 hits x 10/10", func() (int, error) {
			_, err := st.ListMessages(ctx, MessageFilter{}, Context{10, 10}, page(200, 0))
			return 0, err
		})
		if !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("200 hits with 10/10 context = 4,000 rows: %v, want ErrResourceLimit", err)
		}
	})

	t.Run("ambiguous ID chats", func(t *testing.T) {
		chats := func(n int) []Message {
			out := make([]Message, n)
			for i := range out {
				jid := phoneJID(fmt.Sprintf("4917%08d", i))
				out[i] = msg(jid, "dup", jid, at(i))
			}
			return out
		}
		mustStore(t, st, chats(maxAmbiguousIDs)...)
		var amb *AmbiguousIDError
		err := observe(t, st, "ID in 100 chats", func() (int, error) { _, err := st.GetMessageByID(ctx, "dup"); return 0, err })
		if !errors.As(err, &amb) || len(amb.ChatJIDs) != maxAmbiguousIDs {
			t.Fatalf("an ID in 100 chats: %v, want the list of 100", err)
		}
		mustStore(t, st, msg(phoneJID("491799999999"), "dup", phoneJID("491799999999"), at(0)))
		err = observe(t, st, "ID in 101 chats", func() (int, error) { _, err := st.GetMessageByID(ctx, "dup"); return 0, err })
		if !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("an ID in 101 chats: %v, want ErrResourceLimit", err)
		}
	})

	t.Run("result bytes", func(t *testing.T) {
		big := strings.Repeat("b", maxTextBytes)
		for lo := 0; lo < 130; lo += 60 {
			var batch []Message
			for i := lo; i < min(lo+60, 130); i++ {
				batch = append(batch, msg(bob, fmt.Sprintf("big%03d", i), bob, at(i), withText(big)))
			}
			mustStore(t, st, batch...)
		}
		var hits []Hit
		err := observe(t, st, "100 rows of 256 KiB", func() (int, error) {
			var err error
			hits, err = st.ListMessages(ctx, MessageFilter{ChatJID: bob}, Context{}, page(100, 0))
			return hitsBytes(hits), err
		})
		if err != nil || len(hits) != 100 {
			t.Fatalf("a page of 25 MiB: %d hits, %v", len(hits), err)
		}
		hits = nil
		err = observe(t, st, "130 rows of 256 KiB", func() (int, error) {
			_, err := st.ListMessages(ctx, MessageFilter{ChatJID: bob}, Context{}, page(130, 0))
			return 0, err
		})
		if !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("a page of 32.5 MiB: %v, want ErrResourceLimit and not a shorter list", err)
		}
	})
}

// TestHeavyContactListing seeds one contact with 2,000 messages of 256 KiB and lists them by phone and by LID at
// offset 1,800. The skipped rows come from covering indexes and only the page's rows are read, so the call
// succeeds within the limits.
func TestHeavyContactListing(t *testing.T) {
	if !heavyTests() {
		t.Skip("seeds 500 MiB; set " + heavyEnv + "=1 to run it")
	}
	ctx := context.Background()
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	const phone, lid = "491551234567", "900000001234"
	seedLIDMapping(t, dev, lid, phone)

	if _, err := st.writer.ExecContext(ctx, "INSERT INTO chats (jid, phone) VALUES (?, '')", groupOne); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("h", maxTextBytes)
	for lo := 0; lo < 2000; lo += 200 {
		tx, err := st.writer.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO messages (chat_jid, id, sender_jid, sender_phone, time, from_me, text, text_fold)
			VALUES (?, ?, ?, ?, ?, 0, ?, '')`)
		if err != nil {
			t.Fatal(err)
		}
		for n := lo; n < lo+200; n++ {
			sender, senderPhone := phoneJID(phone), phone
			if n%2 == 1 {
				sender, senderPhone = lidJID(lid), ""
			}
			if _, err := stmt.ExecContext(ctx, groupOne, fmt.Sprintf("heavy%04d", n), sender, senderPhone, baseTime.UnixMilli()+int64(n)*1000, body); err != nil {
				t.Fatal(err)
			}
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for _, contact := range []string{phone, lidJID(lid)} {
		var hits []Hit
		err := observe(t, st, "2,000 x 256 KiB by "+contact[:6], func() (int, error) {
			var err error
			hits, err = st.ListMessages(ctx, MessageFilter{Sender: contact}, Context{}, page(100, 1800))
			return hitsBytes(hits), err
		})
		if err != nil || len(hits) != 100 {
			t.Fatalf("offset 1,800 for %s: %d hits, %v", contact, len(hits), err)
		}
	}
}

// ---- Memory tests, in child processes: they change or measure process-wide SQLite state. ----

func TestSearchContactsHeapLimit(t *testing.T) {
	t.Log(runChild(t, "TestSearchContactsHeapLimitChild"))
}

// TestSearchContactsHeapLimitChild measures what SearchContacts over 5,000 contacts needs for its grouping, then
// sets SQLite's hard heap limit below it. The call must fail with ErrResourceLimit "memory", not crash.
func TestSearchContactsHeapLimitChild(t *testing.T) {
	inChild(t, "TestSearchContactsHeapLimitChild")
	ctx := context.Background()
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)

	tx, err := st.writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5000; i++ {
		phone := fmt.Sprintf("49159%07d", i)
		if _, err := tx.ExecContext(ctx, "INSERT INTO whatsmeow_contacts (our_jid, their_jid, full_name) VALUES (?, ?, ?)",
			dev.ID.String(), phoneJID(phone), fmt.Sprintf("Contact %05d with a longer display name", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO whatsmeow_lid_map (lid, pn) VALUES (?, ?)", fmt.Sprintf("8%011d", i), phone); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if _, err := st.SearchContacts(ctx, "Contact", page(10, 0)); err != nil { // warm the statements
		t.Fatal(err)
	}
	used := memoryUsed()
	memoryHighwater(true)
	if _, err := st.SearchContacts(ctx, "Contact", page(10, 0)); err != nil {
		t.Fatal(err)
	}
	need := memoryHighwater(false) - used
	t.Logf("SearchContacts over 5,000 contacts needs %d B above the %d B in use", need, used)
	if need < 32*kib {
		t.Fatalf("the grouping needs only %d B; the test cannot show a limit", need)
	}

	// Near the limit SQLite gives its page caches back before it fails an allocation, so the limit is set from
	// what remains with the caches emptied: the limit must be below what the grouping alone needs.
	releaseCaches()
	used = memoryUsed()
	setHardHeapLimit(used + need/2)
	_, err = st.SearchContacts(ctx, "Contact", page(10, 0))
	var rl *ResourceLimitError
	if !errors.As(err, &rl) || rl.Limit != "memory" {
		t.Fatalf("under a lowered limit: %v, want ErrResourceLimit memory", err)
	}
	t.Logf("with the limit at %d B: %v", used+need/2, err)
}

// heldReader is a test wrapper that holds a sender listing after its first row until n listings have reached that
// point, so their peaks overlap.
type heldReader struct {
	querier
	arrived *atomic.Int32
	release chan struct{}
	n       int32
}

func (h heldReader) afterFirstRow(ctx context.Context) {
	if h.arrived.Add(1) == h.n {
		close(h.release)
	}
	select {
	case <-h.release:
	case <-ctx.Done():
	}
}

func holdAfterFirstRow(st *Store, n int) {
	var arrived atomic.Int32
	release := make(chan struct{})
	st.hooks.wrapReader = func(q querier) querier {
		return heldReader{querier: q, arrived: &arrived, release: release, n: int32(n)}
	}
}

const splitEnv = "GO_STORE_SPLIT"

func TestPerContactMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 200,000 messages twice")
	}
	for _, split := range []string{"even", "phone"} {
		t.Run(split, func(t *testing.T) {
			t.Parallel()
			t.Log(runChild(t, "TestPerContactMemoryChild", splitEnv+"="+split))
		})
	}
}

// TestPerContactMemoryChild checks that a sender listing's memory does not grow with the contact's history or
// with the offset. The contact's messages sit under the phone and the LID in the given split.
func TestPerContactMemoryChild(t *testing.T) {
	inChild(t, "TestPerContactMemoryChild")
	split := os.Getenv(splitEnv)
	ctx := context.Background()
	// Filling every reader's cache scans the whole table, which a slow runner cannot do within the 10 s a normal
	// operation gets. The child process is the only one that sees the longer limit.
	operationLimit = 5 * time.Minute
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	const phone, lid = "491551234567", "900000001234"
	seedLIDMapping(t, dev, lid, phone)

	const chats = 50
	groupJID := func(i int) string { return fmt.Sprintf("%0115d@g.us", i) } // 120 B
	for i := 0; i < chats; i++ {
		if _, err := st.writer.ExecContext(ctx, "INSERT INTO chats (jid, phone) VALUES (?, '')", groupJID(i)); err != nil {
			t.Fatal(err)
		}
	}
	seeded := 0
	seedTo := func(total int) {
		count, next := total-seeded, seeded
		for done := 0; done < count; {
			tx, err := st.writer.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			stmt, err := tx.PrepareContext(ctx, `INSERT INTO messages (chat_jid, id, sender_jid, sender_phone, time, from_me, text, text_fold)
				VALUES (?, ?, ?, ?, ?, 0, ?, ?)`)
			if err != nil {
				t.Fatal(err)
			}
			for end := min(done+10_000, count); done < end; done++ {
				n := next
				next++
				sender, senderPhone := phoneJID(phone), phone
				if split == "even" && n%2 == 1 {
					sender, senderPhone = lidJID(lid), ""
				}
				id := fmt.Sprintf("%0150d", n) // 150 B
				text := fmt.Sprintf("message %d", n)
				if _, err := stmt.ExecContext(ctx, groupJID(n%chats), id, sender, senderPhone, baseTime.UnixMilli()+int64(n)*1000, text, text); err != nil {
					t.Fatal(err)
				}
			}
			stmt.Close()
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		seeded = total
	}

	listing := func(offset int) func() error {
		return func() error {
			_, err := st.ListMessages(ctx, MessageFilter{Sender: phone}, Context{}, page(200, offset))
			return err
		}
	}
	// barrier holds each of n operations at the start of its read until all n have arrived, so n different reader
	// connections are in use at once.
	barrier := func(n int) {
		var arrived atomic.Int32
		release := make(chan struct{})
		st.hooks.queryHold = func(ctx context.Context, _ string) {
			if int(arrived.Add(1)) == n {
				close(release)
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	runAll := func(fns ...func() error) {
		var wg sync.WaitGroup
		errs := make([]error, len(fns))
		for i, fn := range fns {
			wg.Add(1)
			go func() { defer wg.Done(); errs[i] = fn() }()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// fill reads the whole table on every reader connection, so each page cache is at its 2,000 KiB ceiling and
	// reuses its slots from then on.
	fill := func() {
		barrier(readConns)
		scan := func() error {
			return st.read(ctx, "fill", func(ctx context.Context, conn *sql.Conn) error {
				var n, sum int64
				return conn.QueryRowContext(ctx, "SELECT count(*), sum(length(text)) FROM messages").Scan(&n, &sum)
			})
		}
		runAll(scan, scan, scan, scan)
	}
	// warm fills the caches, then runs the listings once on every reader connection.
	warm := func(offsets ...int) {
		fill()
		for _, off := range offsets {
			barrier(readConns)
			runAll(listing(off), listing(off), listing(off), listing(off))
		}
		st.hooks.queryHold = nil
	}
	growth := func(fn func() error) int64 {
		used := memoryUsed()
		memoryHighwater(true)
		if err := fn(); err != nil {
			t.Fatal(err)
		}
		return memoryHighwater(false) - used
	}

	// Growth is measured at three sizes. Each must stay at most 256 KiB at every offset, and from the second size
	// to the third it may rise by at most 16 KiB, which is allocator rounding and slot reuse, not history. The cost
	// of a GetMessage, which cannot depend on the contact, is logged next to it, before and after a checkpoint,
	// to show where the fixed part comes from.
	floor := func() int64 {
		return growth(func() error { _, err := st.GetMessage(ctx, groupJID(0), fmt.Sprintf("%0150d", 0)); return err })
	}
	previous := map[int]int64{}
	sizes := []int{50_000, 200_000}
	if heavyTests() {
		sizes = append(sizes, 400_000) // shows that the cost stops growing; costs about 100 s more per child
	}
	for _, size := range sizes {
		seedTo(size)
		warm(0, 49_800, 99_800)
		before := floor()
		if _, err := st.writer.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			t.Fatal(err)
		}
		warm(0, 49_800, 99_800)
		t.Logf("split=%s %d messages: a GetMessage grows %d B, and %d B after a checkpoint", split, size, before, floor())
		current := map[int]int64{}
		for _, off := range []int{0, 49_800, 99_800} {
			if off >= size {
				continue
			}
			g := growth(listing(off))
			current[off] = g
			t.Logf("split=%s %d messages, offset %d: growth %d B", split, size, off, g)
			if g > 256*kib {
				t.Errorf("%d messages, offset %d: growth %d B, want at most 256 KiB", size, off, g)
			}
			if size == 400_000 {
				if p, ok := previous[off]; ok && g > p+16*kib {
					t.Errorf("offset %d: growth rose from %d B to %d B between 200,000 and 400,000 messages, want at most +16 KiB", off, p, g)
				}
			}
		}
		previous = current
	}

	// Two deep and two shallow listings at once.
	warm(99_800, 9_800)
	used := memoryUsed()
	memoryHighwater(true)
	holdAfterFirstRow(st, 4)
	runAll(listing(99_800), listing(99_800), listing(9_800), listing(9_800))
	st.hooks.wrapReader = nil
	together := memoryHighwater(false) - used
	t.Logf("split=%s two deep and two shallow listings at once: growth %d B", split, together)
	if together > mib {
		t.Errorf("four concurrent listings grew %d B, want at most 1 MiB", together)
	}

	// LastInteraction on the same contact.
	used = memoryUsed()
	memoryHighwater(true)
	start := time.Now()
	if _, err := st.LastInteraction(ctx, phone); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	last := memoryHighwater(false) - used
	t.Logf("split=%s LastInteraction: growth %d B in %v", split, last, elapsed)
	if last >= mib || elapsed >= 50*time.Millisecond {
		t.Errorf("LastInteraction used %d B in %v, want below 1 MB and 50 ms", last, elapsed)
	}
}

// TestContactChatsResultBytes gives a contact 200 groups whose latest message holds 256 KiB of text and 256 KiB of
// caption. The result is far over 32 MiB, so the call must fail with ErrResourceLimit, and it must stop counting
// as it reads: the heap may not hold every latest message before the bound is checked.
func TestContactChatsResultBytes(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t, newDataDir(t))
	const phone = "491551234567"
	big := strings.Repeat("c", maxTextBytes)
	tx, err := st.writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		jid := fmt.Sprintf("1203630000000%05d@g.us", i)
		if _, err := tx.ExecContext(ctx, "INSERT INTO chats (jid, phone, last_message_time) VALUES (?, '', ?)", jid, baseTime.UnixMilli()+int64(i)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages (chat_jid, id, sender_jid, sender_phone, time, from_me, text, caption, text_fold, caption_fold)
			VALUES (?, 'latest', ?, ?, ?, 0, ?, ?, '', '')`, jid, phoneJID(phone), phone, baseTime.UnixMilli()+int64(i), big, big); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	err = observe(t, st, "ContactChats of 200 x 512 KiB", func() (int, error) {
		_, err := st.ContactChats(ctx, phone, page(200, 0))
		return 0, err
	})
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("a result of about 50 MiB: %v, want ErrResourceLimit", err)
	}
	err = observe(t, st, "ListChats with latest of 200 x 512 KiB", func() (int, error) {
		_, err := st.ListChats(ctx, "", ByLastActivity, true, page(200, 0))
		return 0, err
	})
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("a chat listing of about 50 MiB: %v, want ErrResourceLimit", err)
	}
}

// TestBeforeFilterBelowAMillisecond checks that a strict "before" bound that falls inside a millisecond keeps a
// message stored at that millisecond, because the message is earlier than the bound.
func TestBeforeFilterBelowAMillisecond(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st, msg(alice, "ms", alice, baseTime))
	for _, c := range []struct {
		before time.Time
		want   int
	}{
		{baseTime.Add(500 * time.Microsecond), 1},
		{baseTime, 0},
		{baseTime.Add(time.Millisecond), 1},
	} {
		hits, err := st.ListMessages(ctx, MessageFilter{Before: c.before}, Context{}, all)
		if err != nil || len(hits) != c.want {
			t.Errorf("before %v: %d hits, %v; want %d", c.before.Format(time.RFC3339Nano), len(hits), err, c.want)
		}
	}
}

// TestBadSenderIsRejectedWhateverTheRange checks that a sender that is no contact is a validation error naming the
// sender, also when the time range is empty.
func TestBadSenderIsRejectedWhateverTheRange(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t, newDataDir(t))
	for name, f := range map[string]MessageFilter{
		"valid range":    {Sender: "not-a-number"},
		"reversed range": {Sender: "not-a-number", After: at(10), Before: at(5)},
	} {
		_, err := st.ListMessages(ctx, f, Context{}, all)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field != "sender" {
			t.Errorf("%s: %v, want a validation error naming the sender", name, err)
		}
	}
}
