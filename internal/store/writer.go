package store

import (
	"context"
	"database/sql/driver"
	"time"
)

// writerIntent covers callers queued in database/sql before they reach the driver.
func (g *gate) writerIntent() func() {
	g.mu.Lock()
	g.writerPending++
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		g.writerPending--
		g.mu.Unlock()
	}
}

func (g *gate) beginWriter(ctx context.Context) error {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		g.mu.Lock()
		// Closing admission does not revoke an already admitted operation's grace period.
		if g.closed && g.walDraining {
			g.mu.Unlock()
			return ErrClosed
		}
		if err := ctx.Err(); err != nil {
			g.mu.Unlock()
			return err
		}
		if !g.walDraining {
			g.writerEpoch++
			g.writerActive++
			g.mu.Unlock()
			return nil
		}
		if g.walDeadline.IsZero() {
			g.walDeadline = time.Now().Add(admissionWait)
		}
		remaining := time.Until(g.walDeadline)
		if g.toolFailed || remaining <= 0 {
			g.failToolLocked()
			g.mu.Unlock()
			return ErrBusy
		}
		changed := g.changed
		g.mu.Unlock()
		if timer == nil {
			timer = time.NewTimer(remaining)
		}
		select {
		case <-changed:
		case <-g.toolFailure:
		case <-g.closing:
		case <-ctx.Done():
		case <-timer.C:
		}
	}
}

func (s *Store) endWriter() {
	size, err := s.walSize()
	g := s.gate
	g.mu.Lock()
	if g.toolReader && (err != nil || size > 2*walLimitBytes) {
		g.walDraining = true
		g.toolCancel()
	}
	g.writerActive--
	g.mu.Unlock()
}

type writerConnector struct {
	driver.Connector
	store *Store
}

func (w writerConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := w.store.gate.beginWriter(ctx); err != nil {
		return nil, err
	}
	defer w.store.endWriter()
	c, err := w.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	inner, ok := c.(sqliteWriterConn)
	if !ok {
		c.Close()
		return nil, ioError("io")
	}
	return &writerConn{sqliteWriterConn: inner, store: w.store}, nil
}

// These interface sets match the pinned driver's optional methods, without exposing its raw handle.
type sqliteWriterConn interface {
	driver.Conn
	driver.Execer
	driver.Queryer
	driver.ExecerContext
	driver.QueryerContext
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

type writerConn struct {
	sqliteWriterConn
	store *Store
	// database/sql serializes calls on a connection, including transaction and rows calls.
	inTx, dead bool
}

func (c *writerConn) start(ctx context.Context) (func(), error) {
	if c.dead {
		return nil, driver.ErrBadConn
	}
	if c.inTx {
		return func() {}, nil
	}
	if err := c.store.gate.beginWriter(ctx); err != nil {
		return nil, err
	}
	return c.store.endWriter, nil
}

func (c *writerConn) Close() error {
	if c.dead {
		return nil
	}
	g := c.store.gate
	g.mu.Lock()
	g.writerEpoch++
	g.writerActive++
	g.mu.Unlock()
	defer c.store.endWriter()
	return c.discard()
}

func (c *writerConn) discard() error {
	c.dead = true
	return c.sqliteWriterConn.Close()
}

func (c *writerConn) IsValid() bool { return !c.dead && c.sqliteWriterConn.IsValid() }

func (c *writerConn) ResetSession(ctx context.Context) error {
	if c.dead {
		return driver.ErrBadConn
	}
	return c.sqliteWriterConn.ResetSession(ctx)
}

func (c *writerConn) Ping(ctx context.Context) error {
	done, err := c.start(ctx)
	if err != nil {
		return err
	}
	defer done()
	return c.sqliteWriterConn.Ping(ctx)
}

func (c *writerConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *writerConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	done, err := c.start(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := c.sqliteWriterConn.BeginTx(ctx, opts)
	if err != nil {
		done()
		return nil, err
	}
	c.inTx = true
	return &writerTx{Tx: tx, conn: c, done: done}, nil
}

type writerTx struct {
	driver.Tx
	conn *writerConn
	done func()
}

func (t *writerTx) Commit() error   { return t.finish(t.Tx.Commit()) }
func (t *writerTx) Rollback() error { return t.finish(t.Tx.Rollback()) }

func (t *writerTx) finish(err error) error {
	if err != nil {
		// The pinned driver attempts rollback after a failed commit, but does not report whether it succeeded.
		_ = t.conn.discard()
	}
	t.conn.inTx = false
	t.done()
	return err
}

func (c *writerConn) Exec(q string, args []driver.Value) (driver.Result, error) {
	return c.ExecContext(context.Background(), q, writerArgs(args))
}

func (c *writerConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	done, err := c.start(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	return c.sqliteWriterConn.ExecContext(ctx, q, args)
}

func (c *writerConn) Query(q string, args []driver.Value) (driver.Rows, error) {
	return c.QueryContext(context.Background(), q, writerArgs(args))
}

func (c *writerConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	done, err := c.start(ctx)
	if err != nil {
		return nil, err
	}
	r, err := c.sqliteWriterConn.QueryContext(ctx, q, args)
	return wrapWriterRows(r, err, done)
}

func (c *writerConn) Prepare(q string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), q)
}

func (c *writerConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	done, err := c.start(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	stmt, err := c.sqliteWriterConn.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	inner, ok := stmt.(sqliteWriterStmt)
	if !ok {
		stmt.Close()
		return nil, ioError("io")
	}
	return &writerStmt{sqliteWriterStmt: inner, conn: c}, nil
}

type sqliteWriterStmt interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}

type writerStmt struct {
	sqliteWriterStmt
	conn *writerConn
}

func (s *writerStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), writerArgs(args))
}

func (s *writerStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	done, err := s.conn.start(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	return s.sqliteWriterStmt.ExecContext(ctx, args)
}

func (s *writerStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), writerArgs(args))
}

func (s *writerStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	done, err := s.conn.start(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.sqliteWriterStmt.QueryContext(ctx, args)
	return wrapWriterRows(r, err, done)
}

func writerArgs(args []driver.Value) []driver.NamedValue {
	n := make([]driver.NamedValue, len(args))
	for i, v := range args {
		n[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return n
}

type sqliteWriterRows interface {
	driver.Rows
	driver.RowsColumnTypeScanType
	driver.RowsColumnTypeDatabaseTypeName
	driver.RowsColumnTypeLength
	driver.RowsColumnTypeNullable
	driver.RowsColumnTypePrecisionScale
}

type writerRows struct {
	sqliteWriterRows
	done func()
}

func wrapWriterRows(r driver.Rows, err error, done func()) (driver.Rows, error) {
	if err == nil {
		if inner, ok := r.(sqliteWriterRows); ok {
			return &writerRows{sqliteWriterRows: inner, done: done}, nil
		}
		r.Close()
		err = ioError("io")
	}
	done()
	return nil, err
}

func (r *writerRows) Close() error {
	if r.done == nil {
		return nil
	}
	err := r.sqliteWriterRows.Close()
	r.done()
	r.done = nil
	return err
}
