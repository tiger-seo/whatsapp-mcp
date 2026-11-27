package store

import (
	"context"
	"database/sql"
	"math"
	"strings"
)

// GetLastInteraction selects and projects one verified interaction in a single
// snapshot. accept has the same synchronous, non-reentrant contract as
// GetMessageExact; rejection returns a reply-bytes limit, not a partial message.
func (r *ToolReader) GetLastInteraction(ctx context.Context, jid string, accept func(MessageView) (bool, error)) (MessageView, error) {
	if _, _, err := parseContact(jid); err != nil {
		return MessageView{}, err
	}
	if accept == nil {
		return MessageView{}, invalid("policy", "required")
	}
	return messageSnapshot(ctx, r, func(ctx context.Context, q querier) (MessageView, error) {
		ids, err := resolveContact(ctx, q, jid)
		if err != nil {
			return MessageView{}, err
		}
		phone, lid, err := ownIdentity(ctx, q)
		if err != nil {
			return MessageView{}, err
		}
		own := ids.phone != "" && ids.phone == phone || ids.lidJID != "" && lid != "" && ids.lidJID == lid+"@"+serverLID
		if own {
			if phone != "" {
				ids.phone, ids.phoneJID = phone, phone+"@"+serverUser
			}
			if lid != "" {
				ids.lidJID = lid + "@" + serverLID
			}
		}
		key, err := interactionKey(ctx, q, ids, own, messageSearchVisits)
		if err != nil {
			return MessageView{}, err
		}
		row, err := messageProjection(ctx, q, key, &resultBudget{})
		if err != nil {
			return MessageView{}, err
		}
		ok, err := accept(row)
		if err != nil {
			return MessageView{}, err
		}
		if !ok {
			return MessageView{}, limitErr("reply bytes")
		}
		return row, nil
	})
}

// interactionKey bounds all enumerated keys, including direct seeks and
// sender-stream lookahead. Sender predicates precede only the bounded batch;
// group and direction predicates are checked after enumeration.
func interactionKey(ctx context.Context, q querier, ids contactIDs, own bool, ceiling int) (messageKey, error) {
	var best messageKey
	found, visits := false, 0
	consider := func(k messageKey) {
		if !found || searchKeyGreater(k, best) {
			best, found = k, true
		}
	}
	cursor := messageKey{time: math.MaxInt64}
	for _, chat := range []string{ids.phoneJID, ids.lidJID} {
		if chat == "" {
			continue
		}
		if visits >= ceiling {
			return messageKey{}, limitErr("interaction work")
		}
		rows, _, count, _, _, err := searchMessageBatch(ctx, q, MessageFilter{ChatJID: chat}, senderBranch{}, cursor, 1, "", 0, 0)
		if err != nil {
			return messageKey{}, err
		}
		visits += count
		if count > 0 {
			consider(rows[0].key)
		}
	}
	if !own {
		for _, branch := range ids.branches() {
			cursor := cursor
			for {
				if visits >= ceiling {
					return messageKey{}, limitErr("interaction work")
				}
				n := min(messageSearchBatch, ceiling-visits)
				rows, next, count, _, _, err := searchMessageBatch(ctx, q, MessageFilter{}, branch, cursor, n, "", 0, 0)
				if err != nil {
					return messageKey{}, err
				}
				visits += count
				finished := false
				for _, candidate := range rows {
					k := candidate.key
					if found && !searchKeyGreater(k, best) {
						finished = true
						break
					}
					if !strings.HasSuffix(k.chatJID, "@"+serverGroup) {
						continue
					}
					var fromMe int
					if err := q.QueryRowContext(ctx, `SELECT from_me FROM messages WHERE chat_jid=? AND id=?`, k.chatJID, k.id).Scan(&fromMe); err != nil {
						return messageKey{}, err
					}
					if fromMe != 0 && fromMe != 1 {
						return messageKey{}, limitErr("message fields")
					}
					if fromMe == 0 {
						consider(k)
						finished = true
						break
					}
				}
				if finished || count < n {
					break
				}
				cursor = next
			}
		}
	}
	if !found {
		return messageKey{}, sql.ErrNoRows
	}
	return best, nil
}
