package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// messageProjection reads only guarded public fields at one indexed key.
func messageProjection(ctx context.Context, q querier, k messageKey, budget *resultBudget) (MessageView, error) {
	badNames := `(EXISTS (SELECT 1 FROM whatsmeow_contacts WHERE our_jid = ` + currentDevice +
		` AND (their_jid = m.sender_jid OR their_jid = (` + counterpartJID + `)) AND (` +
		`coalesce(octet_length(full_name), 0) > 1024 OR coalesce(octet_length(first_name), 0) > 1024 OR ` +
		`coalesce(octet_length(push_name), 0) > 1024 OR coalesce(octet_length(business_name), 0) > 1024 OR ` +
		`typeof(full_name) NOT IN ('text', 'null') OR typeof(first_name) NOT IN ('text', 'null') OR ` +
		`typeof(push_name) NOT IN ('text', 'null') OR typeof(business_name) NOT IN ('text', 'null'))) OR ` +
		`EXISTS (SELECT 1 FROM chats WHERE jid = m.sender_jid AND name_quality >= 2 AND (octet_length(name) > 1024 OR typeof(name) <> 'text')))`
	senderName := `CASE WHEN m.from_me = 1 OR ` + badNames + ` THEN '' ELSE coalesce(` +
		contactNameOf(`m.sender_jid`) + `, ` + contactNameOf(`(`+counterpartJID+`)`) +
		`, (SELECT name FROM chats WHERE jid = m.sender_jid AND name_quality >= 2), '') END`
	// The length check runs in the same first step as the projection, so an
	// over-budget row never transfers its result fields into Go.
	fitExpression := `(octet_length(m.id) + octet_length(m.chat_jid) + octet_length(c.name) + ` +
		`octet_length(m.sender_jid) + octet_length(m.sender_phone) + octet_length(` + senderName + `) + ` +
		`octet_length(m.text) + octet_length(m.caption) + octet_length(m.media_type) + 64 <= ?3)`
	fits := `(SELECT ok FROM message_budget)`
	guarded := strings.Replace(messageSelect, `CASE WHEN m.from_me = 1 THEN '' ELSE coalesce(`,
		`CASE WHEN NOT `+fits+` OR m.from_me = 1 OR `+badNames+` THEN '' ELSE coalesce(`, 1)
	guarded = strings.Replace(guarded, `SELECT m.id, m.chat_jid, c.name, m.sender_jid, m.sender_phone, `,
		`SELECT CASE WHEN `+fits+` AND octet_length(m.id) <= 256 THEN m.id ELSE '' END, `+
			`CASE WHEN `+fits+` AND octet_length(m.chat_jid) <= 256 THEN m.chat_jid ELSE '' END, `+
			`CASE WHEN `+fits+` AND octet_length(c.name) <= 1024 THEN c.name ELSE '' END, `+
			`CASE WHEN `+fits+` AND octet_length(m.sender_jid) <= 256 THEN m.sender_jid ELSE '' END, `+
			`CASE WHEN `+fits+` AND octet_length(m.sender_phone) <= 256 THEN m.sender_phone ELSE '' END, `, 1)
	guarded = strings.Replace(guarded, `m.text, m.caption, m.media_type `,
		`CASE WHEN `+fits+` AND octet_length(m.text) <= 262144 THEN m.text END, `+
			`CASE WHEN `+fits+` AND octet_length(m.caption) <= 262144 THEN m.caption END, `+
			`CASE WHEN `+fits+` AND octet_length(m.media_type) <= 8 THEN m.media_type ELSE '' END, `+
			`octet_length(m.text), octet_length(m.caption), `+
			`CASE WHEN m.from_me = 1 THEN 0 ELSE `+badNames+` END OR octet_length(c.name) > 1024, `+
			`(octet_length(m.id) > 256 OR octet_length(m.chat_jid) > 256 OR `+
			`octet_length(m.sender_jid) > 256 OR octet_length(m.sender_phone) > 256 OR octet_length(m.media_type) > 8 OR `+
			`typeof(m.id) <> 'text' OR typeof(m.chat_jid) <> 'text' OR typeof(c.name) <> 'text' OR `+
			`typeof(m.sender_jid) <> 'text' OR typeof(m.sender_phone) <> 'text' OR typeof(m.text) <> 'text' OR `+
			`typeof(m.caption) <> 'text' OR typeof(m.media_type) <> 'text' OR typeof(m.time) <> 'integer' OR typeof(m.from_me) <> 'integer'), `+fits+` `, 1)
	var v MessageView
	var ms, textBytes, captionBytes int64
	var fromMe int
	var text, caption sql.NullString
	var badName, badIdentity, fitsBudget bool
	budgetQuery := `WITH message_budget(ok) AS MATERIALIZED (SELECT ` + fitExpression +
		` FROM messages m JOIN chats c ON c.jid = m.chat_jid WHERE m.chat_jid = ?1 AND m.id = ?2) `
	err := q.QueryRowContext(ctx, budgetQuery+guarded+`WHERE m.chat_jid = ?1 AND m.id = ?2`, k.chatJID, k.id, maxResultBytes-budget.used).
		Scan(&v.ID, &v.ChatJID, &v.ChatName, &v.SenderJID, &v.SenderPhone, &v.SenderName,
			&ms, &fromMe, &text, &caption, &v.MediaType, &textBytes, &captionBytes, &badName, &badIdentity, &fitsBudget)
	if err != nil {
		return MessageView{}, err
	}
	if badName || badIdentity || textBytes > maxTextBytes || captionBytes > maxCaptionBytes {
		return MessageView{}, limitErr("message fields")
	}
	if !fitsBudget {
		return MessageView{}, limitErr("result bytes")
	}
	if !text.Valid || !caption.Valid {
		return MessageView{}, limitErr("message fields")
	}
	v.Time, v.FromMe, v.Text, v.Caption = time.UnixMilli(ms).UTC(), fromMe == 1, text.String, caption.String
	if fromMe != 0 && fromMe != 1 || v.Time.Year() < 0 || v.Time.Year() > 9999 || v.ID == "" || v.ChatJID == "" || v.SenderJID == "" {
		return MessageView{}, limitErr("message fields")
	}
	if validateID("id", v.ID) != nil {
		return MessageView{}, limitErr("message fields")
	}
	if _, err := parseChatJID("chat_jid", v.ChatJID); err != nil {
		return MessageView{}, limitErr("message fields")
	}
	if _, err := parseSenderJID("sender", v.SenderJID); err != nil {
		return MessageView{}, limitErr("message fields")
	}
	for _, field := range []string{v.ID, v.ChatJID, v.ChatName, v.SenderJID, v.SenderPhone, v.SenderName, v.Text, v.Caption, v.MediaType} {
		if !utf8.ValidString(field) {
			return MessageView{}, limitErr("message fields")
		}
	}
	switch v.MediaType {
	case "", "image", "video", "audio", "document":
	default:
		return MessageView{}, limitErr("message fields")
	}
	if err := budget.addMessage(v); err != nil {
		return MessageView{}, err
	}
	return v, nil
}

type messageContextSide struct {
	next    *messageKey
	rows    []MessageView
	want    int
	reduced bool
}

type messageContextHit struct {
	before, after messageContextSide
}

// messageContext expands only admitted anchors. accept is synchronous and must
// neither retain rejected projections nor reenter the store.
func messageContext(ctx context.Context, q querier, anchors []messageKey, counts Context, budget *resultBudget, accept func(MessageView) (bool, error)) ([]messageContextHit, int, error) {
	if err := validateContext(counts); err != nil {
		return nil, 0, err
	}
	if len(anchors) > maxListSize || accept == nil {
		return nil, 0, invalid("context", "invalid selection")
	}
	result := make([]messageContextHit, len(anchors))
	touched := 0
	probe := func(k messageKey, before bool) (*messageKey, error) {
		operator, order := ">", "ASC"
		if before {
			operator, order = "<", "DESC"
		}
		var next messageKey
		var id sql.NullString
		err := q.QueryRowContext(ctx, `SELECT time, CASE WHEN octet_length(id) <= 256 THEN id END FROM messages WHERE chat_jid = ? AND (time, id) `+operator+` (?, ?) ORDER BY time `+order+`, id `+order+` LIMIT 1`, k.chatJID, k.time, k.id).Scan(&next.time, &id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		touched++
		if !id.Valid || !utf8.ValidString(id.String) || validateID("id", id.String) != nil {
			return nil, limitErr("message identity")
		}
		next.chatJID, next.id = k.chatJID, id.String
		return &next, nil
	}
	for _, k := range anchors {
		if !utf8.ValidString(k.chatJID) || !utf8.ValidString(k.id) || validateID("id", k.id) != nil {
			return nil, touched, limitErr("message identity")
		}
		if _, err := parseChatJID("chat_jid", k.chatJID); err != nil {
			return nil, touched, limitErr("message identity")
		}
	}
	for i, k := range anchors {
		result[i].before.want, result[i].after.want = counts.Before, counts.After
		for _, item := range []struct {
			side   *messageContextSide
			before bool
		}{{&result[i].before, true}, {&result[i].after, false}} {
			if item.side.want == 0 {
				continue
			}
			if touched >= maxContextRows {
				return nil, touched, limitErr("context rows")
			}
			var err error
			item.side.next, err = probe(k, item.before)
			if err != nil {
				return nil, touched, err
			}
		}
	}
	for i := range result {
		for {
			progress := false
			for _, item := range []struct {
				side   *messageContextSide
				before bool
			}{{&result[i].before, true}, {&result[i].after, false}} {
				s := item.side
				if s.next == nil || s.reduced || len(s.rows) == s.want {
					continue
				}
				progress = true
				needed := 1
				if len(s.rows)+1 < s.want {
					needed++
				}
				if touched+needed > maxContextRows {
					s.reduced = true
					continue
				}
				key := *s.next
				v, err := messageProjection(ctx, q, key, budget)
				if err != nil {
					var limit *ResourceLimitError
					if errors.As(err, &limit) && limit.Limit == "result bytes" {
						touched++
						s.reduced = true
						continue
					}
					return nil, touched, err
				}
				touched++
				accepted, err := accept(v)
				if err != nil {
					return nil, touched, err
				}
				if !accepted {
					s.reduced = true
					continue
				}
				var next *messageKey
				if needed == 2 {
					next, err = probe(key, item.before)
					if err != nil {
						return nil, touched, err
					}
				}
				s.rows = append(s.rows, v)
				s.next = next
			}
			if !progress {
				break
			}
		}
		slices.Reverse(result[i].before.rows)
	}
	return result, touched, nil
}
