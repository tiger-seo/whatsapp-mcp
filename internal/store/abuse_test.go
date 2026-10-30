package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAbuseCases runs the abuse cases that are about the write side and the lifecycle. Each shows the rejected
// action has no side effect: the tables are unchanged and a sentinel file outside the data directory is
// untouched. The search, sort and context cases belong to the query tests.
func TestAbuseCases(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := openTest(t, newDataDir(t))
	mustStore(t, st, msg(alice, "seed", alice, at(0)))
	ctx := context.Background()

	unchanged := func(t *testing.T, before string) {
		t.Helper()
		if got := tableDump(t, st); got != before {
			t.Errorf("the tables changed:\n%s\nwas\n%s", got, before)
		}
		assertFile(t, sentinel, "untouched")
	}

	t.Run("4 search text matches literally", func(t *testing.T) {
		mustStore(t, st,
			msg(alice, "lit-plain", alice, at(10), withText("plain text")),
			msg(alice, "lit-quote", alice, at(11), withText("it said x' OR '1'='1 once")),
			msg(alice, "lit-pct", alice, at(12), withText("100% sure")),
			msg(alice, "lit-under", alice, at(13), withText("snake_case")),
			msg(alice, "lit-slash", alice, at(14), withText(`C:\dir`)),
		)
		nameChat(t, st, bob, "50% off_sale", NameReal)
		before := tableDump(t, st)
		for _, c := range []struct {
			query string
			want  []string
		}{
			{"x' OR '1'='1", []string{"lit-quote"}},
			{"%", []string{"lit-pct"}},
			{"_", []string{"lit-under"}},
			{`\`, []string{"lit-slash"}},
			{"'; DROP TABLE messages; --", nil},
		} {
			hits, err := st.ListMessages(ctx, MessageFilter{Query: c.query}, Context{}, all)
			if err != nil {
				t.Fatalf("search %q: %v", c.query, err)
			}
			if got := hitIDs(hits); !slices.Equal(got, c.want) {
				t.Errorf("messages matching %q: %v, want %v", c.query, got, c.want)
			}
		}
		for query, want := range map[string][]string{"%": {bob}, "_": {bob}, "' OR '1'='1": nil, `\`: nil} {
			chats, err := st.ListChats(ctx, query, ByName, false, all)
			if err != nil {
				t.Fatalf("chat search %q: %v", query, err)
			}
			if got := chatJIDs(chats); !slices.Equal(got, want) {
				t.Errorf("chats matching %q: %v, want %v", query, got, want)
			}
		}
		for _, query := range []string{"%", "_", `\`, "' OR '1'='1"} {
			contacts, err := st.SearchContacts(ctx, query, all)
			if err != nil || len(contacts) != 0 {
				t.Errorf("contact search %q: %v, %v", query, contacts, err)
			}
		}
		if got := tableDump(t, st); got != before {
			t.Error("a search changed the tables")
		}
		assertFile(t, sentinel, "untouched")
	})

	t.Run("5 malformed identifiers and fields", func(t *testing.T) {
		before := tableDump(t, st)
		neg := int64(-1)
		bad := []Message{
			msg("@g.us", "A", alice, at(1)),
			msg("x@evil.example", "A", alice, at(1)),
			msg("\x00@lid", "A", alice, at(1)),
			msg(alice, "", alice, at(1)),
			msg(alice, strings.Repeat("i", maxIDBytes+1), alice, at(1)),
			msg(alice, "A", "nobody@g.us", at(1)),
			msg(alice, "A", alice, time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)),
			msg(alice, "A", alice, at(1), withText(strings.Repeat("x", maxTextBytes+1))),
			msg(alice, "A", alice, at(1), func(m *Message) { m.MediaType = "sticker" }),
			msg(alice, "A", alice, at(1), func(m *Message) { m.Media.MediaKey = make([]byte, 31) }),
			msg(alice, "A", alice, at(1), func(m *Message) { m.Media.FileLength = &neg }),
		}
		for i, m := range bad {
			if err := st.StoreMessage(ctx, m); !errors.Is(err, ErrInvalid) {
				t.Errorf("message %d: %v, want a validation error", i, err)
			}
		}
		for _, u := range []ChatUpdate{{JID: "x@evil.example"}, {JID: alice, Name: strings.Repeat("n", maxNameBytes+1), NameQuality: NameReal, NameTime: at(0)}} {
			if err := st.UpdateChat(ctx, u); !errors.Is(err, ErrInvalid) {
				t.Errorf("update %+v: %v", u, err)
			}
		}
		if _, err := st.ListChats(ctx, "", ChatSort(99), false, all); !errors.Is(err, ErrInvalid) {
			t.Errorf("ChatSort(99): %v", err)
		}
		if _, err := st.ListMessages(ctx, MessageFilter{ChatJID: "x@evil.example"}, Context{}, all); !errors.Is(err, ErrInvalid) {
			t.Errorf("a foreign server in a filter: %v", err)
		}
		if _, err := st.ListMessages(ctx, MessageFilter{}, Context{math.MaxInt, math.MaxInt}, page(math.MaxInt, math.MaxInt)); !errors.Is(err, ErrInvalid) {
			t.Errorf("overflowing page and context: %v", err)
		}
		for _, bad := range []string{"@g.us", "x@evil.example", "\x00@lid"} {
			if _, err := st.GetMessage(ctx, bad, "A"); !errors.Is(err, ErrInvalid) {
				t.Errorf("GetMessage in %q: %v", bad, err)
			}
		}
		unchanged(t, before)
	})

	t.Run("6 an oversized batch is rejected whole", func(t *testing.T) {
		before := tableDump(t, st)
		batch := make([]Message, maxBatchMessages+1)
		for i := range batch {
			batch[i] = msg(bob, "id"+strings.Repeat("0", 3)+string(rune('a'+i%26))+strings.Repeat("z", i/26), bob, at(i%100))
		}
		if err := st.StoreMessages(ctx, batch); !errors.Is(err, ErrResourceLimit) {
			t.Errorf("1,001 messages: %v", err)
		}
		unchanged(t, before)
	})

	t.Run("7 a flood of calls stays bounded", func(t *testing.T) {
		release := holdWriteLock(t, st)
		defer release()
		before := runtime.NumGoroutine()
		var wg sync.WaitGroup
		var busy, other int64
		var mu sync.Mutex
		for i := 0; i < 300; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
				defer cancel()
				err := st.StoreMessage(short, msg(alice, "flood", alice, at(1)))
				mu.Lock()
				if errors.Is(err, ErrBusy) {
					busy++
				} else {
					other++
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
		release()
		if busy == 0 {
			t.Error("no call was refused as busy")
		}
		eventually(t, "goroutines to settle", func() bool { return runtime.NumGoroutine() <= before+5 })
	})

	t.Run("8 limits are errors and not a short list", func(t *testing.T) {
		// The aggregate context rows, the result bytes, the ambiguous IDs and the memory limit have their own
		// tests in limits_test.go; here a request that would exceed the context rows must leave no trace.
		before := tableDump(t, st)
		var msgs []Message
		for i := 0; i < 220; i++ {
			msgs = append(msgs, msg(bob, fmt.Sprintf("a8-%03d", i), bob, at(i)))
		}
		mustStore(t, st, msgs...)
		before = tableDump(t, st)
		if _, err := st.ListMessages(ctx, MessageFilter{ChatJID: bob}, Context{10, 10}, page(200, 0)); !errors.Is(err, ErrResourceLimit) {
			t.Errorf("200 hits with 10/10 context: %v", err)
		}
		unchanged(t, before)
	})

	t.Run("9 low disk and full disk delete nothing", func(t *testing.T) {
		dir := filepath.Dir(st.path)
		before := strings.Join(fileNames(t, dir), ",")
		st.disk.free = func(string) (uint64, error) { return 0, nil }
		st.disk.measured = time.Time{}
		if err := st.StoreMessage(ctx, msg(alice, "low", alice, at(2))); !errors.Is(err, ErrDiskLow) {
			t.Errorf("low disk: %v", err)
		}
		st.disk.free = freeBytes
		st.disk.measured, st.disk.refusing = time.Time{}, false
		if got := strings.Join(fileNames(t, dir), ","); got != before {
			t.Errorf("files changed: %s, was %s", got, before)
		}
	})

	t.Run("10 a newer schema leaves every byte alone", func(t *testing.T) {
		clean, wal := stateDirs(t, "UPDATE mcp_version SET version = 7")
		for _, dir := range []string{clean, wal} {
			before := snapshotDir(t, dir)
			if _, err := open(ctx, dir, nil, openOptions{}); !errors.Is(err, ErrSchemaTooNew) {
				t.Fatalf("open = %v", err)
			}
			after := snapshotDir(t, dir)
			if len(after) != len(before) {
				t.Errorf("files created or removed: %v -> %v", before, after)
			}
			for name, s := range before {
				if after[name] != s {
					t.Errorf("%s changed", name)
				}
			}
		}
		assertFile(t, sentinel, "untouched")
	})
}
