package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestToolMessageInteraction(t *testing.T) {
	f := newToolMessageFixture(t)
	accept := func(MessageView) (bool, error) { return true, nil }
	check := func(identity, want string) {
		t.Helper()
		got, err := f.reader.GetLastInteraction(t.Context(), identity, accept)
		if err != nil || got.ID != want {
			t.Fatalf("identity %s: got %+v, %v; want %s", identity, got, err, want)
		}
	}
	for _, identity := range []string{"491511111111", f.phone, f.alias} {
		check(identity, "group")
	}
	mustStore(t, f.store,
		msg(f.alias, "outgoing", ownJID, at(5), fromMe()),
		msg(groupOne, "unrelated", phoneJID("491522222222"), at(9)),
		msg(phoneJID("491533333333"), "wrong-direct", f.phone, at(10)),
		msg(groupOne, "wrong-author", f.phone, at(11), fromMe()),
	)
	check(f.phone, "outgoing")
	mustStore(t, f.store, msg(groupTwo, "group-new", f.phone, at(12)))
	check(f.alias, "group-new")
	mustStore(t, f.store, msg(f.phone, "tie-a", f.phone, at(12)), msg(f.phone, "tie-z", f.phone, at(12)))
	check(f.phone, "tie-z")
	mustStore(t, f.store, msg(lidJID("200000000099"), "unmapped", lidJID("200000000099"), at(13)))
	check(lidJID("200000000099"), "unmapped")
	for _, identity := range []string{testOwnPhone, ownJID, ownLID} {
		if _, err := f.reader.GetLastInteraction(t.Context(), identity, accept); !errors.Is(err, ErrNotFound) {
			t.Fatalf("self without chat: %v", err)
		}
	}
	mustStore(t, f.store, msg(ownJID, "self", ownJID, at(1), fromMe()))
	for _, identity := range []string{testOwnPhone, ownJID, ownLID} {
		check(identity, "self")
	}
	if _, err := f.reader.GetLastInteraction(t.Context(), "491599999999", accept); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no history: %v", err)
	}
	if _, err := f.reader.GetLastInteraction(t.Context(), f.phone, func(MessageView) (bool, error) { return false, nil }); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("rejected projection: %v", err)
	}
	if _, err := f.reader.GetLastInteraction(t.Context(), "invalid", accept); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid identity: %v", err)
	}
}

func TestToolMessageInteractionUnverified(t *testing.T) {
	f := newToolMessageFixture(t)
	mustStore(t, f.store, msg(f.phone, "direct-new", ownJID, at(4), fromMe()),
		msg(phoneJID("491533333333"), "irrelevant", f.phone, at(8)))
	_, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) (messageKey, error) {
		ids, err := resolveContact(ctx, q, f.phone)
		if err != nil {
			return messageKey{}, err
		}
		return interactionKey(ctx, q, ids, false, 2)
	})
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Limit != "interaction work" {
		t.Fatalf("unfinished stream: %v", err)
	}
}

func TestToolMessageInteractionSnapshotAndPolicies(t *testing.T) {
	f := newToolMessageFixture(t)
	wantErr := ErrInvalid
	got, err := f.reader.GetLastInteraction(t.Context(), f.phone, func(MessageView) (bool, error) { return false, wantErr })
	if !errors.Is(err, wantErr) || !reflect.DeepEqual(got, MessageView{}) {
		t.Fatalf("policy error: %+v %v", got, err)
	}
	if _, err := f.reader.GetLastInteraction(t.Context(), f.phone, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil policy: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.reader.GetLastInteraction(ctx, f.phone, func(MessageView) (bool, error) { return true, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	key, err := messageSnapshot(t.Context(), f.reader, func(ctx context.Context, q querier) (messageKey, error) {
		ids, err := resolveContact(ctx, q, f.phone)
		if err != nil {
			return messageKey{}, err
		}
		if _, err := f.store.writer.ExecContext(ctx, `DELETE FROM whatsmeow_lid_map WHERE lid = ?`, "200000000001"); err != nil {
			return messageKey{}, err
		}
		ids, err = resolveContact(ctx, q, f.phone)
		if err != nil {
			return messageKey{}, err
		}
		return interactionKey(ctx, q, ids, false, messageSearchVisits)
	})
	if err != nil || key.id != "group" {
		t.Fatalf("snapshot lookup: %+v %v", key, err)
	}
}

func TestToolMessageInteractionSeekPlans(t *testing.T) {
	f := newToolMessageFixture(t)
	for _, tc := range []struct {
		query, index string
		args         []any
	}{
		{`EXPLAIN QUERY PLAN SELECT time, id FROM messages INDEXED BY messages_chat_time WHERE chat_jid=? AND (time,id)<(?,?) ORDER BY time DESC,id DESC LIMIT 1`, "messages_chat_time", []any{f.phone, int64(9), "z"}},
		{`EXPLAIN QUERY PLAN SELECT time,chat_jid,id FROM messages INDEXED BY messages_sender_time WHERE sender_phone=? AND (time,chat_jid,id)<(?,?,?) ORDER BY time DESC,chat_jid DESC,id DESC LIMIT 256`, "messages_sender_time", []any{"491511111111", int64(9), "z", "z"}},
		{`EXPLAIN QUERY PLAN SELECT time,chat_jid,id FROM messages INDEXED BY messages_sender_jid WHERE sender_jid=? AND (time,chat_jid,id)<(?,?,?) ORDER BY time DESC,chat_jid DESC,id DESC LIMIT 256`, "messages_sender_jid", []any{f.alias, int64(9), "z", "z"}},
	} {
		rows, err := f.store.reader.QueryContext(t.Context(), tc.query, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		plan := strings.Join(details, " ")
		if !strings.Contains(plan, "SEARCH") || !strings.Contains(plan, tc.index) || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("unexpected plan: %s", plan)
		}
	}
}
