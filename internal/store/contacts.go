package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	const contactSource = `
		FROM whatsmeow_contacts c
		LEFT JOIN whatsmeow_lid_map m
		  ON m.lid = CASE WHEN octet_length(c.their_jid) <= 256 AND c.their_jid LIKE '%@lid'
		                  THEN substr(c.their_jid, 1, length(c.their_jid) - 4) END
		WHERE c.our_jid = ` + currentDevice + ` AND c.their_jid NOT LIKE '%@g.us'`
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
		       ELSE '' END, ?2) > 0)) AS hit` + contactSource

	const people = `
		SELECT person, max(phone) AS phone,
		  coalesce(nullif(max(CASE WHEN is_lid = 0 THEN name END), ''), max(CASE WHEN is_lid = 1 THEN name END), '') AS name
		FROM (` + entries + `)
		GROUP BY person
		HAVING ?1 = '' OR max(hit)`

	const bounded = `WITH pre AS (
		SELECT count(*) AS n, coalesce(sum(nb), 0) AS bytes, coalesce(max(fb), 0) AS longest,
		       coalesce(sum(ab), 0) AS allb, coalesce(max(jb), 0) AS longest_jid FROM (
		  SELECT coalesce(nullif(octet_length(c.full_name), 0), nullif(octet_length(c.first_name), 0),
		                  nullif(octet_length(c.push_name), 0), nullif(octet_length(c.business_name), 0), 0) AS nb,
		         coalesce(octet_length(c.full_name), 0) + coalesce(octet_length(c.first_name), 0) +
		         coalesce(octet_length(c.push_name), 0) + coalesce(octet_length(c.business_name), 0) AS ab,
		         max(coalesce(octet_length(c.full_name), 0), coalesce(octet_length(c.first_name), 0),
		             coalesce(octet_length(c.push_name), 0), coalesce(octet_length(c.business_name), 0)) AS fb,
		         max(octet_length(c.their_jid), coalesce(octet_length(m.pn) + length('@s.whatsapp.net'), 0)) AS jb` +
		contactSource + ` LIMIT 100001))
		SELECT n, bytes, longest, allb,
		  CASE WHEN n <= 100000 AND bytes <= 4194304 AND longest <= 1024 AND allb <= 16777216 AND longest_jid <= 256 THEN
		    (SELECT json_group_array(json_array(person, phone, hex(CAST(name AS BLOB))) ORDER BY mcp_fold(name), person)
		     FROM (SELECT person, phone, name FROM (` + people + `) ORDER BY mcp_fold(name), person LIMIT ?3 OFFSET ?4))
		  END FROM pre`
	var out []Contact
	err = s.snapshot(ctx, "SearchContacts", func(ctx context.Context, q querier) error {
		var count, chosenBytes, longest, allBytes int64
		var data sql.NullString
		if err := q.QueryRowContext(ctx, bounded, text, digits, p.Limit, p.Offset).
			Scan(&count, &chosenBytes, &longest, &allBytes, &data); err != nil {
			return err
		}
		if !data.Valid {
			return limitErr("contact source")
		}
		var rows [][]json.RawMessage
		if err := json.Unmarshal([]byte(data.String), &rows); err != nil {
			return ioError("contact_result")
		}
		budget := &resultBudget{}
		for _, row := range rows {
			if len(row) != 3 {
				return ioError("contact_result")
			}
			var c Contact
			fields := []*string{&c.JID, &c.Phone, &c.Name}
			for i, raw := range row {
				var value any
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.UseNumber()
				if err := decoder.Decode(&value); err != nil {
					return ioError("contact_result")
				}
				if value != nil {
					*fields[i] = fmt.Sprint(value)
				}
			}
			name, err := hex.DecodeString(c.Name)
			if err != nil {
				return ioError("contact_result")
			}
			c.Name = string(name)
			if err := budget.add(len(c.JID) + len(c.Phone) + len(c.Name) + 48); err != nil {
				return err
			}
			out = append(out, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ContactNames returns the current device's contact-table names for jid: saved is the full name, else the first
// name; real is the push name, else the business name. Both are empty when there is no entry or no device. A name
// longer than the store's name limit is a resource-limit error, never a shortened name.
func (s *Store) ContactNames(ctx context.Context, jid string) (saved, real string, err error) {
	j, err := parseJID("jid", jid, []string{serverUser, serverLID}, false)
	if err != nil {
		return "", "", s.report(newOpID(), err)
	}
	err = s.snapshot(ctx, "ContactNames", func(ctx context.Context, q querier) error {
		var sv, rl sql.NullString
		err := q.QueryRowContext(ctx, `
			SELECT coalesce(nullif(full_name, ''), nullif(first_name, ''), ''),
			       coalesce(nullif(push_name, ''), nullif(business_name, ''), '')
			FROM whatsmeow_contacts WHERE our_jid = `+currentDevice+` AND their_jid = ?`, j.String()).Scan(&sv, &rl)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(sv.String) > maxNameBytes || len(rl.String) > maxNameBytes {
			return limitErr("name bytes")
		}
		saved, real = sv.String, rl.String
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return saved, real, nil
}
