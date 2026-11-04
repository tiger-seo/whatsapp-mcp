package store

import (
	"context"
	"database/sql"
	"errors"

	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

const deviceContactBatchSize = 300

// contact reads through the same transaction-aware handle as the guarded device.
func (s *deviceSession) contact(ctx context.Context, user types.JID) (types.ContactInfo, error) {
	var info types.ContactInfo
	var first, full, push, business, redacted sql.NullString
	var oversized bool
	err := s.st.wrapped.QueryRow(ctx, `SELECT
		CASE WHEN octet_length(first_name) <= $3 THEN first_name END,
		CASE WHEN octet_length(full_name) <= $3 THEN full_name END,
		CASE WHEN octet_length(push_name) <= $3 THEN push_name END,
		CASE WHEN octet_length(business_name) <= $3 THEN business_name END,
		CASE WHEN octet_length(redacted_phone) <= $4 THEN redacted_phone END,
		coalesce(octet_length(first_name),0) > $3 OR coalesce(octet_length(full_name),0) > $3 OR
		coalesce(octet_length(push_name),0) > $3 OR coalesce(octet_length(business_name),0) > $3 OR
		coalesce(octet_length(redacted_phone),0) > $4
		FROM whatsmeow_contacts WHERE our_jid=$1 AND their_jid=$2`, s.id, user, maxNameBytes, maxJIDBytes).Scan(&first, &full, &push, &business, &redacted, &oversized)
	if errors.Is(err, sql.ErrNoRows) {
		return info, nil
	}
	if err != nil {
		return info, err
	}
	if oversized {
		return info, limitErr("result bytes")
	}
	info = types.ContactInfo{Found: true, FirstName: first.String, FullName: full.String, PushName: push.String, BusinessName: business.String, RedactedPhone: redacted.String}
	return info, nil
}

func (s *deviceSession) putContactNameField(ctx context.Context, user types.JID, value string, business bool) (bool, string, error) {
	var changed bool
	var previous string
	err := s.st.wrapped.DoTxn(ctx, nil, func(tx context.Context) error {
		info, err := s.contact(tx, user)
		if err != nil {
			return err
		}
		query := `INSERT INTO whatsmeow_contacts (our_jid, their_jid, push_name) VALUES ($1, $2, $3)
			ON CONFLICT (our_jid, their_jid) DO UPDATE SET push_name=excluded.push_name`
		previous = info.PushName
		if business {
			previous = info.BusinessName
			query = `INSERT INTO whatsmeow_contacts (our_jid, their_jid, business_name) VALUES ($1, $2, $3)
				ON CONFLICT (our_jid, their_jid) DO UPDATE SET business_name=excluded.business_name`
		}
		if previous == value {
			previous = ""
			return nil
		}
		_, err = s.st.wrapped.Exec(tx, query, s.id, user, value)
		changed = err == nil
		return err
	})
	return changed, previous, err
}

func (s *deviceSession) putContactNames(ctx context.Context, contacts []wastore.ContactEntry) error {
	return s.st.wrapped.DoTxn(ctx, nil, func(tx context.Context) error {
		for start := 0; start < len(contacts); start += deviceContactBatchSize {
			end := min(start+deviceContactBatchSize, len(contacts))
			for _, contact := range contacts[start:end] {
				_, err := s.st.wrapped.Exec(tx, `INSERT INTO whatsmeow_contacts (our_jid, their_jid, first_name, full_name)
					VALUES ($1, $2, $3, $4) ON CONFLICT (our_jid, their_jid)
					DO UPDATE SET first_name=excluded.first_name, full_name=excluded.full_name`, s.id, contact.JID, contact.FirstName, contact.FullName)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *deviceSession) putRedactedPhones(ctx context.Context, entries []wastore.RedactedPhoneEntry) error {
	return s.st.wrapped.DoTxn(ctx, nil, func(tx context.Context) error {
		for start := 0; start < len(entries); start += deviceContactBatchSize {
			end := min(start+deviceContactBatchSize, len(entries))
			for _, entry := range entries[start:end] {
				_, err := s.st.wrapped.Exec(tx, `INSERT INTO whatsmeow_contacts (our_jid, their_jid, redacted_phone)
					VALUES ($1, $2, $3) ON CONFLICT (our_jid, their_jid) DO UPDATE SET redacted_phone=excluded.redacted_phone`, s.id, entry.JID, entry.RedactedPhone)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}
