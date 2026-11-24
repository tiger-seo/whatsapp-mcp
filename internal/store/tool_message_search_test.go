package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestToolMessageSearchKeys(t *testing.T) {
	f := newToolMessageFixture(t)
	mustStore(t, f.store,
		msg(f.phone, "accent", f.phone, at(10), withText("Cafe\u0301 STRASSE")),
		msg(f.phone, "caption", f.phone, at(11), withCaption("literal 100%_!")),
		msg(groupOne, "tie-a", f.alias, at(12), withText("Needle")),
		msg(f.phone, "tie-b", f.phone, at(12), withText("needle")),
		msg(groupTwo, "unmapped", lidJID("987654321"), at(13), withText("unmapped marker")),
	)
	if _, err := f.store.writer.ExecContext(t.Context(), `UPDATE messages SET sender_phone='491511111111' WHERE chat_jid=? AND id='tie-a'`, groupOne); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		filter MessageFilter
		page   Page
		want   []string
	}{
		{"canonical fold", MessageFilter{Query: "CAFÉ strasse"}, Page{Limit: 10}, []string{"accent"}},
		{"literal caption", MessageFilter{Query: "%_!"}, Page{Limit: 10}, []string{"caption"}},
		{"chat and query", MessageFilter{ChatJID: groupOne, Query: "NEEDLE"}, Page{Limit: 10}, []string{"tie-a"}},
		{"phone author aliases", MessageFilter{Sender: f.phone, Query: "needle"}, Page{Limit: 10}, []string{"tie-b", "tie-a"}},
		{"lid author aliases", MessageFilter{Sender: f.alias, Query: "needle"}, Page{Limit: 10}, []string{"tie-b", "tie-a"}},
		{"chat and author", MessageFilter{ChatJID: groupOne, Sender: f.phone, Query: "needle"}, Page{Limit: 10}, []string{"tie-a"}},
		{"unmapped lid", MessageFilter{Sender: lidJID("987654321")}, Page{Limit: 10}, []string{"unmapped"}},
		{"exclusive dates", MessageFilter{After: at(10), Before: at(12)}, Page{Limit: 10}, []string{"caption"}},
		{"equal dates", MessageFilter{After: at(12), Before: at(12)}, Page{Limit: 10}, nil},
		{"reversed dates", MessageFilter{After: at(12), Before: at(10)}, Page{Limit: 10}, nil},
		{"offset", MessageFilter{Query: "needle"}, Page{Limit: 1, Offset: 1}, []string{"tie-a"}},
		{"end", MessageFilter{Query: "needle"}, Page{Limit: 1, Offset: 2}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageKey, error) {
				return searchMessageKeys(ctx, q, tc.filter, tc.page)
			})
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, key := range keys {
				ids = append(ids, key.id)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("got %v, want %v", ids, tc.want)
			}
		})
	}
}

func TestToolMessageSearchRejectsUnverifiedPage(t *testing.T) {
	f := newToolMessageFixture(t)
	if _, err := f.store.writer.ExecContext(t.Context(), `UPDATE messages SET text_fold=? WHERE chat_jid=? AND id='direct'`, strings.Repeat("x", messageSearchFieldFold+1), f.phone); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"synthetic", "absent"} {
		keys, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageKey, error) {
			return searchMessageKeys(ctx, q, MessageFilter{Query: query}, Page{Limit: 50})
		})
		if !errors.Is(err, ErrResourceLimit) || len(keys) != 0 {
			t.Fatalf("query %q: keys=%v err=%v", query, keys, err)
		}
	}
}

func TestToolMessageSearchRejectsMalformedUTF8(t *testing.T) {
	for _, value := range []string{string([]byte{0xff}), string([]byte{'x', 0, 0xff})} {
		f := newToolMessageFixture(t)
		if _, err := f.store.writer.ExecContext(t.Context(), `UPDATE messages SET text=?,text_fold=? WHERE chat_jid=? AND id='direct'`, value, value, f.phone); err != nil {
			t.Fatal(err)
		}
		keys, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageKey, error) {
			return searchMessageKeys(ctx, q, MessageFilter{Query: "absent"}, Page{Limit: 50})
		})
		if !errors.Is(err, ErrResourceLimit) || len(keys) != 0 {
			t.Fatalf("malformed search field: %v %v", keys, err)
		}
	}
}

func TestToolMessageSearchBatchGuardsBeforeBody(t *testing.T) {
	f := newToolMessageFixture(t)
	if _, err := f.store.writer.ExecContext(t.Context(), `UPDATE messages SET text=?,text_fold=? WHERE chat_jid=? AND id='direct'`, "\xff", "\xff", f.phone); err != nil {
		t.Fatal(err)
	}
	filt := MessageFilter{ChatJID: f.phone, Query: "absent"}
	_, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) (int, error) {
		keys, _, count, raw, folded, err := searchMessageBatch(ctx, q, filt, senderBranch{}, messageKey{time: math.MaxInt64}, 1, filt.Query, 0, 0)
		if !errors.Is(err, errSearchBatchBytes) || count != 1 || len(keys) != 0 || raw != 0 || folded != 0 {
			t.Errorf("over-budget batch: keys=%v count=%d raw=%d folded=%d err=%v", keys, count, raw, folded, err)
		}
		keys, _, _, _, _, err = searchMessageBatch(ctx, q, filt, senderBranch{}, messageKey{time: math.MaxInt64}, 1, filt.Query, 2*messageSearchField, 2*messageSearchFieldFold)
		if !errors.Is(err, ErrResourceLimit) || len(keys) != 0 {
			t.Errorf("reserved batch accepted malformed field: keys=%v err=%v", keys, err)
		}
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type capturedSearchQuery struct {
	querier
	sql  string
	args []any
}

func (q *capturedSearchQuery) QueryRowContext(ctx context.Context, sql string, args ...any) *sql.Row {
	q.sql, q.args = sql, args
	return q.querier.QueryRowContext(ctx, sql, args...)
}

func TestToolMessageSearchReservedSeekPlans(t *testing.T) {
	f := newToolMessageFixture(t)
	for _, tc := range []struct {
		chat   string
		branch senderBranch
		index  string
	}{
		{"", senderBranch{}, "messages_time"},
		{f.phone, senderBranch{}, "messages_chat_time"},
		{"", senderBranch{column: "sender_phone", value: "491511111111"}, "messages_sender_time"},
		{"", senderBranch{column: "sender_jid", value: f.alias}, "messages_sender_jid"},
		{f.phone, senderBranch{column: "sender_phone", value: "491511111111"}, "messages_chat_sender_phone"},
		{f.phone, senderBranch{column: "sender_jid", value: f.alias}, "messages_chat_sender_jid"},
	} {
		t.Run(tc.index, func(t *testing.T) {
			q := &capturedSearchQuery{querier: f.store.reader}
			filter := MessageFilter{ChatJID: tc.chat, Query: "needle", After: at(1), Before: at(20)}
			cursor := messageKey{time: at(10).UnixMilli(), chatJID: f.phone, id: "z"}
			if _, _, _, _, _, err := searchMessageBatch(t.Context(), q, filter, tc.branch, cursor, 32, filter.Query, messageSearchRawBytes, messageSearchFoldBytes); err != nil {
				t.Fatal(err)
			}
			rows, err := q.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+q.sql, q.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(plan, "\n")
			if !strings.Contains(joined, "SEARCH messages USING INDEX "+tc.index+" (") || strings.Contains(joined, "SEARCH m USING INTEGER PRIMARY KEY") {
				t.Fatalf("expected one indexed table walk: %s", joined)
			}
		})
	}
}

func TestToolMessageSearchRejectsOversizedKey(t *testing.T) {
	f := newToolMessageFixture(t)
	if _, err := f.store.writer.ExecContext(t.Context(), `UPDATE messages SET id=? WHERE chat_jid=? AND id='direct'`, strings.Repeat("x", 2<<20), f.phone); err != nil {
		t.Fatal(err)
	}
	keys, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageKey, error) {
		return searchMessageKeys(ctx, q, MessageFilter{Query: "absent"}, Page{Limit: 50})
	})
	if !errors.Is(err, ErrResourceLimit) || len(keys) != 0 {
		t.Fatalf("oversized key: %v %v", keys, err)
	}
}

func TestToolMessageSearchFullPageBeforeLargeRemainder(t *testing.T) {
	f := newToolMessageFixture(t)
	if _, err := f.store.writer.ExecContext(t.Context(), `DELETE FROM messages`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.writer.ExecContext(t.Context(), `WITH RECURSIVE seq(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM seq WHERE i<161), field(v) AS (SELECT replace(hex(zeroblob(131072)),'0','a'))
	 INSERT INTO messages(chat_jid,id,sender_jid,time,from_me,text,text_fold,caption,caption_fold)
	 SELECT ?1,printf('large-%03d',i),?1,i,0,v,v,v,v FROM seq CROSS JOIN field`, f.phone); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.writer.ExecContext(t.Context(), `INSERT INTO messages(chat_jid,id,sender_jid,time,from_me,text,text_fold) VALUES(?1,'newest',?1,1000,0,'needle','needle')`, f.phone); err != nil {
		t.Fatal(err)
	}
	var busy, log, checkpointed int
	if err := f.store.writer.QueryRowContext(t.Context(), `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil || busy != 0 {
		t.Fatalf("checkpoint: %d %v", busy, err)
	}
	keys, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageKey, error) {
		return searchMessageKeys(ctx, q, MessageFilter{Query: "needle"}, Page{Limit: 1})
	})
	if err != nil || len(keys) != 1 || keys[0].id != "newest" {
		t.Fatalf("verified page: %v %v", keys, err)
	}
}

func TestToolMessageSearchOlderAfterLargeFields(t *testing.T) {
	f := newToolMessageFixture(t)
	if _, err := f.store.writer.ExecContext(t.Context(), `DELETE FROM messages`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.writer.ExecContext(t.Context(), `INSERT INTO messages(chat_jid,id,sender_jid,time,from_me,text,text_fold) VALUES(?1,'older',?1,1,0,'needle','needle')`, f.phone); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.writer.ExecContext(t.Context(), `WITH RECURSIVE seq(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM seq WHERE i<128), field(v) AS (SELECT replace(hex(zeroblob(131072)),'0','a'))
	 INSERT INTO messages(chat_jid,id,sender_jid,time,from_me,text,text_fold,caption,caption_fold)
	 SELECT ?1,printf('large-%03d',i),?1,i+1,0,v,v,v,v FROM seq CROSS JOIN field`, f.phone); err != nil {
		t.Fatal(err)
	}
	var busy, log, checkpointed int
	if err := f.store.writer.QueryRowContext(t.Context(), `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil || busy != 0 {
		t.Fatalf("checkpoint: %d %v", busy, err)
	}
	keys, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageKey, error) {
		return searchMessageKeys(ctx, q, MessageFilter{Query: "needle"}, Page{Limit: 50})
	})
	if err != nil || len(keys) != 1 || keys[0].id != "older" {
		t.Fatalf("older hit: %v %v", keys, err)
	}
}

func TestToolMessageSearchScale(t *testing.T) {
	f := newToolMessageFixture(t)
	if _, err := f.store.writer.ExecContext(t.Context(), `DELETE FROM messages`); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.writer.ExecContext(t.Context(), `WITH RECURSIVE seq(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM seq WHERE i<400000)
	 INSERT INTO messages(chat_jid,id,sender_jid,time,from_me,text,text_fold)
	 SELECT ?1,printf('scale-%08d',i),?1,1000000+i,0,
	 CASE WHEN i<=360000 AND i%30000=0 THEN 'sparse needle' ELSE 'ordinary chatter' END,
	 CASE WHEN i<=360000 AND i%30000=0 THEN 'sparse needle' ELSE 'ordinary chatter' END FROM seq`, f.phone)
	if err != nil {
		t.Fatal(err)
	}
	var busy, log, checkpointed int
	if err := f.store.writer.QueryRowContext(t.Context(), `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil || busy != 0 {
		t.Fatalf("checkpoint: %d %v", busy, err)
	}
	for _, query := range []string{"sparse needle", "not-present"} {
		t.Run(query, func(t *testing.T) {
			keys, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageKey, error) {
				return searchMessageKeys(ctx, q, MessageFilter{Query: query}, Page{Limit: 50})
			})
			if err != nil {
				t.Fatal(err)
			}
			if query == "not-present" {
				if len(keys) != 0 {
					t.Fatalf("unexpected keys: %d", len(keys))
				}
				return
			}
			if len(keys) != 12 {
				t.Fatalf("found %d sparse keys", len(keys))
			}
			for i, key := range keys {
				if want := fmt.Sprintf("scale-%08d", 360000-i*30000); key.id != want {
					t.Fatalf("key %d: got %s want %s", i, key.id, want)
				}
			}
		})
	}
	if _, err := f.store.writer.ExecContext(t.Context(), `INSERT INTO messages(chat_jid,id,sender_jid,time,from_me,text,text_fold) VALUES(?1,'extra-1',?1,2000001,0,'other','other'),(?1,'extra-2',?1,2000002,0,'other','other')`, f.phone); err != nil {
		t.Fatal(err)
	}
	keys, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageKey, error) {
		return searchMessageKeys(ctx, q, MessageFilter{Query: "not-present"}, Page{Limit: 50})
	})
	if !errors.Is(err, ErrResourceLimit) || len(keys) != 0 {
		t.Fatalf("unfinished search: %v %v", keys, err)
	}
}
