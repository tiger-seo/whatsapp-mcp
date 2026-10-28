package store

import (
	"context"
	"database/sql"
	"strings"
)

// MergeLID merges everything stored under lidJID into phoneJID in one transaction: the sender of every message
// sent as lidJID, in any chat, and the chat lidJID itself if it exists. Messages the two chats share keep the
// phone chat's copy. The LID chat's name is offered to the phone chat only when it is saved or real, since a
// fallback derived from a LID would name the person after an opaque number. Nothing left under lidJID is a no-op.
func (s *Store) MergeLID(ctx context.Context, lidJID, phoneJID string) error {
	lid, err := parseJID("lid_jid", lidJID, []string{serverLID}, false)
	if err != nil {
		return s.report(newOpID(), err)
	}
	phone, err := parseJID("phone_jid", phoneJID, []string{serverUser}, false)
	if err != nil {
		return s.report(newOpID(), err)
	}
	L, P := lid.String(), phone.String()
	digits := phoneOf(phone)

	return s.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"UPDATE messages SET sender_jid = ?, sender_phone = ? WHERE sender_jid = ?", P, digits, L); err != nil {
			return err
		}
		if s.hooks.afterSenderMerge != nil {
			if err := s.hooks.afterSenderMerge(); err != nil {
				return err
			}
		}

		var name string
		var quality int
		var nameTime, last sql.NullInt64
		err := tx.QueryRowContext(ctx,
			"SELECT name, name_quality, name_time, last_message_time FROM chats WHERE jid = ?", L).
			Scan(&name, &quality, &nameTime, &last)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO chats (jid, phone) VALUES (?, ?) ON CONFLICT (jid) DO NOTHING", P, digits); err != nil {
			return err
		}
		if last.Valid {
			if err := touchChat(ctx, tx, P, last.Int64); err != nil {
				return err
			}
		}
		if quality >= int(NameReal) && name != "" && nameTime.Valid {
			if err := applyChatUpdate(ctx, tx, chatUpdate{
				jid: P, phone: digits, name: name, fold: fold(name), quality: quality, nameTime: nameTime.Int64,
			}); err != nil {
				return err
			}
		}
		cols := strings.Join(messageColumns, ", ")
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO messages (chat_jid, "+cols+") SELECT ?, "+cols+" FROM messages WHERE chat_jid = ? "+
				"ON CONFLICT (chat_jid, id) DO NOTHING", P, L); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE chat_jid = ?", L); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM chats WHERE jid = ?", L)
		return err
	})
}

// messageColumns are every column of messages except chat_jid, in table order.
var messageColumns = []string{
	"id", "sender_jid", "sender_phone", "time", "from_me", "text", "text_fold", "caption", "caption_fold", "media_type",
	"media_url", "media_direct_path", "media_key", "file_sha256", "file_enc_sha256", "file_length", "mime_type", "file_name",
}
