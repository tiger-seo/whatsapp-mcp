package store

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentWritersAndReaders runs 16 writers of 100 messages each beside 4 readers, and expects no error
// (in particular no "database is locked") and every message present.
func TestConcurrentWritersAndReaders(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	var failures atomic.Int32
	report := func(err error) {
		if err != nil {
			failures.Add(1)
			t.Errorf("error: %v", err)
		}
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, q := range []string{
					"SELECT chat_jid, id FROM messages ORDER BY time DESC, chat_jid DESC, id DESC LIMIT 50",
					"SELECT jid FROM chats ORDER BY (last_message_time IS NULL), last_message_time DESC, jid LIMIT 50",
				} {
					rows, err := st.reader.QueryContext(ctx, q)
					if err != nil {
						report(err)
						return
					}
					for rows.Next() {
					}
					report(rows.Err())
					rows.Close()
				}
			}
		}()
	}

	var writers sync.WaitGroup
	for w := 0; w < 16; w++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; i < 100; i++ {
				chat := fmt.Sprintf("1203630000000000%02d@g.us", w)
				report(st.StoreMessage(ctx, msg(chat, fmt.Sprintf("w%d-%d", w, i), alice, at(i))))
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	if n := countRows(t, st, "messages"); n != 1600 {
		t.Errorf("messages = %d, want 1600", n)
	}
	if failures.Load() != 0 {
		t.Errorf("%d operations failed", failures.Load())
	}
}
