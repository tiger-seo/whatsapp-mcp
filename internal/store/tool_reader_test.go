package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.mau.fi/whatsmeow/types"
	"modernc.org/sqlite"
)

var testContactFoldCalls atomic.Int64
var registerTestContactFold sync.Once
var registerTestContactFoldErr error
var testContactFoldCancel context.CancelFunc
var registerTestSQLiteStep sync.Once
var registerTestSQLiteStepErr error
var testSQLiteStepEntered, testSQLiteStepRelease chan struct{}

type countingFoldQuerier struct{ querier }

type blockingStepQuerier struct{ querier }

func (q blockingStepQuerier) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(query, "last_message_time FROM chats WHERE jid = ?") {
		query += " AND test_sqlite_step() = 1"
	}
	return q.querier.QueryRowContext(ctx, query, args...)
}

func TestToolSQLiteCloseCancellationRaces(t *testing.T) {
	registerTestSQLiteStep.Do(func() {
		registerTestSQLiteStepErr = sqlite.RegisterScalarFunction("test_sqlite_step", 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
			close(testSQLiteStepEntered)
			<-testSQLiteStepRelease
			return int64(1), nil
		})
	})
	if registerTestSQLiteStepErr != nil {
		t.Fatal(registerTestSQLiteStepErr)
	}
	for i := 0; i < 50; i++ {
		t.Run(fmt.Sprintf("iteration-%02d", i), func(t *testing.T) {
			st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
				free: func(string) (uint64, error) { return 2 * gib, nil },
			}})
			jid := phoneJID("491511111111")
			nameChat(t, st, jid, "name", NameSaved)
			st.hooks.wrapReader = func(q querier) querier { return blockingStepQuerier{q} }
			testSQLiteStepEntered, testSQLiteStepRelease = make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() { _, err := st.ToolReader().GetChat(context.Background(), jid, false); done <- err }()
			select {
			case <-testSQLiteStepEntered:
			case <-time.After(5 * time.Second):
				close(testSQLiteStepRelease)
				<-done
				t.Fatal("SQLite did not enter the test callback")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := st.gate.quiesce(ctx); !errors.Is(err, ErrNotQuiescent) {
				t.Errorf("close during SQLite step: %v", err)
			}
			if st.gate.activeCount() != 1 {
				t.Error("SQLite step lost gate accounting")
			}
			close(testSQLiteStepRelease)
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("cancelled SQLite step: %v", err)
			}
			if err := st.gate.quiesce(context.Background()); err != nil || st.gate.activeCount() != 0 {
				t.Errorf("drain after SQLite step: %v", err)
			}
		})
	}
}

func (q countingFoldQuerier) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return q.querier.QueryRowContext(ctx, strings.ReplaceAll(query, "mcp_fold(", "test_contact_fold("), args...)
}

func registerContactFold(t *testing.T) {
	t.Helper()
	registerTestContactFold.Do(func() {
		registerTestContactFoldErr = sqlite.RegisterDeterministicScalarFunction("test_contact_fold", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			testContactFoldCalls.Add(1)
			if testContactFoldCancel != nil {
				testContactFoldCancel()
			}
			switch v := args[0].(type) {
			case string:
				return fold(v), nil
			case []byte:
				return fold(string(v)), nil
			default:
				return v, nil
			}
		})
	})
	if registerTestContactFoldErr != nil {
		t.Fatal(registerTestContactFoldErr)
	}
}

func TestToolContactCeilingsZeroFolds(t *testing.T) {
	registerContactFold(t)
	for _, tc := range []struct {
		name  string
		n     int
		full  string
		other string
	}{
		{"entries", 100001, "", ""},
		{"chosen-bytes", 4097, strings.Repeat("x", 1024), ""},
		{"all-name-bytes", 4097, strings.Repeat("x", 1024), strings.Repeat("y", 1024)},
		{"field-bytes", 1, strings.Repeat("x", 1025), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := openTest(t, newDataDir(t))
			dev := seedDevice(t, st)
			_, err := st.writer.ExecContext(context.Background(), `WITH RECURSIVE n(x) AS
				(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x < ?)
				INSERT INTO whatsmeow_contacts(our_jid, their_jid, full_name, first_name, push_name, business_name)
				SELECT ?, printf('%012d@s.whatsapp.net', x), ?, ?, ?, ? FROM n`,
				tc.n, dev.ID.String(), tc.full, tc.other, tc.other, tc.other)
			if err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "chosen-bytes":
				_, err = st.writer.ExecContext(context.Background(), `UPDATE whatsmeow_contacts SET full_name = 'x' WHERE their_jid = '000000004097@s.whatsapp.net'`)
			case "all-name-bytes":
				_, err = st.writer.ExecContext(context.Background(), `UPDATE whatsmeow_contacts SET full_name = CASE their_jid
					WHEN '000000000001@s.whatsapp.net' THEN ? ELSE 'x' END,
					first_name = CASE WHEN their_jid = '000000004097@s.whatsapp.net' THEN 'x' ELSE first_name END,
					push_name = CASE WHEN their_jid = '000000004097@s.whatsapp.net' THEN '' ELSE push_name END,
					business_name = CASE WHEN their_jid = '000000004097@s.whatsapp.net' THEN '' ELSE business_name END
					WHERE their_jid IN ('000000000001@s.whatsapp.net', '000000004097@s.whatsapp.net')`, strings.Repeat("x", 1023))
			}
			if err != nil {
				t.Fatal(err)
			}
			measure := func() int64 {
				t.Helper()
				var n, chosen, all, longest int64
				err := st.writer.QueryRowContext(context.Background(), `SELECT count(*),
					coalesce(sum(coalesce(nullif(octet_length(full_name), 0), nullif(octet_length(first_name), 0),
						nullif(octet_length(push_name), 0), nullif(octet_length(business_name), 0), 0)), 0),
					coalesce(sum(coalesce(octet_length(full_name), 0) + coalesce(octet_length(first_name), 0) +
						coalesce(octet_length(push_name), 0) + coalesce(octet_length(business_name), 0)), 0),
					coalesce(max(max(coalesce(octet_length(full_name), 0), coalesce(octet_length(first_name), 0),
						coalesce(octet_length(push_name), 0), coalesce(octet_length(business_name), 0))), 0)
					FROM whatsmeow_contacts`).Scan(&n, &chosen, &all, &longest)
				if err != nil {
					t.Fatal(err)
				}
				switch tc.name {
				case "entries":
					return n
				case "chosen-bytes":
					return chosen
				case "all-name-bytes":
					return all
				default:
					return longest
				}
			}
			limit := map[string]int64{"entries": 100000, "chosen-bytes": 4 * mib, "all-name-bytes": 16 * mib, "field-bytes": 1024}[tc.name]
			if got := measure(); got != limit+1 {
				t.Fatalf("fixture over ceiling: got %d, want %d", got, limit+1)
			}
			st.hooks.wrapReader = func(q querier) querier { return countingFoldQuerier{q} }
			testContactFoldCalls.Store(0)
			_, err = st.SearchContacts(context.Background(), "x", Page{Limit: 1})
			if !errors.Is(err, ErrResourceLimit) || testContactFoldCalls.Load() != 0 {
				t.Fatalf("ceiling: err=%v, fold calls=%d", err, testContactFoldCalls.Load())
			}
			var query string
			var args []any
			switch tc.name {
			case "entries":
				query = `DELETE FROM whatsmeow_contacts WHERE their_jid = '000000100001@s.whatsapp.net'`
			case "chosen-bytes":
				query = `DELETE FROM whatsmeow_contacts WHERE their_jid = '000000004097@s.whatsapp.net'`
			case "all-name-bytes":
				query = `UPDATE whatsmeow_contacts SET first_name = '' WHERE their_jid = '000000004097@s.whatsapp.net'`
			case "field-bytes":
				query, args = `UPDATE whatsmeow_contacts SET full_name = ?`, []any{strings.Repeat("x", 1024)}
			}
			if _, err := st.writer.ExecContext(context.Background(), query, args...); err != nil {
				t.Fatal(err)
			}
			if got := measure(); got != limit {
				t.Fatalf("fixture at ceiling: got %d, want %d", got, limit)
			}
			if _, err := st.SearchContacts(context.Background(), "x", Page{Limit: 1}); err != nil || testContactFoldCalls.Load() == 0 {
				t.Fatalf("at %s ceiling: err=%v, fold calls=%d", tc.name, err, testContactFoldCalls.Load())
			}
			if tc.name == "chosen-bytes" {
				const fallback = "000000004097@s.whatsapp.net"
				if _, err := st.writer.Exec(`INSERT INTO whatsmeow_contacts (our_jid, their_jid) VALUES (?, ?)`, dev.ID.String(), fallback); err != nil {
					t.Fatal(err)
				}
				for _, excess := range []int{1, 0} {
					if _, err := st.writer.Exec(`UPDATE whatsmeow_contacts SET full_name = ? WHERE their_jid = '000000000001@s.whatsapp.net'`, strings.Repeat("x", 1024-len(fallback)+excess)); err != nil {
						t.Fatal(err)
					}
					testContactFoldCalls.Store(0)
					_, err := st.SearchContacts(t.Context(), "x", Page{Limit: 1})
					folded := testContactFoldCalls.Load()
					if excess == 1 && (!errors.Is(err, ErrResourceLimit) || folded != 0) || excess == 0 && (err != nil || folded == 0) {
						t.Fatalf("chosen bytes with identity fallback, excess %d: err=%v, fold calls=%d", excess, err, folded)
					}
				}
			}
		})
	}
}

func TestToolFirstStepCancellationDuringContactFolding(t *testing.T) {
	registerContactFold(t)
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	dev := seedDevice(t, st)
	if _, err := st.writer.ExecContext(context.Background(), `WITH RECURSIVE n(x) AS
		(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x < 10000)
		INSERT INTO whatsmeow_contacts(our_jid, their_jid, full_name)
		SELECT ?, printf('%012d@s.whatsapp.net', x), 'name' FROM n`, dev.ID.String()); err != nil {
		t.Fatal(err)
	}
	st.hooks.wrapReader = func(q querier) querier { return countingFoldQuerier{q} }
	testContactFoldCalls.Store(0)
	if _, err := st.ToolReader().SearchContacts(context.Background(), "name", Page{Limit: 200}); err != nil {
		t.Fatal(err)
	}
	completeFolds := testContactFoldCalls.Load()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	testContactFoldCancel = cancel
	defer func() { testContactFoldCancel = nil }()
	testContactFoldCalls.Store(0)
	if _, err := st.ToolReader().SearchContacts(ctx, "name", Page{Limit: 200}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation from inside first SQLite step: %v", err)
	}
	if n := testContactFoldCalls.Load(); n == 0 || n >= completeFolds {
		t.Fatalf("SQLite did not interrupt the grouping work: %d folds, full read uses %d", n, completeFolds)
	}
	if st.gate.activeCount() != 0 || st.reader.Stats().InUse != 0 {
		t.Fatal("cancelled aggregate retained a lease or reader")
	}
	testContactFoldCancel = nil
	if _, err := st.ToolReader().SearchContacts(context.Background(), "name", Page{Limit: 1}); err != nil {
		t.Fatalf("readmission after actual connection return: %v", err)
	}
}

func TestToolContactIdentitySourceCeiling(t *testing.T) {
	registerContactFold(t)
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	if _, err := st.writer.ExecContext(context.Background(),
		`INSERT INTO whatsmeow_contacts(our_jid, their_jid, full_name) VALUES (?, ?, 'name')`,
		dev.ID.String(), strings.Repeat("1", maxJIDBytes)+"@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	st.hooks.wrapReader = func(q querier) querier { return countingFoldQuerier{q} }
	testContactFoldCalls.Store(0)
	if _, err := st.SearchContacts(context.Background(), "name", Page{Limit: 1}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized contact identity: %v", err)
	}
	if n := testContactFoldCalls.Load(); n != 0 {
		t.Fatalf("oversized contact identity reached folding: %d calls", n)
	}
}

func TestToolMappedIdentitySourceCeiling(t *testing.T) {
	registerContactFold(t)
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	ctx := context.Background()
	const lid = "200000000001"
	const pn = "491511111111"
	seedContacts(t, dev, contactSeed{jid: lidJID(lid), full: "Mapped"})
	seedLIDMapping(t, dev, lid, pn)
	st.hooks.wrapReader = func(q querier) querier { return countingFoldQuerier{q} }
	r := st.ToolReader()
	p := Page{Limit: 1}
	for _, tc := range []struct {
		name, sql, match, identity string
		search                     bool
	}{
		{"phone", `UPDATE whatsmeow_lid_map SET pn = ? WHERE lid = ?`, lid, lidJID(lid), true},
		{"lid", `UPDATE whatsmeow_lid_map SET lid = ? WHERE pn = ?`, pn, phoneJID(pn), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "lid" {
				if _, err := st.writer.ExecContext(ctx, `UPDATE whatsmeow_lid_map SET pn = ? WHERE lid = ?`, pn, lid); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := st.writer.ExecContext(ctx, tc.sql, strings.Repeat("1", maxJIDBytes), tc.match); err != nil {
				t.Fatal(err)
			}
			if _, err := r.DirectChat(ctx, tc.identity); !errors.Is(err, ErrResourceLimit) {
				t.Errorf("direct chat: %v, want resource limit", err)
			}
			if _, err := r.ContactChats(ctx, tc.identity, p); !errors.Is(err, ErrResourceLimit) {
				t.Errorf("contact chats: %v, want resource limit", err)
			}
			if tc.search {
				testContactFoldCalls.Store(0)
				if _, err := r.SearchContacts(ctx, "mapped", p); !errors.Is(err, ErrResourceLimit) {
					t.Errorf("contact search: %v, want resource limit", err)
				}
				if got := testContactFoldCalls.Load(); got != 0 {
					t.Errorf("contact search folded %d values before refusing", got)
				}
			}
		})
	}
}

func TestToolContactSourceFieldCeiling(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	jid := phoneJID("491511111111")
	if err := dev.Contacts.PutContactName(context.Background(), types.NewJID("491511111111", types.DefaultUserServer), "", "short"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.writer.ExecContext(context.Background(),
		`UPDATE whatsmeow_contacts SET full_name = ? WHERE their_jid = ?`, strings.Repeat("x", maxNameBytes+1), jid); err != nil {
		t.Fatal(err)
	}
	_, err := st.SearchContacts(context.Background(), "", Page{Limit: 1})
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized contact name: got %v, want resource limit", err)
	}
}

func TestToolContactAggregateLegacyValues(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	jid := types.NewJID("491511111112", types.DefaultUserServer)
	if err := dev.Contacts.PutContactName(context.Background(), jid, "", "initial"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"control", "a\x00\x01b", "a\x00\x01b"},
		{"integer", int64(1234), "1234"},
		{"real", 1.5, "1.5"},
		{"null", nil, jid.String()},
		{"blob", []byte("legacy"), "legacy"},
		{"invalid-utf8-blob", []byte{'a', 0xff, 'b'}, string([]byte{'a', 0xff, 'b'})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.writer.ExecContext(context.Background(),
				`UPDATE whatsmeow_contacts SET full_name = ? WHERE their_jid = ?`, tc.value, jid.String()); err != nil {
				t.Fatal(err)
			}
			got, err := st.SearchContacts(context.Background(), "", Page{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Name != tc.want {
				t.Fatalf("got %#v, want name %q", got, tc.want)
			}
		})
	}
}

func TestToolContactAggregatePlan(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	jid := types.NewJID("491511111112", types.DefaultUserServer)
	if err := dev.Contacts.PutContactName(context.Background(), jid, "", "name"); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	st.hooks.wrapReader = func(q querier) querier { rec.q = q; return rec }
	if _, err := st.SearchContacts(context.Background(), "name", Page{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if len(rec.queries) != 1 || !strings.Contains(rec.queries[0].sql, "json_group_array") {
		t.Fatalf("contact search is not a single aggregate: %#v", rec.queries)
	}
	plan := strings.Join(planOf(t, st, rec.queries[0]), "\n")
	if !strings.Contains(plan, "USE TEMP B-TREE FOR GROUP BY") ||
		!strings.Contains(plan, "USE TEMP B-TREE FOR json_group_array(ORDER BY)") {
		t.Fatalf("unexpected contact plan:\n%s", plan)
	}
}

func TestToolGateRepeatedReaderCancellation(t *testing.T) {
	g := newGate()
	for iteration := 0; iteration < 50; iteration++ {
		release, toolCtx, err := enterTestTool(g)
		if err != nil {
			t.Fatalf("iteration %d tool admission: %v", iteration, err)
		}
		readers := make([]func(), 0, readConns)
		for i := 0; i < readConns; i++ {
			r, _, err := g.enterRead(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			readers = append(readers, r)
		}
		if !errors.Is(toolCtx.Err(), context.Canceled) {
			t.Fatalf("iteration %d: tool not cancelled", iteration)
		}
		if _, _, err := enterTestTool(g); !errors.Is(err, ErrBusy) {
			t.Fatalf("iteration %d: replacement: %v", iteration, err)
		}
		for _, r := range readers {
			r()
		}
		release()
		if g.activeCount() != 0 {
			t.Fatalf("iteration %d: active accounting leaked", iteration)
		}
	}
}

func TestToolGateCloseAdmissionRace(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		g := newGate()
		start := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			<-start
			release, _, err := enterTestTool(g)
			if err == nil {
				release()
			}
			result <- err
		}()
		close(start)
		if err := g.quiesce(context.Background()); err != nil {
			t.Fatalf("iteration %d close: %v", iteration, err)
		}
		if err := <-result; err != nil && !errors.Is(err, ErrClosed) {
			t.Fatalf("iteration %d admission: %v", iteration, err)
		}
		if g.activeCount() != 0 || !g.isQuiesced() {
			t.Fatalf("iteration %d: close returned with active work", iteration)
		}
		if _, _, err := enterTestTool(g); !errors.Is(err, ErrClosed) {
			t.Fatalf("iteration %d: admission after close: %v", iteration, err)
		}
	}
}

func TestToolLatestMessageSourceCeiling(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	jid := phoneJID("491511111111")
	mustStore(t, st, msg(jid, "one", jid, at(1)))
	if _, err := st.writer.ExecContext(context.Background(),
		"UPDATE messages SET text = ? WHERE chat_jid = ?", strings.Repeat("x", maxTextBytes+1), jid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListChats(context.Background(), "", ByLastActivity, false, Page{Limit: 1}); err != nil {
		t.Fatalf("metadata-only read: %v", err)
	}
	if _, err := st.ListChats(context.Background(), "", ByLastActivity, true, Page{Limit: 1}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized latest message: got %v, want resource limit", err)
	}
}

func TestToolLatestIdentitySourceCeilings(t *testing.T) {
	for _, field := range []string{"id", "sender_jid", "sender_phone"} {
		t.Run(field, func(t *testing.T) {
			st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
				free: func(string) (uint64, error) { return 2 * gib, nil },
			}})
			jid := phoneJID("491511111111")
			mustStore(t, st, msg(jid, "one", jid, at(1)))
			for _, size := range []int{maxJIDBytes, maxJIDBytes + 1} {
				if _, err := st.writer.ExecContext(context.Background(), `UPDATE messages SET `+field+` = ?`, strings.Repeat("x", size)); err != nil {
					t.Fatal(err)
				}
				if _, err := st.ToolReader().GetChat(context.Background(), jid, false); err != nil {
					t.Fatalf("metadata-only lookup with %d-byte %s: %v", size, field, err)
				}
				_, err := st.ToolReader().GetChat(context.Background(), jid, true)
				if size == maxJIDBytes && err != nil {
					t.Fatalf("identity at ceiling: %v", err)
				}
				if size > maxJIDBytes && !errors.Is(err, ErrResourceLimit) {
					t.Fatalf("identity over ceiling: %v, want resource limit", err)
				}
			}
		})
	}
}

func TestToolListChatIdentitySourceCeiling(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	jid := phoneJID("491511111111")
	nameChat(t, st, jid, "name", NameSaved)
	if _, err := st.writer.ExecContext(context.Background(), `UPDATE chats SET jid = ?`, strings.Repeat("x", maxJIDBytes+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListChats(context.Background(), "", ByLastActivity, false, Page{Limit: 1}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized chat identity: %v, want resource limit", err)
	}
}

func TestToolGetChatMetadataOnly(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	jid := phoneJID("491511111111")
	mustStore(t, st, msg(jid, "one", jid, at(1)))
	if _, err := st.writer.ExecContext(context.Background(),
		"UPDATE messages SET text = ? WHERE chat_jid = ?", strings.Repeat("x", maxTextBytes+1), jid); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	st.hooks.wrapReader = func(q querier) querier { rec.q = q; return rec }
	got, err := st.GetChat(context.Background(), jid, false)
	if err != nil || got.JID != jid || got.Latest != nil {
		t.Fatalf("metadata-only read: got %#v, %v", got, err)
	}
	if len(rec.queries) != 1 || strings.Contains(strings.ToLower(rec.queries[0].sql), "messages") {
		t.Fatalf("metadata-only statements: %#v", rec.queries)
	}
	if _, err := st.GetChat(context.Background(), jid, true); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("latest read: got %v, want resource limit", err)
	}
}

func TestToolGetChatSourceNameCeiling(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	jid := phoneJID("491511111111")
	nameChat(t, st, jid, "short", NameSaved)
	if _, err := st.writer.ExecContext(context.Background(), `UPDATE chats SET name = ? WHERE jid = ?`,
		strings.Repeat("x", maxNameBytes+1), jid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetChat(context.Background(), jid, false); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized chat name: %v, want resource limit", err)
	}
}

func TestToolGetChatPointPlan(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	jid := phoneJID("491511111111")
	nameChat(t, st, jid, "name", NameSaved)
	rec := &recorder{}
	st.hooks.wrapReader = func(q querier) querier { rec.q = q; return rec }
	if _, err := st.GetChat(context.Background(), jid, false); err != nil {
		t.Fatal(err)
	}
	if len(rec.queries) != 1 {
		t.Fatalf("expected one point query, got %#v", rec.queries)
	}
	plan := strings.Join(planOf(t, st, rec.queries[0]), "\n")
	if !strings.Contains(plan, "SEARCH chats USING INDEX") || strings.Contains(plan, "SCAN chats") ||
		strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("unexpected point plan:\n%s", plan)
	}
}

func TestToolListChatsFirstStepPlans(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sort  ChatSort
		index string
	}{
		{"activity", ByLastActivity, "chats_last_activity"},
		{"name", ByName, "chats_name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := openTest(t, newDataDir(t))
			jid := phoneJID("491511111111")
			mustStore(t, st, msg(jid, "one", jid, at(1)))
			rec := &recorder{}
			st.hooks.wrapReader = func(q querier) querier { rec.q = q; return rec }
			got, err := st.ListChats(context.Background(), "", tc.sort, false, Page{Limit: 1})
			if err != nil || len(got) != 1 || got[0].JID != jid {
				t.Fatalf("ListChats: %#v, %v", got, err)
			}
			if len(rec.queries) != 1 || !strings.Contains(strings.ToLower(rec.queries[0].sql), "json_group_array") {
				t.Fatalf("not one aggregate statement: %#v", rec.queries)
			}
			plan := strings.Join(planOf(t, st, rec.queries[0]), "\n")
			if !strings.Contains(plan, tc.index) || strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") ||
				!strings.Contains(plan, "USE TEMP B-TREE FOR json_group_array(ORDER BY)") {
				t.Fatalf("unexpected query plan:\n%s", plan)
			}
		})
	}
}

func TestToolListChatsWalkCeiling(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	if _, err := st.writer.ExecContext(ctx, `WITH RECURSIVE n(x) AS
		(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x < 100200)
		INSERT INTO chats(jid) SELECT printf('%09d@s.whatsapp.net', x) FROM n`); err != nil {
		t.Fatal(err)
	}
	if got, err := st.ListChats(ctx, "not-present", ByLastActivity, false, Page{Limit: 1}); err != nil || len(got) != 0 {
		t.Fatalf("at walk ceiling: %#v, %v", got, err)
	}
	if _, err := st.writer.ExecContext(ctx, `INSERT INTO chats(jid) VALUES ('100201000@s.whatsapp.net')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListChats(ctx, "not-present", ByLastActivity, false, Page{Limit: 1}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("short page above walk ceiling: %v", err)
	}
	if got, err := st.ListChats(ctx, "", ByLastActivity, false, Page{Limit: 1}); err != nil || len(got) != 1 {
		t.Fatalf("full page above walk ceiling: %#v, %v", got, err)
	}
}

func TestToolListChatsAggregateNameBytes(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	jid := phoneJID("491511111111")
	name := "a\x00\x01b"
	nameChat(t, st, jid, name, NameSaved)
	got, err := st.ListChats(context.Background(), "", ByName, false, Page{Limit: 1})
	if err != nil || len(got) != 1 || got[0].Name != name {
		t.Fatalf("aggregate name: %#v, %v", got, err)
	}
}

func TestToolContactChatsFirstStepPlan(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	jid := phoneJID("491511111111")
	mustStore(t, st, msg(jid, "one", jid, at(1)))
	rec := &recorder{}
	st.hooks.wrapReader = func(q querier) querier { rec.q = q; return rec }
	got, err := st.ContactChats(context.Background(), jid, Page{Limit: 1})
	if err != nil || len(got) != 1 || got[0].JID != jid {
		t.Fatalf("ContactChats: %#v, %v", got, err)
	}
	for _, query := range rec.queries {
		if !strings.Contains(strings.ToLower(query.sql), "json_group_array") {
			continue
		}
		plan := strings.Join(planOf(t, st, query), "\n")
		if !strings.Contains(plan, "chats_last_activity") || !strings.Contains(plan, "messages_chat_sender_phone") ||
			!strings.Contains(plan, "messages_chat_sender_jid") || strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") ||
			!strings.Contains(plan, "USE TEMP B-TREE FOR json_group_array(ORDER BY)") {
			t.Fatalf("unexpected query plan:\n%s", plan)
		}
		return
	}
	t.Fatalf("missing aggregate statement: %#v", rec.queries)
}

func TestToolContactChatsWalkCeiling(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	if _, err := st.writer.ExecContext(ctx, `WITH RECURSIVE n(x) AS
		(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x < 100200)
		INSERT INTO chats(jid) SELECT printf('%09d@s.whatsapp.net', x) FROM n`); err != nil {
		t.Fatal(err)
	}
	contact := phoneJID("491511111111")
	if got, err := st.ContactChats(ctx, contact, Page{Limit: 1}); err != nil || len(got) != 0 {
		t.Fatalf("at walk ceiling: %#v, %v", got, err)
	}
	if _, err := st.writer.ExecContext(ctx, `INSERT INTO chats(jid) VALUES ('100201000@s.whatsapp.net')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ContactChats(ctx, contact, Page{Limit: 1}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("short page above walk ceiling: %v", err)
	}
	if got, err := st.ContactChats(ctx, phoneJID("000000001"), Page{Limit: 1}); err != nil || len(got) != 1 {
		t.Fatalf("full page above walk ceiling: %#v, %v", got, err)
	}
}

func TestToolFacadeGetChatAdmission(t *testing.T) {
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	jid := phoneJID("491511111111")
	mustStore(t, st, msg(jid, "one", jid, at(1)))
	got, err := st.ToolReader().GetChat(context.Background(), jid, false)
	if err != nil || got.JID != jid || got.Latest != nil {
		t.Fatalf("tool metadata read: %#v, %v", got, err)
	}
	release, _, err := enterTestStoreTool(st)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := st.ToolReader().GetChat(context.Background(), jid, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("second tool read: %v, want busy", err)
	}
}

func TestToolFacadeRetainsLeaseUntilReadReturns(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
		queryHold: func(ctx context.Context, name string) {
			if name == "GetChat" {
				close(entered)
				<-unblock
			}
		},
	}})
	jid := phoneJID("491511111111")
	nameChat(t, st, jid, "name", NameSaved)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := st.ToolReader().GetChat(ctx, jid, false); done <- err }()
	<-entered
	cancel()
	if _, err := st.ToolReader().GetChat(context.Background(), jid, false); !errors.Is(err, ErrBusy) {
		close(unblock)
		<-done
		t.Fatalf("replacement while cancelled read holds connection: %v", err)
	}
	if st.gate.activeCount() != 1 {
		close(unblock)
		<-done
		t.Fatal("cancelled read lost admission accounting")
	}
	close(unblock)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	if st.gate.activeCount() != 0 {
		t.Fatal("read did not release admission")
	}
}

func TestToolFacadeRetainsLeaseDuringConnectionCleanup(t *testing.T) {
	entered, unblock := make(chan struct{}), make(chan struct{})
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
		onRead: func(*sql.Conn) func() {
			return func() { close(entered); <-unblock }
		},
	}})
	jid := phoneJID("491511111111")
	nameChat(t, st, jid, "name", NameSaved)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := st.ToolReader().GetChat(ctx, jid, false); done <- err }()
	<-entered
	cancel()
	if _, err := st.ToolReader().GetChat(context.Background(), jid, false); !errors.Is(err, ErrBusy) {
		close(unblock)
		<-done
		t.Fatalf("replacement during connection cleanup: %v", err)
	}
	if st.gate.activeCount() != 1 {
		close(unblock)
		<-done
		t.Fatal("connection cleanup lost admission accounting")
	}
	close(unblock)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	if st.gate.activeCount() != 0 {
		t.Fatal("connection cleanup did not release admission")
	}
}

func TestToolFacadeControlFailureRetainsLease(t *testing.T) {
	shortWaits(t, 30*time.Millisecond, closeGrace, closeCancelWait)
	entered, unblock := make(chan struct{}), make(chan struct{})
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
		queryHold: func(ctx context.Context, name string) {
			if name == "GetChat" {
				close(entered)
				<-unblock
			}
		},
	}})
	jid := phoneJID("491511111111")
	nameChat(t, st, jid, "name", NameSaved)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := st.ToolReader().GetChat(ctx, jid, false); done <- err }()
	<-entered
	cancel()
	select {
	case <-st.ToolStoreNotQuiescent():
	case <-time.After(5 * time.Second):
		close(unblock)
		<-done
		t.Fatal("cancelled held read did not latch control failure")
	}
	if st.gate.activeCount() != 1 {
		t.Error("held read lost its admission accounting")
	}
	if _, err := st.ToolReader().GetChat(context.Background(), jid, false); !errors.Is(err, ErrBusy) {
		t.Errorf("replacement during control failure: %v", err)
	}
	close(unblock)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("held read returned %v", err)
	}
	if _, err := st.ToolReader().GetChat(context.Background(), jid, false); !errors.Is(err, ErrBusy) {
		t.Errorf("replacement after control failure: %v", err)
	}
}

func TestToolDirectChatPointPlans(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	jid := phoneJID("491511111111")
	mustStore(t, st, msg(jid, "one", jid, at(1)))
	rec := &recorder{}
	st.hooks.wrapReader = func(q querier) querier { rec.q = q; return rec }
	got, err := st.DirectChat(context.Background(), jid)
	if err != nil || got.JID != jid || got.Latest == nil {
		t.Fatalf("DirectChat: %#v, %v", got, err)
	}
	if len(rec.queries) != 3 {
		t.Fatalf("expected identity, chat and latest point statements: %#v", rec.queries)
	}
	for _, query := range rec.queries {
		plan := strings.Join(planOf(t, st, query), "\n")
		if strings.Contains(plan, "SCAN chats") || strings.Contains(plan, "SCAN messages") ||
			strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") {
			t.Fatalf("unbounded direct-chat plan:\n%s", plan)
		}
	}
}

func TestToolFacadeQueryEquivalence(t *testing.T) {
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	dev := seedDevice(t, st)
	phone, lid := "491511111111", "200000000001"
	seedLIDMapping(t, dev, lid, phone)
	jid := phoneJID(phone)
	if err := dev.Contacts.PutContactName(context.Background(), types.NewJID(phone, types.DefaultUserServer), "", "A\x00lice"); err != nil {
		t.Fatal(err)
	}
	mustStore(t, st, msg(jid, "direct", jid, at(1)), msg(groupOne, "group", lidJID(lid), at(2)))
	reader := st.ToolReader()
	ctx, page := context.Background(), Page{Limit: 10}
	tests := []struct {
		name     string
		ordinary func() (any, error)
		tool     func() (any, error)
	}{
		{"list", func() (any, error) { return st.ListChats(ctx, "", ByLastActivity, true, page) }, func() (any, error) { return reader.ListChats(ctx, "", ByLastActivity, true, page) }},
		{"get", func() (any, error) { return st.GetChat(ctx, jid, true) }, func() (any, error) { return reader.GetChat(ctx, jid, true) }},
		{"contacts", func() (any, error) { return st.SearchContacts(ctx, "", page) }, func() (any, error) { return reader.SearchContacts(ctx, "", page) }},
		{"direct alias", func() (any, error) { return st.DirectChat(ctx, lidJID(lid)) }, func() (any, error) { return reader.DirectChat(ctx, lidJID(lid)) }},
		{"contact chats alias", func() (any, error) { return st.ContactChats(ctx, lidJID(lid), page) }, func() (any, error) { return reader.ContactChats(ctx, lidJID(lid), page) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := tc.ordinary()
			if err != nil {
				t.Fatal(err)
			}
			got, err := tc.tool()
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("facade=%#v, %v; ordinary=%#v", got, err, want)
			}
		})
	}
}

func TestToolFiveReadsExpectedResults(t *testing.T) {
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	dev := seedDevice(t, st)
	phone, lid := "491511111111", "200000000001"
	jid, alias := phoneJID(phone), lidJID(lid)
	seedLIDMapping(t, dev, lid, phone)
	seedContacts(t, dev, contactSeed{jid: jid, full: "Alice"}, contactSeed{jid: alias, push: "Other"})
	mustStore(t, st, msg(jid, "direct", jid, at(1)), msg(groupOne, "group", alias, at(2)))
	r := st.ToolReader()
	ctx, page := context.Background(), Page{Limit: 10}
	chats, err := r.ListChats(ctx, "", ByLastActivity, true, page)
	if err != nil || len(chats) != 2 || chats[0].JID != groupOne || chats[0].Latest == nil || chats[0].Latest.ID != "group" ||
		chats[1].JID != jid || chats[1].Latest == nil || chats[1].Latest.ID != "direct" {
		t.Fatalf("list: %#v, %v", chats, err)
	}
	chat, err := r.GetChat(ctx, jid, false)
	if err != nil || chat.JID != jid || chat.Latest != nil || chat.LastActivity == nil || !chat.LastActivity.Equal(at(1)) {
		t.Fatalf("get: %#v, %v", chat, err)
	}
	contacts, err := r.SearchContacts(ctx, "other", page)
	if err != nil || len(contacts) != 1 || contacts[0] != (Contact{JID: jid, Phone: phone, Name: "Alice"}) {
		t.Fatalf("contacts: %#v, %v", contacts, err)
	}
	chat, err = r.DirectChat(ctx, alias)
	if err != nil || chat.JID != jid || chat.Latest == nil || chat.Latest.ID != "direct" {
		t.Fatalf("direct: %#v, %v", chat, err)
	}
	chats, err = r.ContactChats(ctx, alias, page)
	if err != nil || len(chats) != 2 || chats[0].JID != groupOne || chats[1].JID != jid {
		t.Fatalf("contact chats: %#v, %v", chats, err)
	}
}

func TestToolLatestSenderNameSourceCeiling(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	dev := seedDevice(t, st)
	jid := types.NewJID("491511111111", types.DefaultUserServer)
	if err := dev.Contacts.PutContactName(context.Background(), jid, "", "short"); err != nil {
		t.Fatal(err)
	}
	mustStore(t, st, msg(jid.String(), "one", jid.String(), at(1)))
	if _, err := st.writer.ExecContext(context.Background(),
		`UPDATE whatsmeow_contacts SET full_name = ? WHERE their_jid = ?`, strings.Repeat("x", maxNameBytes+1), jid.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListChats(context.Background(), "", ByLastActivity, true, Page{Limit: 1}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized sender name: got %v, want resource limit", err)
	}
}

func TestToolLatestSenderChatNameSourceCeiling(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	sender := phoneJID("491511111111")
	nameChat(t, st, sender, "short", NameSaved)
	mustStore(t, st, msg(groupOne, "one", sender, at(1)))
	if _, err := st.writer.ExecContext(context.Background(),
		`UPDATE chats SET name = ? WHERE jid = ?`, strings.Repeat("x", maxNameBytes+1), sender); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetChat(context.Background(), groupOne, true); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized sender chat name: %v, want resource limit", err)
	}
}

func TestToolReadDoesNotSucceedAfterCancellation(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := st.read(ctx, "test", func(context.Context, *sql.Conn) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancellation: got %v, want context canceled", err)
	}
}

func enterTestTool(g *gate) (func(), context.Context, error) {
	return g.enterTool(context.Background(), true)
}

func enterTestStoreTool(st *Store) (func(), context.Context, error) {
	if guarded, ok := any(st).(interface {
		enterTool(context.Context) (func(), context.Context, error)
	}); ok {
		return guarded.enterTool(context.Background())
	}
	return enterTestTool(st.gate)
}

func TestToolAdmissionRefusesLowDisk(t *testing.T) {
	st, records := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 600 * mib, nil },
	}})
	release, _, err := enterTestStoreTool(st)
	if err == nil {
		release()
		t.Fatal("tool admitted below the 640 MiB reserve")
	}
	if !errors.Is(err, ErrDiskLow) {
		t.Fatalf("low disk: %v", err)
	}
	if len(records.events("store.limit")) == 0 {
		t.Fatal("no store.limit event")
	}
}

func TestToolAdmissionRejectsUnknownWALSize(t *testing.T) {
	st, records := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	path := st.path
	st.path += "\x00"
	defer func() { st.path = path }()
	release, _, err := enterTestStoreTool(st)
	if err == nil {
		release()
		t.Fatal("tool admitted with unknown WAL size")
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("unknown WAL size: %v", err)
	}
	if len(records.events("store.limit")) == 0 {
		t.Fatal("no store.limit event")
	}
}

func TestToolWALCheckpointWithoutFurtherWrites(t *testing.T) {
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	ctx := context.Background()
	conn, err := st.reader.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM chats").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err := st.writer.ExecContext(ctx, "CREATE TABLE tool_wal_test (v BLOB)"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.writer.ExecContext(ctx, "INSERT INTO tool_wal_test VALUES (zeroblob(?))", 66*mib); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	size, err := st.walSize()
	if err != nil || size <= walLimitBytes {
		t.Fatalf("fixture WAL size %d, error %v", size, err)
	}
	st.disk.mu.Lock()
	st.disk.free = func(string) (uint64, error) { return 100 * mib, nil }
	st.disk.measured = time.Time{}
	st.disk.mu.Unlock()
	if release, _, err := enterTestStoreTool(st); !errors.Is(err, ErrDiskLow) {
		if err == nil {
			release()
		}
		t.Fatalf("oversized WAL and low disk admission: %v", err)
	}
	eventually(t, "checkpoint truncation", func() bool { size, err := st.walSize(); return err == nil && size <= walLimitBytes })
	eventually(t, "checkpoint release", func() bool { st.gate.mu.Lock(); defer st.gate.mu.Unlock(); return !st.gate.tool })
	st.disk.mu.Lock()
	st.disk.free = func(string) (uint64, error) { return 2 * gib, nil }
	st.disk.measured = time.Time{}
	st.disk.mu.Unlock()
	release, _, err := enterTestStoreTool(st)
	if err != nil {
		t.Fatalf("after recovery: %v", err)
	}
	release()
}

func TestToolCheckpointWaitingForWriterExcludesToolRead(t *testing.T) {
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	writer, err := st.writer.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	oldPath := st.path
	st.path = filepath.Join(t.TempDir(), "checkpoint-fixture")
	defer func() { st.path = oldPath }()
	f, err := os.Create(st.path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(walLimitBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if release, _, err := enterTestStoreTool(st); !errors.Is(err, ErrBusy) {
		if err == nil {
			release()
		}
		t.Fatalf("oversized WAL: %v", err)
	}
	st.gate.mu.Lock()
	checkpointActive := st.gate.tool && !st.gate.toolReader && st.gate.active == 1
	st.gate.mu.Unlock()
	if !checkpointActive {
		t.Fatal("checkpoint did not retain the tool place while waiting for the writer")
	}
	if _, _, err := enterTestTool(st.gate); !errors.Is(err, ErrBusy) {
		t.Fatalf("tool admitted during checkpoint: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "checkpoint release", func() bool { return st.gate.activeCount() == 0 })
}

func TestToolWALWriteCancelsWithoutReleasing(t *testing.T) {
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	release, toolCtx, err := enterTestStoreTool(st)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	oldPath := st.path
	st.path = filepath.Join(t.TempDir(), "synthetic")
	defer func() { st.path = oldPath }()
	f, err := os.Create(st.path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2*walLimitBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.write(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE tool_guard_test (v INTEGER)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(toolCtx.Err(), context.Canceled) {
		t.Fatalf("WAL guard did not cancel: %v", toolCtx.Err())
	}
	if _, _, err := enterTestTool(st.gate); !errors.Is(err, ErrBusy) {
		t.Fatalf("replacement before release: %v", err)
	}
}

func TestToolAdmissionUsesFreshDiskSample(t *testing.T) {
	free := uint64(2 * gib)
	var sampleErr error
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return free, sampleErr },
	}})
	if err := st.disk.check(); err != nil {
		t.Fatal(err)
	}
	free = diskReserve - 1
	release, _, err := enterTestStoreTool(st)
	if err == nil {
		release()
	}
	if !errors.Is(err, ErrDiskLow) {
		t.Errorf("tool admitted on stale free space: %v", err)
	}
	if err := st.disk.check(); err != nil {
		t.Fatalf("tool check refreshed the ordinary disk cache: %v", err)
	}
	free, sampleErr = 2*gib, errors.New("synthetic disk measurement failure")
	release, _, err = enterTestStoreTool(st)
	if err == nil {
		release()
	}
	if !errors.Is(err, ErrDiskLow) {
		t.Fatalf("tool admitted after failed free-space measurement: %v", err)
	}
}

func TestToolWALWaitRetainsDrainAfterLatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newGate()
		lease, ctx, err := g.enter(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer lease()
		// Fence timing is independent of how long an admitted device lease has already run.
		time.Sleep(admissionWait + time.Millisecond)
		// Use a longer caller context for expiry to isolate the fence from the old lease's deadline.
		release, _, err := enterTestTool(g)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		g.walDraining = true
		short, cancel := context.WithCancel(ctx)
		first := make(chan error, 1)
		go func() { first <- g.beginWriter(short) }()
		synctest.Wait()
		deadline := g.walDeadline
		cancel()
		if err := <-first; !errors.Is(err, context.Canceled) || deadline.IsZero() {
			t.Fatalf("first wait: deadline=%v error=%v", deadline, err)
		}
		if err := g.beginWriter(context.Background()); !errors.Is(err, ErrBusy) || time.Now() != deadline {
			t.Fatalf("shared deadline not honored: %v", err)
		}
		select {
		case <-g.toolFailure:
		default:
			t.Fatal("writer timeout did not latch control failure")
		}
		if err := g.beginWriter(context.Background()); !errors.Is(err, ErrBusy) || time.Now() != deadline {
			t.Fatalf("second writer did not fail fast: %v", err)
		}
		if g.activeCount() != 2 {
			t.Fatal("timeout released real accounting")
		}
		release()
		if err := g.beginWriter(context.Background()); err != nil {
			t.Fatalf("fence persisted after release: %v", err)
		}
		if _, _, err := enterTestTool(g); !errors.Is(err, ErrBusy) {
			t.Fatalf("replacement admitted after failure: %v", err)
		}
	})
}

func TestWriterDriverInterfaceParity(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	ctx := context.Background()
	conn, err := st.writer.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		w := raw.(*writerConn)
		check := func(wrapped, inner any, interfaces ...reflect.Type) {
			for _, it := range interfaces {
				if reflect.TypeOf(wrapped).Implements(it) != reflect.TypeOf(inner).Implements(it) {
					t.Errorf("%T changed %v capability", wrapped, it)
				}
			}
		}
		check(w, w.sqliteWriterConn, reflect.TypeFor[driver.Execer](), reflect.TypeFor[driver.Queryer](),
			reflect.TypeFor[driver.ConnBeginTx](), reflect.TypeFor[driver.ConnPrepareContext](),
			reflect.TypeFor[driver.ExecerContext](), reflect.TypeFor[driver.QueryerContext](),
			reflect.TypeFor[driver.Pinger](), reflect.TypeFor[driver.SessionResetter](),
			reflect.TypeFor[driver.Validator](), reflect.TypeFor[driver.NamedValueChecker]())
		prepared, err := w.PrepareContext(ctx, "SELECT 1")
		if err != nil {
			return err
		}
		stmt := prepared.(*writerStmt)
		defer stmt.Close()
		check(stmt, stmt.sqliteWriterStmt, reflect.TypeFor[driver.StmtExecContext](),
			reflect.TypeFor[driver.StmtQueryContext](), reflect.TypeFor[driver.ColumnConverter](), reflect.TypeFor[driver.NamedValueChecker]())
		queried, err := stmt.QueryContext(ctx, nil)
		if err != nil {
			return err
		}
		rows := queried.(*writerRows)
		defer rows.Close()
		check(rows, rows.sqliteWriterRows, reflect.TypeFor[driver.RowsNextResultSet](), reflect.TypeFor[driver.RowsColumnScanner](),
			reflect.TypeFor[driver.RowsColumnTypeScanType](), reflect.TypeFor[driver.RowsColumnTypeDatabaseTypeName](),
			reflect.TypeFor[driver.RowsColumnTypeLength](), reflect.TypeFor[driver.RowsColumnTypeNullable](),
			reflect.TypeFor[driver.RowsColumnTypePrecisionScale]())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestToolWALActualWritesCancelSnapshot(t *testing.T) {
	for _, device := range []bool{false, true} {
		name := "ingest"
		if device {
			name = "device"
		}
		t.Run(name, func(t *testing.T) { toolWALActualWritesCancelSnapshot(t, device) })
	}
}

func toolWALActualWritesCancelSnapshot(t *testing.T, device bool) {
	t.Helper()
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	ctx := context.Background()
	if _, err := st.writer.ExecContext(ctx, `CREATE TABLE tool_actual_wal(v BLOB)`); err != nil {
		t.Fatal(err)
	}
	release, toolCtx, err := enterTestStoreTool(st)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	conn, err := st.reader.Conn(toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The fixture owns the transaction until explicitly released, even after tool cancellation.
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(toolCtx, `SELECT count(*) FROM tool_actual_wal`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if toolCtx.Err() != nil {
			t.Fatalf("tool cancelled before crossing threshold: %v", toolCtx.Err())
		}
		if device {
			err = st.wrapped.DoTxn(ctx, nil, func(ctx context.Context) error {
				for range 2 {
					if _, err := st.wrapped.Exec(ctx, `INSERT INTO tool_actual_wal VALUES (zeroblob(?))`, 4*mib); err != nil {
						return err
					}
				}
				return nil
			})
		} else {
			err = st.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO tool_actual_wal VALUES (zeroblob(?))`, 8*mib)
				return err
			})
		}
		if err != nil {
			t.Fatal(err)
		}
		size, err := st.walSize()
		if err != nil {
			t.Fatal(err)
		}
		if size <= 2*walLimitBytes {
			if toolCtx.Err() != nil {
				t.Fatalf("tool cancelled at WAL size %d", size)
			}
			continue
		}
		if !errors.Is(toolCtx.Err(), context.Canceled) {
			t.Fatalf("WAL size %d did not cancel snapshot: %v", size, toolCtx.Err())
		}
		if _, _, err := enterTestTool(st.gate); !errors.Is(err, ErrBusy) {
			t.Fatalf("replacement while snapshot held: %v", err)
		}
		later, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		_, err = st.writer.ExecContext(later, `INSERT INTO tool_actual_wal VALUES (zeroblob(4096))`)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("second write entered SQLite before actual snapshot release: %v", err)
		}
		if after, err := st.walSize(); err != nil || after != size {
			t.Fatalf("post-crossing write changed the pinned WAL: before %d, after %d, err %v", size, after, err)
		}
		st.gate.mu.Lock()
		deadline := st.gate.walDeadline
		st.gate.mu.Unlock()
		if deadline.IsZero() {
			t.Fatal("writer never reached the fence")
		}
		blocked := make(chan error, 1)
		go func() {
			_, err := st.wrapped.Exec(ctx, `INSERT INTO tool_actual_wal VALUES (zeroblob(4096))`)
			blocked <- err
		}()
		_ = tx.Rollback()
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		release()
		if err := <-blocked; err != nil {
			t.Fatalf("device adapter write after actual return: %v", err)
		}
		if next, _, err := enterTestStoreTool(st); !errors.Is(err, ErrBusy) {
			if err == nil {
				next()
			}
			t.Fatalf("oversized WAL before checkpoint: %v", err)
		}
		eventually(t, "actual WAL recovery checkpoint", func() bool {
			size, err := st.walSize()
			st.gate.mu.Lock()
			active := st.gate.tool
			st.gate.mu.Unlock()
			return err == nil && size <= walLimitBytes && !active
		})
		next, _, err := enterTestStoreTool(st)
		if err != nil {
			t.Fatalf("after actual WAL recovery: %v", err)
		}
		next()
		return
	}
	t.Fatal("WAL did not cross the cancellation threshold")
}

func TestToolAdmissionRejectsChangedWriterGeneration(t *testing.T) {
	var onSample func()
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) {
			if onSample != nil {
				onSample()
			}
			return 2 * gib, nil
		},
	}})
	onSample = func() {
		if _, err := st.writer.Exec(`CREATE TABLE tool_wal_admission_race(v INTEGER)`); err != nil {
			t.Fatal(err)
		}
	}
	release, _, err := enterTestStoreTool(st)
	if err == nil {
		release()
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("tool admitted on past writer generation: %v", err)
	}
	onSample = nil
	release, _, err = enterTestStoreTool(st)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestToolConcurrentIngestAndReadmission(t *testing.T) {
	t.Run("light", func(t *testing.T) { toolConcurrentIngestAndReadmission(t, 120, 1, 128*kib) })
	t.Run("maximum-batches", func(t *testing.T) { toolConcurrentIngestAndReadmission(t, 12, 64, maxTextBytes) })
}

func toolConcurrentIngestAndReadmission(t *testing.T, writes, batchSize, textBytes int) {
	t.Helper()
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free: func(string) (uint64, error) { return 2 * gib, nil },
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jid := phoneJID("491511111111")
	mustStore(t, st, msg(jid, "initial", jid, at(0)))
	type writerResult struct {
		peak, maxBatch int64
		err            error
	}
	done := make(chan writerResult, 1)
	finished := false
	defer func() {
		if !finished {
			cancel()
			<-done
		}
	}()
	start := make(chan struct{})
	go func() {
		<-start
		var peak, maxBatch int64
		payload := strings.Repeat("x", textBytes)
		for i := 0; i < writes; i++ {
			before, err := st.walSize()
			if err != nil {
				done <- writerResult{peak: peak, maxBatch: maxBatch, err: err}
				return
			}
			batch := make([]Message, batchSize)
			var bytes int
			for j := range batch {
				batch[j] = msg(jid, fmt.Sprintf("flood-%04d-%04d", i, j), jid, at(i+1), withText(payload))
				bytes += MessageFieldBytes(batch[j])
			}
			if batchSize > 1 {
				// Fill the approved byte budget exactly, including identity fields.
				last := &batch[len(batch)-1]
				last.Text = last.Text[:len(last.Text)-(bytes-maxBatchBytes)]
			}
			if err := st.StoreMessages(ctx, batch); err != nil {
				done <- writerResult{peak: peak, maxBatch: maxBatch, err: err}
				return
			}
			size, err := st.walSize()
			if err != nil {
				done <- writerResult{peak: peak, maxBatch: maxBatch, err: err}
				return
			}
			if size > peak {
				peak = size
			}
			if size-before > maxBatch {
				maxBatch = size - before
			}
		}
		done <- writerResult{peak: peak, maxBatch: maxBatch}
	}()
	close(start)
	var success, busy, canceled int
	for {
		select {
		case result := <-done:
			finished = true
			if result.err != nil {
				t.Fatalf("ingest failed: %v", result.err)
			}
			if success == 0 {
				t.Fatal("no tool read was admitted during ingest")
			}
			if result.peak > 2*walLimitBytes+result.maxBatch {
				t.Fatalf("WAL peak %d exceeds threshold plus one batch", result.peak)
			}
			t.Logf("%d ingest writes of %d messages; reads success=%d busy=%d canceled=%d; sampled post-write WAL peak=%d bytes, max observed batch growth=%d bytes", writes, batchSize, success, busy, canceled, result.peak, result.maxBatch)
			// An earlier checkpoint attempt may still occupy the tool slot or
			// have started the cooldown. Recovery is retried by tool calls.
			retryCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			for {
				_, err := st.ToolReader().GetChat(retryCtx, jid, false)
				if err == nil {
					break
				}
				if !errors.Is(err, ErrBusy) {
					t.Fatalf("tool read after ingest recovery: %v", err)
				}
				select {
				case <-retryCtx.Done():
					t.Fatalf("post-ingest WAL did not recover: %v", retryCtx.Err())
				case <-tick.C:
				}
			}
			if size, err := st.walSize(); err != nil || size > walLimitBytes {
				t.Fatalf("post-ingest WAL: %d bytes, %v", size, err)
			}
			return
		default:
		}
		_, err := st.ToolReader().ListChats(ctx, "", ByLastActivity, false, Page{Limit: 1})
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrBusy):
			busy++
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			canceled++
		default:
			t.Fatalf("tool read: %v", err)
		}
	}
}

func TestToolGateExclusiveAndRetainsSlot(t *testing.T) {
	g := newGate()
	release, _, err := enterTestTool(g)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	second, _, err := enterTestTool(g)
	if err == nil {
		second()
		t.Fatal("a second tool was admitted while the first still held its slot")
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second tool: got %v, want busy", err)
	}
}

func TestToolGateReaderPressureRetainsAccounting(t *testing.T) {
	g := newGate()
	toolRelease, toolCtx, err := enterTestTool(g)
	if err != nil {
		t.Fatal(err)
	}
	defer toolRelease()
	var releases []func()
	for i := 0; i < readConns; i++ {
		release, _, err := g.enterRead(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
		if i < readConns-1 && toolCtx.Err() != nil {
			t.Fatal("tool cancelled before fourth ordinary reader")
		}
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	if !errors.Is(toolCtx.Err(), context.Canceled) {
		t.Fatalf("fourth reader did not cancel tool: %v", toolCtx.Err())
	}
	if _, _, err := enterTestTool(g); !errors.Is(err, ErrBusy) {
		t.Fatalf("replacement before tool release: %v", err)
	}
	if got := g.activeCount(); got != readConns+1 {
		t.Fatalf("active = %d, want tool plus four readers", got)
	}
}

// Pause a real device call outside the gate mutex, after it registered as a waiter.
type parkedDeviceContext struct {
	context.Context
	parked     chan struct{}
	resume     <-chan struct{}
	parkedOnce sync.Once
}

func (c *parkedDeviceContext) Done() <-chan struct{} {
	c.parkedOnce.Do(func() { close(c.parked) })
	<-c.resume
	return c.Context.Done()
}

func TestToolGatePrioritizesWaitingDevice(t *testing.T) {
	st, dev, _ := loadedDevice(t, newDataDir(t))
	const phone = "491511111111"
	mustStore(t, st, msg(phoneJID(phone), "one", phoneJID(phone), at(1)))
	toolRelease, toolCtx, err := st.enterTool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer toolRelease()
	ordinary := fill(t, st.gate, maxActive-1)
	defer func() {
		for _, release := range ordinary {
			release()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resume := make(chan struct{})
	var once sync.Once
	unpark := func() { once.Do(func() { close(resume) }) }
	defer unpark()
	deviceCtx := &parkedDeviceContext{Context: ctx, parked: make(chan struct{}), resume: resume}
	done := make(chan error, 1)
	go func() {
		done <- dev.Contacts.PutContactName(deviceCtx, types.NewJID(phone, types.DefaultUserServer), "", "Device update")
	}()
	select {
	case <-deviceCtx.parked:
	case err := <-done:
		t.Fatalf("device returned before queuing: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("device did not reach admission wait")
	}
	if !errors.Is(toolCtx.Err(), context.Canceled) {
		t.Fatalf("waiting device did not cancel active tool: %v", toolCtx.Err())
	}
	toolRelease()
	if st.gate.activeCount() != maxActive-1 || st.gate.waitingCount() != 1 {
		t.Fatal("expected one free active slot and one registered device waiter")
	}
	reader := st.ToolReader()
	for range 5 {
		if _, err := reader.GetChat(ctx, phoneJID(phone), false); !errors.Is(err, ErrBusy) {
			t.Fatalf("tool with a free slot and waiting device: %v", err)
		}
	}
	if st.gate.waitingCount() != 1 {
		t.Fatal("the tool requests joined the device waiter")
	}
	unpark()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("device did not finish after the tool released")
	}
	if _, err := reader.GetChat(ctx, phoneJID(phone), false); err != nil {
		t.Fatalf("tool readmission after device completion: %v", err)
	}
}

func TestToolGatePreservesOrdinaryCapacity(t *testing.T) {
	g := newGate()
	toolRelease, _, err := enterTestTool(g)
	if err != nil {
		t.Fatal(err)
	}
	releases := fill(t, g, maxActive-1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, maxWaiting+1)
	for i := 0; i < maxWaiting+1; i++ {
		go func() {
			release, _, err := g.enter(ctx)
			if err == nil {
				release()
			}
			done <- err
		}()
	}
	eventually(t, "65 ordinary waiters", func() bool { return g.waitingCount() == maxWaiting+1 })
	if _, _, err := g.enter(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("81st ordinary operation: %v, want busy", err)
	}
	if _, _, err := enterTestTool(g); !errors.Is(err, ErrBusy) {
		t.Errorf("tool while ordinary operations wait: %v", err)
	}
	cancel()
	for i := 0; i < maxWaiting+1; i++ {
		<-done
	}
	for _, release := range releases {
		release()
	}
	toolRelease()
}

func TestToolCheckpointPreservesOrdinaryCapacity(t *testing.T) {
	g := newGate()
	checkpointRelease, checkpointCtx, err := g.enterTool(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := fill(t, g, maxActive-1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, maxWaiting+1)
	for i := 0; i < maxWaiting+1; i++ {
		go func() {
			release, _, err := g.enter(ctx)
			if err == nil {
				release()
			}
			done <- err
		}()
	}
	eventually(t, "65 ordinary waiters behind checkpoint", func() bool { return g.waitingCount() == maxWaiting+1 })
	if checkpointCtx.Err() != nil {
		t.Errorf("ordinary pressure cancelled checkpoint: %v", checkpointCtx.Err())
	}
	if _, _, err := g.enter(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("81st ordinary operation: %v, want busy", err)
	}
	if _, _, err := enterTestTool(g); !errors.Is(err, ErrBusy) {
		t.Errorf("tool during checkpoint: %v, want busy", err)
	}
	cancel()
	for i := 0; i < maxWaiting+1; i++ {
		<-done
	}
	for _, release := range ordinary {
		release()
	}
	checkpointRelease()
}

func TestToolGateCheckpointAndControlFailure(t *testing.T) {
	t.Run("checkpoint", func(t *testing.T) {
		g := newGate()
		release, ctx, err := g.enterTool(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		ordinary := fill(t, g, maxActive-1)
		cancelCtx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, _, err := g.enter(cancelCtx); done <- err }()
		eventually(t, "ordinary waiter", func() bool { return g.waitingCount() == 1 })
		if ctx.Err() != nil {
			t.Fatal("ordinary pressure cancelled checkpoint")
		}
		if _, _, err := enterTestTool(g); !errors.Is(err, ErrBusy) {
			t.Fatalf("tool during checkpoint: %v", err)
		}
		cancel()
		<-done
		for _, r := range ordinary {
			r()
		}
		release()
	})
	t.Run("control failure", func(t *testing.T) {
		shortWaits(t, 30*time.Millisecond, closeGrace, closeCancelWait)
		g := newGate()
		release, ctx, err := enterTestTool(g)
		if err != nil {
			t.Fatal(err)
		}
		ordinary := fill(t, g, maxActive-1)
		if _, _, err := g.enter(context.Background()); !errors.Is(err, ErrBusy) {
			t.Fatalf("wait: %v", err)
		}
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("tool context: %v", ctx.Err())
		}
		select {
		case <-g.toolFailure:
		default:
			t.Fatal("control failure not latched")
		}
		release()
		for _, r := range ordinary {
			r()
		}
		if _, _, err := enterTestTool(g); !errors.Is(err, ErrBusy) {
			t.Fatalf("replacement after failure: %v", err)
		}
	})
}

func TestToolGateRunGuardReaderContention(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	release, toolCtx, err := enterTestTool(st.gate)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var readers []func()
	for i := 0; i < readConns-1; i++ {
		r, _, err := st.gate.enterRead(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, r)
	}
	defer func() {
		for _, r := range readers {
			r()
		}
	}()
	if _, _, err := st.RunGuard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(toolCtx.Err(), context.Canceled) {
		t.Fatalf("RunGuard did not cancel tool: %v", toolCtx.Err())
	}
}

func TestToolGateCloseWaitsForActualRelease(t *testing.T) {
	g := newGate()
	release, toolCtx, err := enterTestTool(g)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.quiesce(ctx); !errors.Is(err, ErrNotQuiescent) {
		t.Fatalf("close with held tool: %v", err)
	}
	if !errors.Is(toolCtx.Err(), context.Canceled) {
		t.Fatalf("close did not cancel tool: %v", toolCtx.Err())
	}
	if _, _, err := enterTestTool(g); !errors.Is(err, ErrClosed) {
		t.Fatalf("tool after close: %v", err)
	}
	if g.isQuiesced() {
		t.Fatal("tool still holds its slot")
	}
	release()
	if err := g.quiesce(context.Background()); err != nil {
		t.Fatal(err)
	}
}
