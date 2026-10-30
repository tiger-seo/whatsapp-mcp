package store

import (
	"context"
	"database/sql"
)

// entryName is one device-store entry's name in the order full, first, push, business; empty when it has none.
const entryName = `coalesce(nullif(c.full_name, ''), nullif(c.first_name, ''), nullif(c.push_name, ''), nullif(c.business_name, ''), '')`

// SearchContacts lists the people of the current device's contact store whose name or phone number contains
// query, ordered by folded name. A person with both a phone entry and a LID entry is one row, keyed by the phone
// JID, and either entry can match. Without a device the result is empty.
//
// The text is matched after the entries are grouped into people: filtering entries first would give a person
// matched only through the LID entry that entry's name.
func (s *Store) SearchContacts(ctx context.Context, query string, p Page) ([]Contact, error) {
	if err := validatePage(p); err != nil {
		return nil, s.report(newOpID(), err)
	}
	if err := validText("query", query, maxSearchBytes); err != nil {
		return nil, s.report(newOpID(), err)
	}
	text := fold(query)
	// A name never matches through an empty digit string: the phone is searched only for a whole phone number.
	digits, err := NormalizePhone(query)
	if err != nil {
		digits = ""
	}

	// The map is joined on its own key, so each LID entry is one index search. The ?1 and ?2 values are the folded
	// text and the digits.
	const entries = `
		SELECT
		  CASE WHEN c.their_jid LIKE '%@lid' THEN coalesce(m.pn || '@s.whatsapp.net', c.their_jid) ELSE c.their_jid END AS person,
		  c.their_jid LIKE '%@lid' AS is_lid,
		  CASE WHEN c.their_jid LIKE '%@lid' THEN coalesce(m.pn, '')
		       WHEN c.their_jid LIKE '%@s.whatsapp.net' THEN substr(c.their_jid, 1, instr(c.their_jid, '@') - 1)
		       ELSE '' END AS phone,
		  ` + entryName + ` AS name,
		  (instr(mcp_fold(coalesce(c.full_name, '')), ?1) > 0 OR instr(mcp_fold(coalesce(c.first_name, '')), ?1) > 0 OR
		   instr(mcp_fold(coalesce(c.push_name, '')), ?1) > 0 OR instr(mcp_fold(coalesce(c.business_name, '')), ?1) > 0 OR
		   (?2 <> '' AND instr(CASE WHEN c.their_jid LIKE '%@lid' THEN coalesce(m.pn, '')
		       WHEN c.their_jid LIKE '%@s.whatsapp.net' THEN substr(c.their_jid, 1, instr(c.their_jid, '@') - 1)
		       ELSE '' END, ?2) > 0)) AS hit
		FROM whatsmeow_contacts c
		LEFT JOIN whatsmeow_lid_map m
		  ON c.their_jid LIKE '%@lid' AND m.lid = substr(c.their_jid, 1, length(c.their_jid) - 4)
		WHERE c.our_jid = ` + currentDevice + ` AND c.their_jid NOT LIKE '%@g.us'`

	const people = `
		SELECT person, max(phone) AS phone,
		  coalesce(nullif(max(CASE WHEN is_lid = 0 THEN name END), ''), max(CASE WHEN is_lid = 1 THEN name END), '') AS name
		FROM (` + entries + `)
		GROUP BY person
		HAVING ?1 = '' OR max(hit)`

	var out []Contact
	err = s.snapshot(ctx, "SearchContacts", func(ctx context.Context, q querier) error {
		rows, err := q.QueryContext(ctx,
			`SELECT person, phone, name FROM (`+people+`) ORDER BY mcp_fold(name), person LIMIT ?3 OFFSET ?4`,
			text, digits, p.Limit, p.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		budget := &resultBudget{}
		for rows.Next() {
			var c Contact
			var phone, name sql.NullString
			if err := rows.Scan(&c.JID, &phone, &name); err != nil {
				return err
			}
			c.Phone, c.Name = phone.String, name.String
			if err := budget.add(len(c.JID) + len(c.Phone) + len(c.Name) + 48); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
