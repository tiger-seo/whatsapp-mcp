package store

import (
	"context"
	"database/sql"
	"time"
)

// NameQuality ranks where a chat name came from. A higher quality always wins; see UpdateChats.
type NameQuality int

const (
	NameNone     NameQuality = iota // no name
	NameFallback                    // derived from a number or an identifier
	NameReal                        // push name, business name, group subject, history-sync display name
	NameSaved                       // address-book full or first name
)

// ChatUpdate offers a chat's name and last activity. Name "" offers no name, a zero LastActivity none.
type ChatUpdate struct {
	JID          string
	Name         string
	NameQuality  NameQuality // required when Name is set
	NameTime     time.Time   // required when Name is set
	LastActivity time.Time
}

type chatUpdate struct {
	jid, phone, name, fold string
	quality                int
	nameTime               int64
	activity               int64 // 0 = not offered
}

func (u ChatUpdate) validate() (chatUpdate, error) {
	jid, err := parseChatJID("chat_jid", u.JID)
	if err != nil {
		return chatUpdate{}, err
	}
	out := chatUpdate{jid: jid.String(), phone: phoneOf(jid)}
	if err := validText("name", u.Name, maxNameBytes); err != nil {
		return chatUpdate{}, err
	}
	if u.Name != "" {
		if u.NameQuality < NameFallback || u.NameQuality > NameSaved {
			return chatUpdate{}, invalid("name_quality", "not one of fallback, real, saved")
		}
		if u.NameTime.IsZero() {
			return chatUpdate{}, invalid("name_time", "required with a name")
		}
		if err := validateTime("name_time", u.NameTime); err != nil {
			return chatUpdate{}, err
		}
		out.name, out.fold, out.quality, out.nameTime = u.Name, fold(u.Name), int(u.NameQuality), u.NameTime.UnixMilli()
	}
	if !u.LastActivity.IsZero() {
		if err := validateTime("last_activity", u.LastActivity); err != nil {
			return chatUpdate{}, err
		}
		out.activity = u.LastActivity.UnixMilli()
	}
	return out, nil
}

// UpdateChat applies one update; see UpdateChats.
func (s *Store) UpdateChat(ctx context.Context, u ChatUpdate) error {
	return s.UpdateChats(ctx, []ChatUpdate{u})
}

// UpdateChats applies 1 to 1,000 chat updates in one transaction. A chat is created when it is missing. An
// offered name replaces the stored one only when its (quality, time, name) is greater, compared in that order and
// byte-wise on the name, so the result does not depend on the order in which offers arrive. Last activity only
// moves forward.
func (s *Store) UpdateChats(ctx context.Context, updates []ChatUpdate) error {
	if len(updates) == 0 || len(updates) > maxChatUpdates {
		return s.report(newOpID(), invalid("updates", "must hold 1 to 1000 updates"))
	}
	parsed := make([]chatUpdate, len(updates))
	for i, u := range updates {
		p, err := u.validate()
		if err != nil {
			return s.report(newOpID(), err)
		}
		parsed[i] = p
	}
	return s.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, u := range parsed {
			if err := applyChatUpdate(ctx, tx, u); err != nil {
				return err
			}
		}
		return nil
	})
}

func applyChatUpdate(ctx context.Context, tx *sql.Tx, u chatUpdate) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO chats (jid, phone) VALUES (?, ?) ON CONFLICT (jid) DO NOTHING", u.jid, u.phone); err != nil {
		return err
	}
	if u.name != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE chats SET name = ?1, name_fold = ?2, name_quality = ?3, name_time = ?4
			 WHERE jid = ?5 AND (?3, ?4, ?1) > (name_quality, coalesce(name_time, -1), name)`,
			u.name, u.fold, u.quality, u.nameTime, u.jid); err != nil {
			return err
		}
	}
	if u.activity != 0 {
		return touchChat(ctx, tx, u.jid, u.activity)
	}
	return nil
}

// touchChat moves a chat's last activity forward to at least t.
func touchChat(ctx context.Context, tx *sql.Tx, jid string, t int64) error {
	_, err := tx.ExecContext(ctx,
		"UPDATE chats SET last_message_time = max(coalesce(last_message_time, ?1), ?1) WHERE jid = ?2", t, jid)
	return err
}
