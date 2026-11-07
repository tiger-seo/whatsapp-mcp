package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrUnsafeRun = errors.New("unsafe run requires offline recovery")

// RunGuard reports the committed singleton and its five collection-transfer bits.
func (s *Store) RunGuard(ctx context.Context) (string, uint8, error) {
	release, c, err := s.gate.enter(ctx)
	if err != nil {
		return "", 0, err
	}
	defer release()
	return readRunGuard(c, s.reader)
}

func readRunGuard(ctx context.Context, db *sql.DB) (string, uint8, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, state, repair_bits, typeof(id), typeof(state), typeof(repair_bits) FROM unsafe_run LIMIT 2")
	if err != nil {
		return "", 0, ErrUnsafeRun
	}
	defer rows.Close()
	var id, bits int64
	var state, idType, stateType, bitsType string
	if !rows.Next() || rows.Scan(&id, &state, &bits, &idType, &stateType, &bitsType) != nil || id != 1 || idType != "integer" ||
		(state != "clean" && state != "unsafe") || stateType != "text" || bitsType != "integer" || bits < 0 || bits > 31 || rows.Next() || rows.Err() != nil {
		return "", 0, ErrUnsafeRun
	}
	return state, uint8(bits), nil
}

// ArmRunGuard must succeed before any account-capable work. Any failure forbids
// automatic clearing in this Store invocation, including an ambiguous commit.
func (s *Store) ArmRunGuard(ctx context.Context) error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.runArmFailed {
		return ErrUnsafeRun
	}
	s.deviceMu.Lock()
	final := s.deviceFinal
	s.deviceMu.Unlock()
	if final {
		return ErrClosed
	}
	if s.runArmed {
		return nil
	}
	err := s.changeRunGuard(ctx, "clean", "unsafe")
	if err != nil {
		s.runArmFailed = true
		return err
	}
	s.runArmed = true
	return nil
}

// ClearRunGuard requires the caller to have retired and drained all callbacks,
// completed revocation cleanup, and resolved any pairing attempt.
func (s *Store) ClearRunGuard(ctx context.Context) error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if !s.runArmed || s.runArmFailed {
		return ErrUnsafeRun
	}
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	if s.device != nil {
		if !s.device.drained() {
			return ErrNotQuiescent
		}
		s.device.mu.Lock()
		pending := s.device.deleted && !s.device.removed
		s.device.mu.Unlock()
		if pending {
			return ErrDeleteDeferred
		}
	}
	if err := s.changeRunGuard(ctx, "unsafe", "clean"); err != nil {
		s.runArmFailed = true
		s.deviceFinal = true
		return err
	}
	s.runArmed = false
	s.deviceFinal = true
	return nil
}

// RecoverRunGuard is an offline-only primitive. The caller must first verify the
// account and phone-side device, and may set removeIdentity only after separate
// authorization for local removal. It never connects or authorizes pairing.
// A successful or indeterminate recovery ends device admission for this Store.
func (s *Store) RecoverRunGuard(ctx context.Context, removeIdentity bool) error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	if s.deviceFinal || s.runArmed || s.runArmFailed || s.device != nil {
		return ErrUnsafeRun
	}
	release, c, err := s.gate.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := s.disk.check(); err != nil {
		return err
	}
	conn, err := s.writer.Conn(c)
	if err != nil {
		return mapDriverError(err)
	}
	defer conn.Close()
	if err := verifyDurableWriter(c, conn); err != nil {
		return err
	}
	tx, err := conn.BeginTx(c, nil)
	if err != nil {
		return mapDriverError(err)
	}
	defer tx.Rollback()
	if err := validateRunMarker(c, tx); err != nil {
		return err
	}
	var state string
	if err := tx.QueryRowContext(c, "SELECT state FROM unsafe_run WHERE id=1").Scan(&state); err != nil {
		return mapDriverError(err)
	}
	if state != "unsafe" {
		return ErrUnsafeRun
	}
	rows, err := tx.QueryContext(c, "SELECT jid FROM whatsmeow_device LIMIT 2")
	if err != nil {
		return mapDriverError(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return mapDriverError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return mapDriverError(err)
	}
	if len(ids) > 1 || (len(ids) == 1 && !removeIdentity) {
		return ErrUnsafeRun
	}
	if len(ids) == 1 {
		if err := transferRepairAndDelete(c, tx, ids[0]); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(c, "UPDATE unsafe_run SET state='clean' WHERE id=1 AND state='unsafe'")
	if err != nil {
		return mapDriverError(err)
	}
	if err := requireOne(res); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		s.deviceFinal = true
		return ErrIndeterminate
	}
	s.deviceFinal = true
	return nil
}

func verifyDurableWriter(ctx context.Context, conn *sql.Conn) error {
	var mode string
	var synchronous, foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return mapDriverError(err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		return mapDriverError(err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return mapDriverError(err)
	}
	if mode != "wal" || synchronous != 2 || foreignKeys != 1 {
		return ErrUnsafeRun
	}
	return nil
}

func requireOne(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return mapDriverError(err)
	}
	if n != 1 {
		return ErrUnsafeRun
	}
	return nil
}

func validateRunMarker(ctx context.Context, tx *sql.Tx) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM unsafe_run WHERE id=1 AND typeof(id)='integer' AND typeof(state)='text' AND state IN ('clean','unsafe') AND typeof(repair_bits)='integer' AND repair_bits BETWEEN 0 AND 31 AND (SELECT count(*) FROM unsafe_run)=1`).Scan(&count); err != nil {
		return mapDriverError(err)
	}
	if count != 1 {
		return ErrUnsafeRun
	}
	return nil
}

func transferRepairAndDelete(ctx context.Context, tx *sql.Tx, id string) error {
	var unknown int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM device_repair WHERE device_jid=? AND (typeof(collection)!='text' OR collection NOT IN ('critical_block','critical_unblock_low','regular_high','regular','regular_low'))`, id).Scan(&unknown); err != nil {
		return mapDriverError(err)
	}
	if unknown != 0 {
		return ErrUnsafeRun
	}
	for i, name := range repairCollections {
		res, err := tx.ExecContext(ctx, `UPDATE unsafe_run SET repair_bits=repair_bits | CASE WHEN EXISTS (SELECT 1 FROM device_repair WHERE device_jid=? AND collection=?) THEN ? ELSE 0 END WHERE id=1`, id, name, 1<<i)
		if err != nil {
			return mapDriverError(err)
		}
		if err := requireOne(res); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM whatsmeow_device WHERE jid=?", id)
	if err != nil {
		return mapDriverError(err)
	}
	return requireOne(res)
}

func (s *Store) changeRunGuard(ctx context.Context, from, to string) error {
	release, c, err := s.gate.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := s.disk.check(); err != nil {
		return err
	}
	conn, err := s.writer.Conn(c)
	if err != nil {
		return mapDriverError(err)
	}
	defer conn.Close()
	if err := verifyDurableWriter(c, conn); err != nil {
		return err
	}
	tx, err := conn.BeginTx(c, nil)
	if err != nil {
		return mapDriverError(err)
	}
	defer tx.Rollback()
	if err := validateRunMarker(c, tx); err != nil {
		return err
	}
	res, err := tx.ExecContext(c, "UPDATE unsafe_run SET state=? WHERE id=1 AND state=? AND typeof(repair_bits)='integer' AND repair_bits BETWEEN 0 AND 31 AND (SELECT count(*) FROM unsafe_run)=1", to, from)
	if err != nil {
		return mapDriverError(err)
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return ErrUnsafeRun
	}
	if err := tx.Commit(); err != nil {
		return ErrIndeterminate
	}
	return nil
}
