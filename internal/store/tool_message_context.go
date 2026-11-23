package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"slices"
	"unicode/utf8"
)

// GetMessageContext resolves an exact pair, or a unique ID when chatJID is
// empty, and reads its context in the same snapshot. accept follows the same
// synchronous, non-reentrant contract as GetMessageExact.
func (r *ToolReader) GetMessageContext(ctx context.Context, chatJID, id string, counts Context, accept func(MessageView) (bool, error)) (MessageContextResult, error) {
	if chatJID != "" {
		chat, err := parseChatJID("chat_jid", chatJID)
		if err != nil {
			return MessageContextResult{}, err
		}
		chatJID = chat.String()
	}
	if err := validateID("id", id); err != nil {
		return MessageContextResult{}, err
	}
	if err := validateContext(counts); err != nil {
		return MessageContextResult{}, err
	}
	if accept == nil {
		return MessageContextResult{}, invalid("policy", "required")
	}
	return messageSnapshot(ctx, r, func(ctx context.Context, q querier) (MessageContextResult, error) {
		if chatJID == "" {
			// Aggregate only the indexed sentinel-sized range in the first step.
			var encoded string
			var count, valid int
			err := q.QueryRowContext(ctx, `WITH keys AS MATERIALIZED (
				SELECT chat_jid FROM messages INDEXED BY messages_id WHERE id = ? LIMIT 101
			) SELECT count(*), coalesce(min(typeof(chat_jid) = 'text' AND octet_length(chat_jid) <= 256), 1),
				coalesce(json_group_array(CASE WHEN typeof(chat_jid) = 'text' AND octet_length(chat_jid) <= 256 THEN hex(CAST(chat_jid AS BLOB)) END), '[]') FROM keys`, id).Scan(&count, &valid, &encoded)
			if err != nil {
				return MessageContextResult{}, err
			}
			if count > maxAmbiguousIDs {
				return MessageContextResult{}, limitErr("matching chats")
			}
			if valid != 1 {
				return MessageContextResult{}, limitErr("message identity")
			}
			var values []string
			if json.Unmarshal([]byte(encoded), &values) != nil || len(values) != count {
				return MessageContextResult{}, limitErr("message identity")
			}
			chats := make([]string, 0, count)
			for _, value := range values {
				b, err := hex.DecodeString(value)
				chat := string(b)
				if err != nil || !utf8.ValidString(chat) {
					return MessageContextResult{}, limitErr("message identity")
				}
				if _, err := parseChatJID("chat_jid", chat); err != nil {
					return MessageContextResult{}, limitErr("message identity")
				}
				chats = append(chats, chat)
			}
			if count == 0 {
				return MessageContextResult{}, &NotFoundError{ID: id}
			}
			if count > 1 {
				slices.Sort(chats)
				return MessageContextResult{}, &AmbiguousIDError{ID: id, ChatJIDs: chats}
			}
			chatJID = chats[0]
		}
		budget := &resultBudget{}
		anchor, err := messageProjection(ctx, q, messageKey{chatJID: chatJID, id: id}, budget)
		if err != nil {
			return MessageContextResult{}, err
		}
		ok, err := accept(anchor)
		if err != nil {
			return MessageContextResult{}, err
		}
		if !ok {
			return MessageContextResult{}, limitErr("reply bytes")
		}
		hits, _, err := messageContext(ctx, q, []messageKey{{chatJID: anchor.ChatJID, id: anchor.ID, time: anchor.Time.UnixMilli()}}, counts, budget, accept)
		if err != nil {
			return MessageContextResult{}, err
		}
		result := MessageContextResult{Message: anchor, Before: hits[0].before.rows, After: hits[0].after.rows,
			BeforeReduced: hits[0].before.reduced, AfterReduced: hits[0].after.reduced}
		if result.Before == nil {
			result.Before = []MessageView{}
		}
		if result.After == nil {
			result.After = []MessageView{}
		}
		return result, nil
	})
}
