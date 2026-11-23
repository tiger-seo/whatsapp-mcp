package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestToolMessageContextSelection(t *testing.T) {
	f := newToolMessageFixture(t)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		mustStore(t, f.store, msg(f.phone, id, f.phone, at(10+i)))
	}
	var touched int
	got, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageContextHit, error) {
		var hits []messageContextHit
		var err error
		hits, touched, err = messageContext(ctx, q, []messageKey{{time: at(12).UnixMilli(), chatJID: f.phone, id: "c"}}, Context{Before: 2, After: 2}, &resultBudget{}, func(MessageView) (bool, error) { return true, nil })
		return hits, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if touched != 8 || len(got) != 1 || len(got[0].before.rows) != 2 || len(got[0].after.rows) != 2 ||
		got[0].before.rows[0].ID != "a" || got[0].before.rows[1].ID != "b" || got[0].after.rows[0].ID != "d" || got[0].after.rows[1].ID != "e" || got[0].before.reduced || got[0].after.reduced {
		t.Fatalf("context: %#v touched=%d", got, touched)
	}
}

func TestToolMessageExactFacade(t *testing.T) {
	f := newToolMessageFixture(t)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		mustStore(t, f.store, msg(f.phone, id, f.phone, at(10+i)))
	}
	mustStore(t, f.store, msg(groupOne, "c", f.alias, at(12)))
	var seen []string
	got, err := f.reader.GetMessageExact(t.Context(), f.phone, "c", Context{Before: 2, After: 2}, func(v MessageView) (bool, error) {
		seen = append(seen, v.ID)
		return v.ID != "a", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []string{"c", "b", "d", "a", "e"}) || got.Message.ChatJID != f.phone ||
		len(got.Before) != 1 || got.Before[0].ID != "b" || len(got.After) != 2 || got.After[0].ID != "d" || got.After[1].ID != "e" || !got.BeforeReduced || got.AfterReduced {
		t.Fatalf("result=%+v seen=%v", got, seen)
	}
	empty, err := f.reader.GetMessageExact(t.Context(), f.phone, "direct", Context{Before: 2}, func(MessageView) (bool, error) { return true, nil })
	if err != nil || empty.Before == nil || empty.After == nil || len(empty.Before) != 0 || empty.BeforeReduced || empty.AfterReduced {
		t.Fatalf("empty context: %+v, %v", empty, err)
	}
	if f.store.gate.activeCount() != 0 || f.store.reader.Stats().InUse != 0 {
		t.Fatal("facade returned before resource release")
	}
}

func TestToolMessageExactFacadeErrors(t *testing.T) {
	f := newToolMessageFixture(t)
	mustStore(t, f.store, msg(f.phone, "later", f.phone, at(3)))
	accept := func(MessageView) (bool, error) { return true, nil }
	for _, tc := range []struct {
		name, chat, id string
		counts         Context
		policy         func(MessageView) (bool, error)
		want           error
	}{
		{"chat", "invalid", "direct", Context{}, accept, ErrInvalid},
		{"id", f.phone, "", Context{}, accept, ErrInvalid},
		{"context", f.phone, "direct", Context{After: 21}, accept, ErrInvalid},
		{"policy", f.phone, "direct", Context{}, nil, ErrInvalid},
		{"missing", f.phone, "missing", Context{}, accept, ErrNotFound},
		{"anchor rejected", f.phone, "direct", Context{}, func(MessageView) (bool, error) { return false, nil }, ErrResourceLimit},
		{"policy error", f.phone, "direct", Context{}, func(MessageView) (bool, error) { return false, ErrInvalid }, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.reader.GetMessageExact(t.Context(), tc.chat, tc.id, tc.counts, tc.policy)
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, MessageContextResult{}) {
				t.Fatalf("got=%+v err=%v want=%v", got, err, tc.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := f.reader.GetMessageExact(ctx, f.phone, "direct", Context{}, accept)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, MessageContextResult{}) {
		t.Fatalf("cancelled: %+v %v", got, err)
	}
	got, err = f.reader.GetMessageExact(t.Context(), f.phone, "direct", Context{After: 1}, func(v MessageView) (bool, error) {
		if v.ID == "later" {
			return false, ErrInvalid
		}
		return true, nil
	})
	if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(got, MessageContextResult{}) {
		t.Fatalf("neighbour policy error: %+v %v", got, err)
	}
	// Validation must win even when the store cannot admit another read.
	release, _, err := f.store.enterTool(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = f.reader.GetMessageExact(t.Context(), "invalid", "direct", Context{}, accept)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("validation after admission: %v", err)
	}
}

func TestToolMessageContextRejectedProjection(t *testing.T) {
	f := newToolMessageFixture(t)
	mustStore(t, f.store, msg(f.phone, "later", f.phone, at(3)))
	got, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageContextHit, error) {
		hits, _, err := messageContext(ctx, q, []messageKey{{time: at(1).UnixMilli(), chatJID: f.phone, id: "direct"}}, Context{Before: 1, After: 2}, &resultBudget{}, func(MessageView) (bool, error) { return false, nil })
		return hits, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].before.reduced || !got[0].after.reduced || len(got[0].after.rows) != 0 {
		t.Fatalf("reduction: %#v", got)
	}
}

func TestToolMessageContextRowCap(t *testing.T) {
	f := newToolMessageFixture(t)
	for i := 0; i < 45; i++ {
		mustStore(t, f.store, msg(f.phone, fmt.Sprintf("row-%02d", i), f.phone, at(10+i)))
	}
	anchors := make([]messageKey, 200)
	for i := range anchors {
		anchors[i] = messageKey{time: at(32).UnixMilli(), chatJID: f.phone, id: "row-22"}
	}
	var touched int
	hits, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageContextHit, error) {
		var hits []messageContextHit
		var err error
		hits, touched, err = messageContext(ctx, q, anchors, Context{Before: 20, After: 20}, &resultBudget{}, func(MessageView) (bool, error) { return true, nil })
		return hits, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if touched != maxContextRows {
		t.Fatalf("row touches: %d", touched)
	}
	if hits[0].before.reduced || hits[0].after.reduced || !hits[len(hits)-1].before.reduced || !hits[len(hits)-1].after.reduced {
		t.Fatalf("incorrect row cap flags: first=%#v last=%#v", hits[0], hits[len(hits)-1])
	}
}

func TestToolMessageSnapshotCancellationRetainsLease(t *testing.T) {
	f := newToolMessageFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := messageSnapshot(ctx, f.reader, func(ctx context.Context, q querier) (int, error) {
			var n int
			if err := q.QueryRowContext(ctx, `SELECT count(*) FROM messages`).Scan(&n); err != nil {
				return 0, err
			}
			close(entered)
			<-release
			return n, nil
		})
		done <- err
	}()
	<-entered
	cancel()
	if f.store.gate.activeCount() != 1 {
		t.Error("cancelled snapshot lost its lease")
	}
	if _, err := f.reader.GetChat(t.Context(), f.phone, false); !errors.Is(err, ErrBusy) {
		t.Errorf("concurrent tool: %v", err)
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled snapshot: %v", err)
	}
	if f.store.gate.activeCount() != 0 || f.store.reader.Stats().InUse != 0 {
		t.Error("snapshot did not return its resources")
	}
}

func TestToolMessageProjectionValidation(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"oversize", "text", strings.Repeat("x", maxTextBytes+1)},
		{"utf8", "caption", string([]byte{0xff})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newToolMessageFixture(t)
			if _, err := f.store.writer.ExecContext(t.Context(), `UPDATE messages SET `+tc.field+` = ? WHERE chat_jid = ? AND id = ?`, tc.value, f.phone, "direct"); err != nil {
				t.Fatal(err)
			}
			_, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) (MessageView, error) {
				return messageProjection(ctx, q, messageKey{chatJID: f.phone, id: "direct"}, &resultBudget{})
			})
			if !errors.Is(err, ErrResourceLimit) {
				t.Fatalf("invalid field: %v", err)
			}
		})
	}
}

func TestToolMessageProjectionRemainingBudget(t *testing.T) {
	f := newToolMessageFixture(t)
	key := messageKey{chatJID: f.phone, id: "direct"}
	var charged int
	_, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) (MessageView, error) {
		budget := &resultBudget{}
		v, err := messageProjection(ctx, q, key, budget)
		charged = budget.used
		return v, err
	})
	if err != nil || charged == 0 {
		t.Fatalf("baseline projection: bytes=%d err=%v", charged, err)
	}
	for _, excess := range []int{0, 1} {
		budget := &resultBudget{used: maxResultBytes - charged + excess}
		before := budget.used
		_, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) (MessageView, error) {
			return messageProjection(ctx, q, key, budget)
		})
		if excess == 0 && (err != nil || budget.used != maxResultBytes) {
			t.Fatalf("exact remaining budget: used=%d err=%v", budget.used, err)
		}
		if excess == 1 && (!errors.Is(err, ErrResourceLimit) || budget.used != before) {
			t.Fatalf("over remaining budget: used=%d err=%v", budget.used, err)
		}
	}
}

func TestToolMessageContextMaterializationBudget(t *testing.T) {
	f := newToolMessageFixture(t)
	mustStore(t, f.store, msg(f.phone, "later", f.phone, at(3)))
	var touches int
	hits, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) ([]messageContextHit, error) {
		var result []messageContextHit
		var err error
		result, touches, err = messageContext(ctx, q, []messageKey{{chatJID: f.phone, id: "direct", time: at(1).UnixMilli()}}, Context{Before: 1, After: 1}, &resultBudget{used: maxResultBytes}, func(MessageView) (bool, error) { return true, nil })
		return result, err
	})
	if err != nil || len(hits) != 1 || hits[0].before.reduced || !hits[0].after.reduced || len(hits[0].after.rows) != 0 || touches != 2 {
		t.Fatalf("budget reduction: hits=%#v touches=%d err=%v", hits, touches, err)
	}
}

func TestToolMessageContextValidationAndReentry(t *testing.T) {
	f := newToolMessageFixture(t)
	mustStore(t, f.store, msg(f.phone, "later", f.phone, at(3)))
	_, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) (int, error) {
		anchor := messageKey{time: at(1).UnixMilli(), chatJID: f.phone, id: "direct"}
		_, _, err := messageContext(ctx, q, []messageKey{anchor}, Context{After: 1}, &resultBudget{}, func(MessageView) (bool, error) {
			if _, err := f.reader.GetChat(ctx, f.phone, false); !errors.Is(err, ErrBusy) {
				t.Errorf("reentrant reader: %v", err)
			}
			return true, nil
		})
		return 0, err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, anchor := range []messageKey{
		{chatJID: "invalid", id: "id"},
		{chatJID: f.phone, id: "bad\x00id"},
	} {
		_, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) (int, error) {
			_, touched, err := messageContext(ctx, q, []messageKey{anchor}, Context{After: 1}, &resultBudget{}, func(MessageView) (bool, error) { return true, nil })
			return touched, err
		})
		if !errors.Is(err, ErrResourceLimit) {
			t.Errorf("invalid anchor: %v", err)
		}
	}
}

func TestToolMessageSnapshotReadOnly(t *testing.T) {
	f := newToolMessageFixture(t)
	_, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) (int, error) {
		var value int
		err := q.QueryRowContext(ctx, `UPDATE messages SET text = 'changed' RETURNING 1`).Scan(&value)
		return value, err
	})
	if err == nil {
		t.Fatal("write succeeded on message snapshot")
	}
}

type toolMessageFixture struct {
	store        *Store
	reader       *ToolReader
	phone, alias string
}

func newToolMessageFixture(t *testing.T) toolMessageFixture {
	t.Helper()
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	dev := seedDevice(t, st)
	const phone, lid = "491511111111", "200000000001"
	seedLIDMapping(t, dev, lid, phone)
	seedContacts(t, dev, contactSeed{jid: phoneJID(phone), full: "Synthetic contact"})
	mustStore(t, st,
		msg(phoneJID(phone), "direct", phoneJID(phone), at(1), withText("synthetic text"), withCaption("synthetic caption")),
		msg(groupOne, "group", lidJID(lid), at(2)),
	)
	return toolMessageFixture{store: st, reader: st.ToolReader(), phone: phoneJID(phone), alias: lidJID(lid)}
}

func TestToolMessageFixtureSnapshotAndAliases(t *testing.T) {
	f := newToolMessageFixture(t)
	ctx := context.Background()
	conn, err := f.store.reader.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM messages`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("initial snapshot: count=%d err=%v", count, err)
	}
	mustStore(t, f.store, msg(f.phone, "later", f.phone, at(3)))
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM messages`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("snapshot changed after write: count=%d err=%v", count, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	chat, err := f.reader.DirectChat(ctx, f.alias)
	if err != nil || chat.JID != f.phone {
		t.Fatalf("local alias resolution: chat=%#v err=%v", chat, err)
	}
}

func TestToolMessageFixtureFieldBoundaries(t *testing.T) {
	f := newToolMessageFixture(t)
	ctx := context.Background()
	for _, size := range []int{maxTextBytes, maxTextBytes + 1} {
		if _, err := f.store.writer.ExecContext(ctx, `UPDATE messages SET text = ? WHERE chat_jid = ? AND id = ?`, strings.Repeat("x", size), f.phone, "direct"); err != nil {
			t.Fatal(err)
		}
		var got int
		if err := f.store.reader.QueryRowContext(ctx, `SELECT octet_length(text) FROM messages WHERE chat_jid = ? AND id = ?`, f.phone, "direct").Scan(&got); err != nil || got != size {
			t.Fatalf("synthetic field length: got=%d want=%d err=%v", got, size, err)
		}
	}
}

func TestToolMessageReaderHasNoAccountOrWriteMethods(t *testing.T) {
	typ := reflect.TypeFor[*ToolReader]()
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		for _, forbidden := range []string{"Write", "Store", "Send", "Connect", "Pair", "Device"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("unexpected tool reader capability: %s", name)
			}
		}
	}
}
