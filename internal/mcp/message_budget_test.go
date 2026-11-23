package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tiger-seo/whatsapp-mcp/internal/store"
	wastore "go.mau.fi/whatsmeow/store"
)

type budgetReader struct {
	*store.ToolReader
	last store.MessageList
}

func (r *budgetReader) ListMessages(ctx context.Context, f store.MessageFilter, c store.Context, p store.Page, contextEnabled bool, policy store.MessageListPolicy) (store.MessageList, error) {
	result, err := r.ToolReader.ListMessages(ctx, f, c, p, contextEnabled, policy)
	r.last = result
	return result, err
}

func TestMessageBudgetContinuation(t *testing.T) {
	for _, tc := range []struct{ name, fill string }{{"ordinary", "x"}, {"escaped", "\x01"}} {
		t.Run(tc.name, func(t *testing.T) {
			const size = 256 << 10
			body := strings.Repeat(tc.fill, size)
			r := newFixture(t, func(st *store.Store, _ *wastore.Device) {
				rows := make([]store.Message, 60)
				for i := range rows {
					text := body
					if i >= 5 && i < 55 {
						text = "anchor" + body[len("anchor"):]
					}
					rows[i] = store.Message{ChatJID: "budget@g.us", SenderJID: fixtureJID(0), ID: fmt.Sprintf("row-%02d", i), Time: time.Date(2026, 4, 1, 0, 0, i, 0, time.UTC), Text: text, Caption: body}
				}
				for i := 0; i < len(rows); i += 4 {
					if err := st.StoreMessages(context.Background(), rows[i:i+4]); err != nil {
						t.Fatal(err)
					}
				}
			})
			c := chatTestServer(t, r.store)
			args := map[string]any{"chat_jid": "budget@g.us", "query": "anchor", "include_context": true, "limit": 50}
			seen := map[string]bool{}
			reduced, sides, previews, single := false, false, false, false
			for calls := 0; calls < 55; calls++ {
				started := time.Now()
				root := chatCall(t, c, "list_messages", args, false)
				if calls == 0 {
					elapsed := time.Since(started)
					t.Logf("default 50-anchor guarded reply: %s", elapsed)
					if elapsed > 10*time.Second {
						t.Fatalf("default context reply exceeded 10 seconds: %s", elapsed)
					}
				}
				hits := root["messages"].([]any)
				if len(hits) == 0 {
					break
				}
				for _, value := range hits {
					hit := value.(map[string]any)
					id := hit["id"].(string)
					if seen[id] {
						t.Fatalf("duplicate anchor %s", id)
					}
					seen[id] = true
					if hit["before_reduced"] == true || hit["after_reduced"] == true {
						sides = true
					}
					if fields, ok := hit["preview_fields"].([]any); ok {
						previews = true
						for _, field := range fields {
							value := hit[field.(string)].(string)
							if len(value) > 32<<10 || !utf8.ValidString(value) {
								t.Fatalf("invalid %s preview", field)
							}
						}
					}
					for _, side := range []string{"before", "after"} {
						for _, neighbour := range hit[side].([]any) {
							if _, ok := neighbour.(map[string]any)["preview_fields"]; ok {
								t.Fatal("neighbour was previewed")
							}
						}
					}
				}
				if root["page_reduced"] == true {
					reduced = true
					next := root["next_page"].(map[string]any)
					if int(next["page"].(float64)) != len(seen) || next["limit"] != float64(1) {
						t.Fatalf("invalid continuation: %v after %d", next, len(seen))
					}
					args["page"], args["limit"] = int(next["page"].(float64)), 1
				} else {
					if args["limit"] != 1 {
						t.Fatal("expected a reduced initial page")
					}
					single = true
					args["page"] = args["page"].(int) + 1
				}
			}
			if len(seen) != 50 || !reduced || !sides || !single || (tc.name == "escaped" && !previews) {
				t.Fatalf("seen=%d reduced=%v sides=%v single=%v previews=%v", len(seen), reduced, sides, single, previews)
			}
			for i := 5; i < 55; i++ {
				if !seen[fmt.Sprintf("row-%02d", i)] {
					t.Fatalf("missing row-%02d", i)
				}
			}
		})
	}
}

func TestMessageBudgetContextRows(t *testing.T) {
	r := newFixture(t, func(st *store.Store, _ *wastore.Device) {
		rows := make([]store.Message, 60)
		for i := range rows {
			rows[i] = store.Message{ChatJID: "rows@g.us", SenderJID: fixtureJID(0), ID: fmt.Sprintf("row-%02d", i), Time: time.Date(2026, 4, 1, 0, 0, i, 0, time.UTC), Text: "x"}
		}
		if err := st.StoreMessages(context.Background(), rows); err != nil {
			t.Fatal(err)
		}
	})
	reader := &budgetReader{ToolReader: r.store.(*store.ToolReader)}
	c := chatTestServer(t, reader)
	root := chatCall(t, c, "list_messages", map[string]any{"chat_jid": "rows@g.us", "limit": 60, "include_context": true, "context_before": 20, "context_after": 20}, false)
	if reader.last.ContextRows < 1999 || reader.last.ContextRows > 2000 || root["page_reduced"] != false {
		t.Fatalf("ledger=%d page_reduced=%v", reader.last.ContextRows, root["page_reduced"])
	}
	reduced, full, empty, short := 0, 0, 0, 0
	for _, v := range root["messages"].([]any) {
		hit := v.(map[string]any)
		var index int
		if _, err := fmt.Sscanf(hit["id"].(string), "row-%d", &index); err != nil {
			t.Fatal(err)
		}
		for _, side := range []string{"before", "after"} {
			rows := hit[side].([]any)
			n := len(rows)
			available := index
			if side == "after" {
				available = 59 - index
			}
			if available > 20 {
				available = 20
			}
			if (hit[side+"_reduced"] == true) != (n < available) {
				t.Fatalf("incorrect %s flag for %s: got %d of %d", side, hit["id"], n, available)
			}
			for j, v := range rows {
				want := index + j + 1
				if side == "before" {
					want = index - n + j
				}
				if v.(map[string]any)["id"] != fmt.Sprintf("row-%02d", want) {
					t.Fatalf("incorrect %s neighbour for %s", side, hit["id"])
				}
			}
			if hit[side+"_reduced"] == true {
				reduced++
			} else if n == 20 {
				full++
			} else if n == 0 {
				empty++
			} else {
				short++
			}
		}
	}
	if reduced == 0 || full == 0 || empty == 0 || short == 0 {
		t.Fatalf("flags: reduced=%d full=%d empty=%d short=%d", reduced, full, empty, short)
	}
}

func TestMessageBudgetMaximumOffset(t *testing.T) {
	r := newFixture(t, func(st *store.Store, _ *wastore.Device) {
		const batch = 500
		for start := 0; start < 100002; start += batch {
			rows := make([]store.Message, 0, batch)
			for i := start; i < start+batch && i < 100002; i++ {
				text, caption := "x", ""
				if i < 2 {
					text = strings.Repeat("\x01", 256<<10)
					caption = text
				}
				rows = append(rows, store.Message{ChatJID: "offset@g.us", SenderJID: fixtureJID(0), ID: fmt.Sprintf("offset-%06d", i), Time: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second), Text: text, Caption: caption})
			}
			if err := st.StoreMessages(context.Background(), rows); err != nil {
				t.Fatal(err)
			}
		}
	})
	c := chatTestServer(t, r.store)
	args := map[string]any{"chat_jid": "offset@g.us", "limit": 1, "page": 100000, "include_context": true, "context_before": 0, "context_after": 0}
	root := chatCall(t, c, "list_messages", args, false)
	if hits := root["messages"].([]any); len(hits) != 1 || hits[0].(map[string]any)["id"] != "offset-000001" || root["page_reduced"] != false || root["narrow_search"] != true {
		t.Fatal("incorrect complete maximum-offset page")
	}
	if _, ok := root["next_page"]; ok {
		t.Fatal("invalid continuation at maximum offset")
	}
	args["page"], args["limit"] = 500, 200
	root = chatCall(t, c, "list_messages", args, false)
	if len(root["messages"].([]any)) != 1 || root["page_reduced"] != true || root["narrow_search"] != true {
		t.Fatal("expected reduced page with unsupported next offset")
	}
	if _, ok := root["next_page"]; ok {
		t.Fatal("unsupported next offset supplied")
	}
	args["page"], args["limit"] = 100000, 1
	args["query"] = "absent"
	root = chatCall(t, c, "list_messages", args, false)
	if len(root["messages"].([]any)) != 0 {
		t.Fatal("expected empty page")
	}
	if _, ok := root["narrow_search"]; ok {
		t.Fatal("empty page advises narrowing")
	}
	if _, ok := root["next_page"]; ok {
		t.Fatal("empty page contains continuation")
	}
}
