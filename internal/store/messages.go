package store

import (
	"context"
	"database/sql"
	"time"
)

// Message is one WhatsApp message as ingest stores it.
type Message struct {
	ChatJID   string // s.whatsapp.net, g.us or lid
	ID        string
	SenderJID string // s.whatsapp.net or lid; a device part (":12") is stripped
	Time      time.Time
	FromMe    bool
	Text      string
	Caption   string
	MediaType string    // "", "image", "video", "audio" or "document"
	Media     MediaMeta // the zero value holds no media metadata
}

// MediaMeta is what a download of the message's media needs.
type MediaMeta struct {
	URL, DirectPath           string
	MediaKey                  []byte // 32 bytes when present
	FileSHA256, FileEncSHA256 []byte // 32 bytes when present
	FileLength                *int64 // nil = absent
	MIMEType, FileName        string
}

type storedMessage struct {
	chatJID, id, senderJID, senderPhone string
	chatPhone                           string
	time                                int64
	m                                   Message
	textFold, captionFold               string
}

// validate checks every field before any SQL runs and returns the stored form, with the number of field bytes
// it counts toward the batch limit.
func (m Message) validate() (storedMessage, int, error) {
	chat, err := parseChatJID("chat_jid", m.ChatJID)
	if err != nil {
		return storedMessage{}, 0, err
	}
	if err := validateID("id", m.ID); err != nil {
		return storedMessage{}, 0, err
	}
	sender, err := parseSenderJID("sender_jid", m.SenderJID)
	if err != nil {
		return storedMessage{}, 0, err
	}
	if err := validateTime("time", m.Time); err != nil {
		return storedMessage{}, 0, err
	}
	if err := validText("text", m.Text, maxTextBytes); err != nil {
		return storedMessage{}, 0, err
	}
	if err := validText("caption", m.Caption, maxCaptionBytes); err != nil {
		return storedMessage{}, 0, err
	}
	if err := validateMedia(m.MediaType, m.Media); err != nil {
		return storedMessage{}, 0, err
	}
	size := len(m.ChatJID) + len(m.ID) + len(m.SenderJID) + len(m.Text) + len(m.Caption) + len(m.MediaType) +
		len(m.Media.URL) + len(m.Media.DirectPath) + len(m.Media.MediaKey) + len(m.Media.FileSHA256) +
		len(m.Media.FileEncSHA256) + len(m.Media.MIMEType) + len(m.Media.FileName)
	return storedMessage{
		chatJID: chat.String(), chatPhone: phoneOf(chat), id: m.ID,
		senderJID: sender.String(), senderPhone: phoneOf(sender),
		time: m.Time.UnixMilli(), m: m, textFold: fold(m.Text), captionFold: fold(m.Caption),
	}, size, nil
}

// StoreMessage stores one message; see StoreMessages.
func (s *Store) StoreMessage(ctx context.Context, m Message) error {
	return s.StoreMessages(ctx, []Message{m})
}

const upsertMessage = `
INSERT INTO messages (chat_jid, id, sender_jid, sender_phone, time, from_me, text, text_fold, caption, caption_fold,
    media_type, media_url, media_direct_path, media_key, file_sha256, file_enc_sha256, file_length, mime_type, file_name)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (chat_jid, id) DO UPDATE SET
    sender_jid        = excluded.sender_jid,
    sender_phone      = excluded.sender_phone,
    time              = excluded.time,
    from_me           = excluded.from_me,
    text              = excluded.text,
    text_fold         = excluded.text_fold,
    caption           = excluded.caption,
    caption_fold      = excluded.caption_fold,
    media_type        = excluded.media_type,
    media_url         = coalesce(nullif(excluded.media_url, ''), media_url),
    media_direct_path = coalesce(nullif(excluded.media_direct_path, ''), media_direct_path),
    media_key         = CASE WHEN length(excluded.media_key) > 0 THEN excluded.media_key ELSE media_key END,
    file_sha256       = CASE WHEN length(excluded.file_sha256) > 0 THEN excluded.file_sha256 ELSE file_sha256 END,
    file_enc_sha256   = CASE WHEN length(excluded.file_enc_sha256) > 0 THEN excluded.file_enc_sha256 ELSE file_enc_sha256 END,
    file_length       = coalesce(excluded.file_length, file_length),
    mime_type         = coalesce(nullif(excluded.mime_type, ''), mime_type),
    file_name         = coalesce(nullif(excluded.file_name, ''), file_name)`

// StoreMessages stores 1 to 1,000 messages of at most 16 MiB of field bytes in one transaction, all or nothing.
// Chats are created as needed and their last activity moves forward. Storing a message again replaces it, except
// that an empty media metadata field never overwrites a stored non-empty one.
func (s *Store) StoreMessages(ctx context.Context, msgs []Message) error {
	if len(msgs) == 0 || len(msgs) > maxBatchMessages {
		return s.report(newOpID(), limitOrInvalid(len(msgs) > maxBatchMessages, "messages", "batch"))
	}
	stored := make([]storedMessage, len(msgs))
	total := 0
	for i, m := range msgs {
		sm, size, err := m.validate()
		if err != nil {
			return s.report(newOpID(), err)
		}
		if total += size; total > maxBatchBytes {
			return s.report(newOpID(), limitErr("batch bytes"))
		}
		stored[i] = sm
	}
	// The newest time per chat, in first-seen order, so each chat is touched once per batch.
	var order []string
	newest := map[string]int64{}
	phones := map[string]string{}
	for _, sm := range stored {
		if t, ok := newest[sm.chatJID]; !ok {
			order = append(order, sm.chatJID)
			newest[sm.chatJID], phones[sm.chatJID] = sm.time, sm.chatPhone
		} else if sm.time > t {
			newest[sm.chatJID] = sm.time
		}
	}
	return s.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for i, jid := range order {
			if _, err := tx.ExecContext(ctx, "INSERT INTO chats (jid, phone) VALUES (?, ?) ON CONFLICT (jid) DO NOTHING", jid, phones[jid]); err != nil {
				return err
			}
			if err := touchChat(ctx, tx, jid, newest[jid]); err != nil {
				return err
			}
			if i == 0 && s.hooks.afterChatInsert != nil {
				if err := s.hooks.afterChatInsert(); err != nil {
					return err
				}
			}
		}
		stmt, err := tx.PrepareContext(ctx, upsertMessage)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, sm := range stored {
			m := sm.m
			var length any
			if m.Media.FileLength != nil {
				length = *m.Media.FileLength
			}
			if _, err := stmt.ExecContext(ctx, sm.chatJID, sm.id, sm.senderJID, sm.senderPhone, sm.time, boolInt(m.FromMe),
				m.Text, sm.textFold, m.Caption, sm.captionFold, m.MediaType,
				nullable(m.Media.URL), nullable(m.Media.DirectPath), nullableBytes(m.Media.MediaKey),
				nullableBytes(m.Media.FileSHA256), nullableBytes(m.Media.FileEncSHA256), length,
				nullable(m.Media.MIMEType), nullable(m.Media.FileName)); err != nil {
				return err
			}
		}
		return nil
	})
}

func limitOrInvalid(tooMany bool, limit, field string) error {
	if tooMany {
		return limitErr(limit)
	}
	return invalid(field, "must not be empty")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
