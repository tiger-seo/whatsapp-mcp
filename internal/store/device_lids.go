package store

import (
	"context"
	"database/sql"
	"errors"

	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

const deviceLIDBatchSize = 300

func (s *deviceSession) putLIDMappings(ctx context.Context, mappings []wastore.LIDMapping) error {
	return s.st.wrapped.DoTxn(ctx, nil, func(tx context.Context) error {
		for start := 0; start < len(mappings); start += deviceLIDBatchSize {
			end := min(start+deviceLIDBatchSize, len(mappings))
			for _, m := range mappings[start:end] {
				if _, err := s.st.wrapped.Exec(tx, `DELETE FROM whatsmeow_lid_map WHERE lid<>$1 AND pn=$2`, m.LID.User, m.PN.User); err != nil {
					return err
				}
				if _, err := s.st.wrapped.Exec(tx, `INSERT INTO whatsmeow_lid_map (lid, pn) VALUES ($1, $2)
					ON CONFLICT (lid) DO UPDATE SET pn=excluded.pn WHERE whatsmeow_lid_map.pn<>excluded.pn`, m.LID.User, m.PN.User); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *deviceSession) getLIDMapping(ctx context.Context, source types.JID, toLID bool) (types.JID, error) {
	query := `SELECT CASE WHEN octet_length(pn) <= $2 THEN pn END FROM whatsmeow_lid_map WHERE lid=$1`
	server := types.DefaultUserServer
	if toLID {
		query = `SELECT CASE WHEN octet_length(lid) <= $2 THEN lid END FROM whatsmeow_lid_map WHERE pn=$1`
		server = types.HiddenUserServer
	}
	var user sql.NullString
	err := s.st.wrapped.QueryRow(ctx, query, source.User, maxJIDBytes).Scan(&user)
	if errors.Is(err, sql.ErrNoRows) {
		return types.JID{}, nil
	}
	if err != nil {
		return types.JID{}, err
	}
	if !user.Valid {
		return types.JID{}, limitErr("result bytes")
	}
	return types.JID{User: user.String, Server: server, Device: source.Device}, nil
}

func (s *deviceSession) getManyLIDs(ctx context.Context, pns []types.JID) (map[types.JID]types.JID, error) {
	if len(pns) == 0 {
		return nil, nil
	}
	result := make(map[types.JID]types.JID)
	total := 0
	for _, pn := range pns {
		lid, err := s.getLIDMapping(ctx, pn, true)
		if err != nil {
			return nil, err
		}
		if lid.IsEmpty() {
			continue
		}
		if _, exists := result[pn]; !exists {
			charge := len(pn.User) + len(lid.User) + 192
			if charge > maxResultBytes-total {
				return nil, limitErr("result bytes")
			}
			total += charge
		}
		result[pn] = lid
	}
	return result, nil
}
