package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
