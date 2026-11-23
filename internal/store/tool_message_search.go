package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// MessageListHit holds a verified message and its admitted context.
type MessageListHit struct {
	Message                     MessageView
	Before, After               []MessageView
	BeforeReduced, AfterReduced bool
	PreviewFields               []string
}

// MessageList contains a contiguous prefix of the verified page.
type MessageList struct {
	Hits        []MessageListHit
	PageReduced bool
	ContextRows int
}

// MessageListPolicy is invoked synchronously. Only a first anchor may be
// returned with shorter text or caption. Rejected rows must not be retained.
type MessageListPolicy func(row MessageView, anchor, first bool) (MessageView, bool, error)

// ListMessages reads one verified page and, when requested, same-chat context.
func (r *ToolReader) ListMessages(ctx context.Context, f MessageFilter, counts Context, p Page, includeContext bool, policy MessageListPolicy) (MessageList, error) {
	if err := validatePage(p); err != nil {
		return MessageList{}, err
	}
	if err := validateContext(counts); err != nil {
		return MessageList{}, err
	}
	if err := validText("query", f.Query, maxSearchBytes); err != nil {
		return MessageList{}, err
	}
	for _, item := range []struct {
		name  string
		value time.Time
	}{{"after", f.After}, {"before", f.Before}} {
		if !item.value.IsZero() {
			if err := validateTime(item.name, item.value); err != nil {
				return MessageList{}, err
			}
		}
	}
	if f.ChatJID != "" {
		chat, err := parseChatJID("chat_jid", f.ChatJID)
		if err != nil {
			return MessageList{}, err
		}
		f.ChatJID = chat.String()
	}
	if f.Sender != "" {
		if _, _, err := parseContact(f.Sender); err != nil {
			return MessageList{}, err
		}
	}
	if policy == nil {
		return MessageList{}, invalid("policy", "required")
	}
	return messageSnapshot(ctx, r, func(ctx context.Context, q querier) (MessageList, error) {
		keys, err := searchMessageKeys(ctx, q, f, p)
		if err != nil {
			return MessageList{}, err
		}
		result := MessageList{Hits: make([]MessageListHit, 0, len(keys))}
		budget := &resultBudget{}
		admitted := make([]messageKey, 0, len(keys))
		for _, key := range keys {
			row, err := messageProjection(ctx, q, key, budget)
			if err != nil {
				var limit *ResourceLimitError
				if includeContext && len(admitted) > 0 && errors.As(err, &limit) && limit.Limit == "result bytes" {
					result.PageReduced = true
					break
				}
				return MessageList{}, err
			}
			selected, ok, err := policy(row, true, len(admitted) == 0)
			if err != nil {
				return MessageList{}, err
			}
			if !ok {
				if !includeContext || len(admitted) == 0 {
					return MessageList{}, limitErr("reply bytes")
				}
				result.PageReduced = true
				break
			}
			preview := []string(nil)
			if selected.Text != row.Text {
				preview = append(preview, "content")
			}
			if selected.Caption != row.Caption {
				preview = append(preview, "caption")
			}
			if len(preview) > 0 && (!includeContext || len(admitted) != 0) {
				return MessageList{}, limitErr("reply bytes")
			}
			result.Hits = append(result.Hits, MessageListHit{Message: selected, Before: []MessageView{}, After: []MessageView{}, PreviewFields: preview})
			admitted = append(admitted, key)
		}
		if includeContext {
			hits, touched, err := messageContext(ctx, q, admitted, counts, budget, func(row MessageView) (bool, error) {
				_, ok, err := policy(row, false, false)
				return ok, err
			})
			if err != nil {
				return MessageList{}, err
			}
			result.ContextRows = touched
			for i, hit := range hits {
				if hit.before.rows != nil {
					result.Hits[i].Before = hit.before.rows
				}
				if hit.after.rows != nil {
					result.Hits[i].After = hit.after.rows
				}
				result.Hits[i].BeforeReduced, result.Hits[i].AfterReduced = hit.before.reduced, hit.after.reduced
			}
		}
		return result, nil
	})
}

const (
	messageSearchVisits    = 400001
	messageSearchBatch     = 256
	messageSearchRawBytes  = 80 << 20
	messageSearchFoldBytes = 320 << 20
	messageSearchFieldFold = 2621440
)

type searchCandidate struct {
	key   messageKey
	match bool
}

type messageSearchStream struct {
	branch senderBranch
	cursor messageKey
	buffer []searchCandidate
	done   bool
}

type messageQueryCache struct {
	querier
	prepare interface {
		PrepareContext(context.Context, string) (*sql.Stmt, error)
	}
	statements map[string]*sql.Stmt
}

func (q *messageQueryCache) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	stmt := q.statements[query]
	if stmt == nil {
		var err error
		stmt, err = q.prepare.PrepareContext(ctx, query)
		if err != nil {
			return q.querier.QueryRowContext(ctx, query, args...)
		}
		q.statements[query] = stmt
	}
	return stmt.QueryRowContext(ctx, args...)
}

var errSearchBatchBytes = errors.New("search batch exceeds remaining byte budget")

// searchMessageKeys verifies a page before any result projection or context.
func searchMessageKeys(ctx context.Context, q querier, f MessageFilter, p Page) ([]messageKey, error) {
	if preparer, ok := q.(interface {
		PrepareContext(context.Context, string) (*sql.Stmt, error)
	}); ok {
		cache := &messageQueryCache{querier: q, prepare: preparer, statements: make(map[string]*sql.Stmt)}
		defer func() {
			for _, stmt := range cache.statements {
				_ = stmt.Close()
			}
		}()
		q = cache
	}
	if err := validatePage(p); err != nil {
		return nil, err
	}
	if err := validText("query", f.Query, maxSearchBytes); err != nil {
		return nil, err
	}
	if !f.After.IsZero() && !f.Before.IsZero() && !f.After.Before(f.Before) {
		return nil, nil
	}
	branches := []senderBranch{{}}
	if f.Sender != "" {
		ids, err := resolveContact(ctx, q, f.Sender)
		if err != nil {
			return nil, err
		}
		branches = ids.branches()
	}
	streams := make([]messageSearchStream, len(branches))
	for i, b := range branches {
		streams[i] = messageSearchStream{branch: b, cursor: messageKey{time: math.MaxInt64}}
	}
	visits, raw, folded := 0, 0, 0
	needle := fold(f.Query)
	var out []messageKey
	matched := 0
	var previous *messageKey
	for {
		for i := range streams {
			s := &streams[i]
			for len(s.buffer) == 0 && !s.done {
				remaining := messageSearchVisits - visits
				if remaining <= 0 {
					return nil, limitErr("search work")
				}
				n := min(messageSearchBatch, remaining)
				if p.Limit == 1 && p.Offset == 0 {
					n = 1
				}
				var candidates []searchCandidate
				var next messageKey
				var count, rawUsed, foldUsed int
				var err error
				for {
					candidates, next, count, rawUsed, foldUsed, err = searchMessageBatch(ctx, q, f, s.branch, s.cursor, n, needle, messageSearchRawBytes-raw, messageSearchFoldBytes-folded)
					if !errors.Is(err, errSearchBatchBytes) {
						break
					}
					visits += count
					if n == 1 || visits >= messageSearchVisits {
						return nil, limitErr("search work")
					}
					n = min(n/2, messageSearchVisits-visits)
				}
				if err != nil {
					return nil, err
				}
				visits += count
				raw += rawUsed
				folded += foldUsed
				s.buffer = candidates
				s.done = count < n
				if count > 0 {
					s.cursor = next
				}
			}
		}
		best := -1
		for i := range streams {
			if len(streams[i].buffer) > 0 && (best < 0 || searchKeyGreater(streams[i].buffer[0].key, streams[best].buffer[0].key)) {
				best = i
			}
		}
		if best < 0 {
			return out, nil
		}
		candidate := streams[best].buffer[0]
		streams[best].buffer = streams[best].buffer[1:]
		if previous != nil && *previous == candidate.key {
			continue
		}
		key := candidate.key
		previous = &key
		if !candidate.match {
			continue
		}
		if matched >= p.Offset {
			out = append(out, key)
		}
		matched++
		if len(out) == p.Limit {
			return out, nil
		}
	}
}

func searchKeyGreater(a, b messageKey) bool {
	if a.time != b.time {
		return a.time > b.time
	}
	if a.chatJID != b.chatJID {
		return a.chatJID > b.chatJID
	}
	return a.id > b.id
}

func searchMessageBatch(ctx context.Context, q querier, f MessageFilter, branch senderBranch, cursor messageKey, limit int, needle string, rawLeft, foldLeft int) ([]searchCandidate, messageKey, int, int, int, error) {
	index, order, boundary := "messages_time", "messages.time DESC,messages.chat_jid DESC,messages.id DESC", "(time,chat_jid,id)<(?1,?2,?3)"
	var where []string
	if f.ChatJID != "" {
		index, order, boundary = "messages_chat_time", "messages.time DESC,messages.id DESC", "(time,id)<(?1,?3)"
		where = append(where, "chat_jid=?8")
	}
	if branch.column != "" {
		if f.ChatJID == "" {
			if branch.column == "sender_phone" {
				index = "messages_sender_time"
			} else {
				index = "messages_sender_jid"
			}
		} else {
			if branch.column == "sender_phone" {
				index = "messages_chat_sender_phone"
			} else {
				index = "messages_chat_sender_jid"
			}
		}
		where = append(where, branch.column+"=?9")
	}
	where = append(where, boundary, "?10 IS NOT NULL", "?11 IS NOT NULL")
	if !f.After.IsZero() {
		where = append(where, "time>?10")
	}
	if !f.Before.IsZero() {
		where = append(where, "time<?11")
	}
	keySQL := `SELECT time,CASE WHEN octet_length(chat_jid)<=256 THEN chat_jid END AS chat_jid,
	 CASE WHEN octet_length(id)<=256 THEN id END AS id,rowid AS rid FROM messages INDEXED BY ` + index + " WHERE " + strings.Join(where, " AND ") + " ORDER BY " + order + " LIMIT ?4"
	// Aggregate the bounded batch in the first SQLite step; no iterator retains a snapshot.
	query := `WITH keys AS MATERIALIZED (` + keySQL + `), lengths AS MATERIALIZED (
	 SELECT k.*, CASE WHEN ?7='' THEN 0 ELSE octet_length(m.text)+octet_length(m.caption) END AS raw,
	 CASE WHEN ?7='' THEN 0 ELSE octet_length(m.text_fold)+octet_length(m.caption_fold) END AS folded,
	 octet_length(k.chat_jid)<=256 AND octet_length(k.id)<=256 AND typeof(k.chat_jid)='text' AND typeof(k.id)='text' AND typeof(k.time)='integer'
	 AND (?7='' OR (typeof(m.text)='text' AND typeof(m.caption)='text' AND typeof(m.text_fold)='text' AND typeof(m.caption_fold)='text'
	 AND octet_length(m.text)<=262144 AND octet_length(m.caption)<=262144 AND octet_length(m.text_fold)<=2621440 AND octet_length(m.caption_fold)<=2621440)) AS valid
	 FROM keys k CROSS JOIN messages m ON m.rowid=k.rid
	), charge AS MATERIALIZED (
	 SELECT count(*) AS n,coalesce(sum(raw),0) AS raw,coalesce(sum(folded),0) AS folded,coalesce(min(valid),1) AS valid FROM lengths
	)
	SELECT n,raw,folded,valid,
	 CASE WHEN valid=1 AND raw<=?5 AND folded<=?6 THEN coalesce((SELECT json_array(time,hex(CAST(chat_jid AS BLOB)),hex(CAST(id AS BLOB))) FROM keys ORDER BY time,chat_jid,id LIMIT 1),'[]') ELSE '[]' END,
	 CASE WHEN valid=1 AND raw<=?5 AND folded<=?6 THEN
	 (SELECT json_array(coalesce(min(CASE WHEN ?7='' THEN 1 WHEN octet_length(m.text)+octet_length(m.caption)+octet_length(m.text_fold)+octet_length(m.caption_fold)>4096
	 THEN mcp_valid_utf8(CAST(m.text AS BLOB),CAST(m.caption AS BLOB),CAST(m.text_fold AS BLOB),CAST(m.caption_fold AS BLOB))
	 WHEN m.text NOT GLOB '*[^ -~]*' AND m.caption NOT GLOB '*[^ -~]*' AND m.text_fold NOT GLOB '*[^ -~]*' AND m.caption_fold NOT GLOB '*[^ -~]*'
	 AND instr(m.text,char(0))=0 AND instr(m.caption,char(0))=0 AND instr(m.text_fold,char(0))=0 AND instr(m.caption_fold,char(0))=0
	 THEN 1 ELSE mcp_valid_utf8(CAST(m.text AS BLOB),CAST(m.caption AS BLOB),CAST(m.text_fold AS BLOB),CAST(m.caption_fold AS BLOB)) END),1),
	 json(coalesce(json_group_array(json_array(k.time,hex(CAST(k.chat_jid AS BLOB)),hex(CAST(k.id AS BLOB))) ORDER BY k.time DESC,k.chat_jid DESC,k.id DESC) FILTER (WHERE ?7='' OR instr(m.text_fold,?7)>0 OR instr(m.caption_fold,?7)>0),'[]')))
	 FROM keys k CROSS JOIN messages m ON m.rowid=k.rid) ELSE '[1,[]]' END FROM charge`
	var count, raw, folded, valid int
	var encoded, cursorJSON string
	err := q.QueryRowContext(ctx, query, cursor.time, cursor.chatJID, cursor.id, limit, rawLeft, foldLeft, needle, f.ChatJID, branch.value, f.After.UnixMilli(), beforeMillis(f.Before)).Scan(&count, &raw, &folded, &valid, &cursorJSON, &encoded)
	if err != nil {
		return nil, messageKey{}, 0, 0, 0, err
	}
	if valid != 1 {
		return nil, messageKey{}, 0, 0, 0, limitErr("search work")
	}
	if raw > rawLeft || folded > foldLeft {
		return nil, messageKey{}, count, 0, 0, errSearchBatchBytes
	}
	var batch []json.RawMessage
	var validUTF8 int
	var rows [][]json.RawMessage
	if json.Unmarshal([]byte(encoded), &batch) != nil || len(batch) != 2 || json.Unmarshal(batch[0], &validUTF8) != nil || validUTF8 != 1 || json.Unmarshal(batch[1], &rows) != nil || len(rows) > count {
		return nil, messageKey{}, 0, 0, 0, limitErr("message identity")
	}
	decode := func(row []json.RawMessage) (messageKey, error) {
		var key messageKey
		var chatHex, idHex string
		if len(row) != 3 || json.Unmarshal(row[0], &key.time) != nil || json.Unmarshal(row[1], &chatHex) != nil || json.Unmarshal(row[2], &idHex) != nil {
			return messageKey{}, limitErr("message identity")
		}
		chat, chatErr := hex.DecodeString(chatHex)
		id, idErr := hex.DecodeString(idHex)
		key.chatJID, key.id = string(chat), string(id)
		if chatErr != nil || idErr != nil || !utf8.ValidString(key.chatJID) || !utf8.ValidString(key.id) || validateID("id", key.id) != nil {
			return messageKey{}, limitErr("message identity")
		}
		if _, err := parseChatJID("chat_jid", key.chatJID); err != nil {
			return messageKey{}, limitErr("message identity")
		}
		return key, nil
	}
	next := messageKey{}
	if count > 0 {
		var rawCursor []json.RawMessage
		if json.Unmarshal([]byte(cursorJSON), &rawCursor) != nil {
			return nil, next, 0, 0, 0, limitErr("message identity")
		}
		next, err = decode(rawCursor)
		if err != nil {
			return nil, next, 0, 0, 0, err
		}
	}
	out := make([]searchCandidate, 0, len(rows))
	for _, row := range rows {
		key, err := decode(row)
		if err != nil {
			return nil, messageKey{}, 0, 0, 0, err
		}
		out = append(out, searchCandidate{key: key, match: true})
	}
	return out, next, count, raw, folded, nil
}
