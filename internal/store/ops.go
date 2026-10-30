package store

import (
	"context"
	"database/sql"
	"errors"
)

// report classifies an error for the caller and logs the operational ones. Limits and busy go to store.limit,
// I/O, disk and corruption failures to store.io_error. Validation, not-found and context errors are the
// caller's, and are not logged.
func (s *Store) report(op string, err error) error {
	if err == nil {
		return nil
	}
	mapped := mapDriverError(err)
	switch {
	case errors.Is(mapped, ErrBusy):
		s.ev.limit(op, "busy", mapped)
	case errors.Is(mapped, ErrResourceLimit):
		var rl *ResourceLimitError
		name := "resource"
		if errors.As(mapped, &rl) {
			name = rl.Limit
		}
		s.ev.limit(op, name, mapped)
	case errors.Is(mapped, ErrIO), errors.Is(mapped, ErrDiskLow):
		s.ev.ioError(op, "operation", mapped)
	}
	return mapped
}

// write runs fn in one transaction on the writer, under admission and the disk guard. fn's error rolls the
// transaction back. A commit that fails after cancellation is indeterminate.
func (s *Store) write(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) error {
	op := newOpID()
	release, opCtx, err := s.gate.enter(ctx)
	if err != nil {
		return s.report(op, err)
	}
	defer release()
	if err := s.disk.check(); err != nil {
		return s.report(op, err)
	}
	tx, err := s.writer.BeginTx(opCtx, nil)
	if err != nil {
		return s.report(op, err)
	}
	if err := fn(opCtx, tx); err != nil {
		tx.Rollback()
		return s.report(op, err)
	}
	if s.hooks.beforeCommit != nil {
		s.hooks.beforeCommit(opCtx)
	}
	if err := commitOutcome(opCtx, tx.Commit()); err != nil {
		if errors.Is(err, ErrIndeterminate) {
			s.ev.emitIndeterminate(op)
		}
		return s.report(op, err)
	}
	return nil
}

// nullable returns nil for an empty string, so an absent value is stored as NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableBytes returns nil for an empty slice, because a nil []byte would be bound as an empty blob.
func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// read runs fn on one reader connection, under admission. fn gets the operation context and the connection, so
// it can open a read-only transaction for one consistent snapshot. The queryHold hook lets a test keep the
// reader busy.
func (s *Store) read(ctx context.Context, name string, fn func(ctx context.Context, conn *sql.Conn) error) error {
	op := newOpID()
	release, opCtx, err := s.gate.enter(ctx)
	if err != nil {
		return s.report(op, err)
	}
	defer release()
	conn, err := s.reader.Conn(opCtx)
	if err != nil {
		return s.report(op, err)
	}
	defer conn.Close()
	if s.hooks.onRead != nil {
		defer s.hooks.onRead(conn)()
	}
	if s.hooks.queryHold != nil {
		s.hooks.queryHold(opCtx, name)
	}
	return s.report(op, fn(opCtx, conn))
}
