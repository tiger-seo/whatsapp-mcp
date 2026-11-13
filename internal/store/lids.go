package store

import (
	"context"
	"database/sql"
	"errors"
)

// LIDMapping is one row of the device's LID map, as full JIDs.
type LIDMapping struct{ LIDJID, PhoneJID string }

// PhoneForLID returns the phone JID mapped to lidJID in the LID map. A device part of lidJID is ignored. The map
// has no device column: a data directory holds one account, so it is that account's map. ok is false when there
// is no mapping. It only reads; ctx bounds it.
func (s *Store) PhoneForLID(ctx context.Context, lidJID string) (phoneJID string, ok bool, err error) {
	lid, err := parseJID("lid_jid", lidJID, []string{serverLID}, true)
	if err != nil {
		return "", false, s.report(newOpID(), err)
	}
	var pn sql.NullString
	err = s.snapshot(ctx, "PhoneForLID", func(ctx context.Context, q querier) error {
		err := q.QueryRowContext(ctx,
			`SELECT CASE WHEN length(pn) <= ? THEN pn END FROM whatsmeow_lid_map WHERE lid = ?`, maxJIDBytes, lid.User).Scan(&pn)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err == nil && !pn.Valid {
			return limitErr("result bytes")
		}
		return err
	})
	if err != nil || !pn.Valid {
		return "", false, err
	}
	return pn.String + "@" + serverUser, true, nil
}

// MappedLIDsWithData returns up to limit (1 to 200) mappings whose LID is greater than afterLID, in key order, and
// that have a chat or a message sender stored under the LID. afterLID is a LID JID, or "" to start at the
// beginning; the last LIDJID of one page is the cursor of the next.
func (s *Store) MappedLIDsWithData(ctx context.Context, afterLID string, limit int) ([]LIDMapping, error) {
	if limit < 1 || limit > maxListSize {
		return nil, s.report(newOpID(), invalid("limit", "must be 1 to 200"))
	}
	after := ""
	if afterLID != "" {
		lid, err := parseJID("after_lid", afterLID, []string{serverLID}, false)
		if err != nil {
			return nil, s.report(newOpID(), err)
		}
		after = lid.User
	}
	var out []LIDMapping
	err := s.snapshot(ctx, "MappedLIDsWithData", func(ctx context.Context, q querier) error {
		// The map is walked on its key; each row costs one primary-key search of chats and one search of the
		// messages_sender_jid index.
		rows, err := q.QueryContext(ctx, `
			SELECT m.lid, m.pn FROM whatsmeow_lid_map m
			WHERE m.lid > ?1 AND length(m.lid) <= ?3 AND length(m.pn) <= ?3
			  AND (EXISTS (SELECT 1 FROM chats WHERE jid = m.lid || '@lid')
			    OR EXISTS (SELECT 1 FROM messages WHERE sender_jid = m.lid || '@lid'))
			ORDER BY m.lid LIMIT ?2`, after, limit, maxJIDBytes-len("@s.whatsapp.net"))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var lid, pn string
			if err := rows.Scan(&lid, &pn); err != nil {
				return err
			}
			out = append(out, LIDMapping{LIDJID: lid + "@" + serverLID, PhoneJID: pn + "@" + serverUser})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
