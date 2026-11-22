package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func holdWALSnapshot(t *testing.T, st *Store) (*sql.Tx, func()) {
	t.Helper()
	release, ctx, err := enterTestStoreTool(st)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	conn, err := st.reader.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM writer_probe`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return tx, sync.OnceFunc(func() {
		if err := tx.Rollback(); err != nil {
			t.Error(err)
		}
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
		release()
	})
}

func writerProbe(t *testing.T, hook func(int64, error) (int64, error)) *Store {
	t.Helper()
	st, _ := openTestWith(t, newDataDir(t), openOptions{hooks: testHooks{
		free:      func(string) (uint64, error) { return 2 * gib, nil },
		writerWAL: hook,
	}})
	if _, err := st.writer.Exec(`CREATE TABLE writer_probe(v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	return st
}

func waitWriterState(t *testing.T, ready func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); !ready(); {
		if time.Now().After(deadline) {
			t.Fatal("writer did not reach expected state")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWriterFenceHandoffAfterRollbackStatFailure(t *testing.T) {
	var inject atomic.Bool
	sampling, publish := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(publish) })
	st := writerProbe(t, func(size int64, err error) (int64, error) {
		if inject.Swap(false) {
			close(sampling)
			<-publish
			return 0, errors.New("synthetic WAL stat failure")
		}
		return size, err
	})
	tx, release := holdWALSnapshot(t, st)
	defer release()
	defer unblock()
	wtx, err := st.writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wtx.Exec(`INSERT INTO writer_probe VALUES(1)`); err != nil {
		wtx.Rollback()
		t.Fatal(err)
	}
	injected := make(chan error, 1)
	injectedWrite := func(ctx context.Context) {
		_, err := st.writer.ExecContext(ctx, `INSERT INTO writer_probe VALUES(2)`)
		injected <- err
	}
	rolled := make(chan error, 1)
	inject.Store(true)
	go func() { rolled <- wtx.Rollback() }()
	select {
	case <-sampling:
	case <-time.After(3 * time.Second):
		t.Fatal("rollback did not sample before handing off")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := st.writer.Stats().WaitCount
	go injectedWrite(ctx)
	waitWriterState(t, func() bool { return st.writer.Stats().WaitCount > before })
	unblock()
	if err := <-rolled; err != nil {
		t.Fatal(err)
	}
	waitWriterState(t, func() bool {
		st.gate.mu.Lock()
		defer st.gate.mu.Unlock()
		return st.gate.walDraining && !st.gate.walDeadline.IsZero() && st.gate.writerActive == 0
	})
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM writer_probe`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("held snapshot: rows=%d err=%v", n, err)
	}
	select {
	case err := <-injected:
		t.Fatalf("writer returned while the snapshot was pinned: %v", err)
	default:
	}
	release()
	if err := <-injected; err != nil {
		t.Fatal(err)
	}
	var v string
	if err := st.reader.QueryRow(`SELECT group_concat(v) FROM writer_probe`).Scan(&v); err != nil || v != "2" {
		t.Fatalf("rollback or waiting write changed outcome: rows=%q err=%v", v, err)
	}
}

func TestWriterFenceExpiryThroughDevicePool(t *testing.T) {
	var inject atomic.Bool
	st := writerProbe(t, func(size int64, err error) (int64, error) {
		if inject.Swap(false) {
			return 2*walLimitBytes + 1, nil
		}
		return size, err
	})
	seedDevice(t, st)
	dev, _ := loadDevice(t, st)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := dev.Sessions.PutSession(ctx, sessionPN, sessionBlob(1)); err != nil {
		t.Fatal(err)
	}
	_, release := holdWALSnapshot(t, st)
	defer release()
	inject.Store(true)
	if _, err := st.writer.ExecContext(ctx, `INSERT INTO writer_probe VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	lookup := func() error { _, err := dev.Sessions.GetSession(ctx, sessionPN); return err }
	first, queued := make(chan error, 1), make(chan error, 1)
	go func() { first <- lookup() }()
	waitWriterState(t, func() bool {
		st.gate.mu.Lock()
		defer st.gate.mu.Unlock()
		return !st.gate.walDeadline.IsZero()
	})
	before := st.writer.Stats().WaitCount
	go func() { queued <- lookup() }()
	waitWriterState(t, func() bool { return st.writer.Stats().WaitCount > before })
	for _, done := range []<-chan error{first, queued} {
		if err := <-done; !errors.Is(err, ErrBusy) {
			t.Fatalf("held device lookup: %v, want ErrBusy", err)
		}
	}
	if _, err := st.writer.ExecContext(ctx, `INSERT INTO writer_probe VALUES(2)`); !errors.Is(err, ErrBusy) {
		t.Fatalf("new writer entered after latch: %v", err)
	}
	var n int
	if err := st.read(ctx, "writer-probe", func(ctx context.Context, conn *sql.Conn) error {
		return conn.QueryRowContext(ctx, `SELECT count(*) FROM writer_probe`).Scan(&n)
	}); err != nil || n != 1 {
		t.Fatalf("independent reader: rows=%d err=%v", n, err)
	}
	if st.gate.activeCount() != 1 {
		t.Fatalf("snapshot lost its accounting: active=%d", st.gate.activeCount())
	}
	release()
	select {
	case <-st.ToolStoreNotQuiescent():
	default:
		t.Fatal("actual release erased the failure latch")
	}
	if err := lookup(); err != nil {
		t.Fatalf("snapshot fence survived actual release: %v", err)
	}
	if r, _, err := enterTestStoreTool(st); !errors.Is(err, ErrBusy) {
		if r != nil {
			r()
		}
		t.Fatalf("replacement after latching: %v", err)
	}
}

func TestWriterFenceDuringExistingDeviceLease(t *testing.T) {
	for _, short := range []bool{false, true} {
		name := "resumes"
		if short {
			name = "caller_deadline"
		}
		t.Run(name, func(t *testing.T) {
			var checksUntilCrossing atomic.Int32
			st := writerProbe(t, func(size int64, err error) (int64, error) {
				if checksUntilCrossing.Load() > 0 && checksUntilCrossing.Add(-1) == 0 {
					return 2*walLimitBytes + 1, nil
				}
				return size, err
			})
			seedDevice(t, st)
			dev, barrier := loadDevice(t, st)
			_, release := holdWALSnapshot(t, st)
			defer release()
			done := make(chan error, 1)
			go func() {
				done <- barrier.sess.run(context.Background(), "lease-fence-test", false, false, nil, func(ctx context.Context) error {
					// Let an already-admitted lease age before its first autocommit.
					time.Sleep(admissionWait + 10*time.Millisecond)
					// The guard reads the run marker before writing the session.
					checksUntilCrossing.Store(2)
					if err := dev.Sessions.PutSession(ctx, sessionPN, sessionBlob(1)); err != nil {
						return err
					}
					if short {
						bounded, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
						defer cancel()
						ctx = bounded
					}
					return dev.Sessions.PutSession(ctx, sessionPN2, sessionBlob(2))
				})
			}()
			deadline := time.After(operationLimit)
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
		wait:
			for {
				st.gate.mu.Lock()
				waiting := !st.gate.walDeadline.IsZero()
				st.gate.mu.Unlock()
				if waiting {
					break wait
				}
				select {
				case err := <-done:
					t.Fatalf("existing lease failed before waiting at the fence: %v", err)
				case <-deadline:
					t.Fatal("existing lease never reached fence")
				case <-tick.C:
				}
			}
			if !short {
				release()
			}
			select {
			case err := <-done:
				if (short && !errors.Is(err, context.DeadlineExceeded)) || (!short && err != nil) {
					t.Fatalf("nested write: %v", err)
				}
			case <-deadline:
				t.Fatal("nested write did not return")
			}
			select {
			case <-st.ToolStoreNotQuiescent():
				t.Fatal("timely release or shorter caller deadline triggered the shared fence failure")
			default:
			}
			release()
			has, err := dev.Sessions.HasSession(context.Background(), sessionPN2)
			if err != nil || has == short {
				t.Fatalf("nested write after fence: stored=%v err=%v", has, err)
			}
		})
	}
}

func TestWriterFenceHoldsReplacementConnection(t *testing.T) {
	var crossing atomic.Bool
	st := writerProbe(t, func(size int64, err error) (int64, error) {
		if crossing.Swap(false) {
			return 2*walLimitBytes + 1, nil
		}
		return size, err
	})
	_, release := holdWALSnapshot(t, st)
	defer release()
	st.writer.SetMaxIdleConns(0)
	// Publish the crossing on the INSERT, then discard that connection.
	conn, err := st.writer.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	crossing.Store(true)
	if _, err := conn.ExecContext(context.Background(), `INSERT INTO writer_probe VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if got := st.writer.Stats().OpenConnections; got != 0 {
		t.Fatalf("expected the pool to discard the writer, open=%d", got)
	}
	st.gate.mu.Lock()
	epoch := st.gate.writerEpoch
	st.gate.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := st.writer.ExecContext(ctx, `INSERT INTO writer_probe VALUES(2)`); done <- err }()
	waitWriterState(t, func() bool {
		st.gate.mu.Lock()
		defer st.gate.mu.Unlock()
		return !st.gate.walDeadline.IsZero()
	})
	st.gate.mu.Lock()
	unchanged := st.gate.writerEpoch == epoch && st.gate.writerActive == 0
	st.gate.mu.Unlock()
	if !unchanged {
		t.Error("replacement connector entered SQLite while snapshot was held")
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWriterFencePreservesOrdinaryCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newGate()
		release, _, err := enterTestTool(g)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		g.walDraining = true
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, maxActive+maxWaiting)
		for range maxActive + maxWaiting {
			go func() {
				leave, opCtx, err := g.enter(ctx)
				if err == nil {
					err = g.beginWriter(opCtx)
					leave()
				}
				done <- err
			}()
		}
		synctest.Wait()
		if got := g.waitingCount(); got != maxWaiting+1 || g.activeCount() != maxActive {
			t.Fatalf("fenced capacity: active=%d waiting=%d", g.activeCount(), got)
		}
		if _, _, err := g.enter(ctx); !errors.Is(err, ErrBusy) {
			t.Fatalf("81st ordinary operation: %v, want ErrBusy", err)
		}
		cancel()
		for range maxActive + maxWaiting {
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("cancelled waiter: %v", err)
			}
		}
		if g.activeCount() != 1 || g.waitingCount() != 0 {
			t.Error("cancelled work leaked slots or released the held tool")
		}
	})
}

func TestWriterPreparedMetadataAndCancellation(t *testing.T) {
	st := writerProbe(t, nil)
	ctx := context.Background()
	stmt, err := st.writer.PrepareContext(ctx, `INSERT INTO writer_probe VALUES (?) RETURNING v`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := stmt.QueryContext(cancelled, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prepared query: %v", err)
	}
	rows, err := stmt.QueryContext(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("missing RETURNING row: %v", rows.Err())
	}
	cols, err := rows.ColumnTypes()
	if err != nil || len(cols) != 1 || cols[0].DatabaseTypeName() != "INTEGER" || cols[0].ScanType().Kind().String() != "int64" {
		t.Fatalf("unexpected wrapped row metadata: %v, %v", cols, err)
	}
	var n int
	if err := rows.Scan(&n); err != nil || n != 2 {
		t.Fatalf("prepared query value=%d err=%v", n, err)
	}
	st.gate.mu.Lock()
	active := st.gate.writerActive
	st.gate.mu.Unlock()
	if active != 1 {
		t.Fatalf("RETURNING released the writer before rows close: active=%d", active)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.writer.QueryRow(`SELECT sum(v) FROM writer_probe`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("reused writer after rows close: total=%d err=%v", n, err)
	}
}
