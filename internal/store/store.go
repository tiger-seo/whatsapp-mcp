package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"go.mau.fi/util/dbutil"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/store/sqlstore/upgrades"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Read and write DSN parameters. The writer holds the write lock from BEGIN, so writers wait in the busy handler
// instead of failing on lock upgrade; the reader has no _txlock because a query_only connection refuses
// BEGIN IMMEDIATE. temp_store(MEMORY) keeps sorts out of the system temp directory, and the hard heap limit bounds
// SQLite's own memory, which Go cannot see.
const (
	writeParams = "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=temp_store(MEMORY)&_pragma=journal_size_limit(67108864)&_pragma=hard_heap_limit(268435456)&_txlock=immediate"
	readParams = "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)" +
		"&_pragma=hard_heap_limit(268435456)&_pragma=query_only(1)"
)

// Store is the database of whatsapp.db: the whatsmeow device store and the chat and message history. It is safe
// for concurrent use.
type Store struct {
	dir, path string
	writer    *sql.DB
	reader    *sql.DB
	gate      *gate
	disk      *diskGuard
	ev        *events
	container *sqlstore.Container
	hooks     *testHooks

	closeMu sync.Mutex
	closed  bool
}

// openOptions are for tests only: the migration list and the injected failures.
type openOptions struct {
	migrations []migration
	hooks      testHooks
}

// testHooks inject failures and pauses at fixed points. The zero value does nothing.
type testHooks struct {
	afterCheck       func() // after the artifact checks and the preflight, before the writer opens
	afterCreate      func() // after a new database file was created, before the writer opens
	beforeCommit     func(ctx context.Context)
	afterSenderMerge func() error                           // MergeLID, after the sender rewrite
	afterChatInsert  func() error                           // StoreMessages, after the chat insert
	free             func(dir string) (uint64, error)       // free-space measurement
	queryHold        func(ctx context.Context, name string) // a query holds its reader here
	wrapReader       func(q querier) querier                // a read operation runs its queries through the wrapper
	onRead           func(conn *sql.Conn) (done func())     // a read operation starts on conn; done runs when it ends
}

// Open checks, creates if missing, preflights, upgrades and opens <dataDir>/whatsapp.db. The caller holds the
// data-directory lock. ctx bounds the whole startup.
func Open(ctx context.Context, dataDir string, logger *slog.Logger) (*Store, error) {
	return open(ctx, dataDir, logger, openOptions{})
}

func open(ctx context.Context, dataDir string, logger *slog.Logger, opts openOptions) (_ *Store, err error) {
	ev := newEvents(logger)
	op := newOpID()
	dbPath := filepath.Join(dataDir, dbFileName)
	ev.open(op, dbPath)
	defer func() {
		if err == nil {
			return
		}
		if isRefusal(err) {
			var se *SchemaError
			var st *StorageError
			switch {
			case errors.As(err, &se):
				ev.refused(op, err, dbPath, se.Schema, se.Observed, se.Supported)
			case errors.As(err, &st):
				ev.refused(op, err, firstNonEmpty(st.Artifact, dbPath), "", 0, 0)
			default:
				ev.refused(op, err, dbPath, "", 0, 0)
			}
		} else {
			ev.ioError(op, "open", err)
		}
	}()

	if err := checkPathForm(dataDir); err != nil {
		return nil, err
	}
	if err := memoryLimitAvailable(); err != nil {
		return nil, err
	}
	migrations := opts.migrations
	if migrations == nil {
		if migrations, err = loadMigrations(embeddedMigrations, "migrations"); err != nil {
			return nil, ioError("migrations")
		}
	}
	latest := migrations[len(migrations)-1].version

	known, err := checkArtifacts(dataDir, false)
	if err != nil {
		return nil, err
	}
	var pf preflightResult
	switch _, exists := known[dbPath]; {
	case !exists:
		id, err := createDB(dbPath)
		if err != nil {
			return nil, err
		}
		known[dbPath] = id
		pf.fresh = true
		ev.preflight(op, true, 0, 0, 0)
		if opts.hooks.afterCreate != nil {
			opts.hooks.afterCreate()
		}
	case sizeOf(dbPath) == 0:
		pf.fresh = true
		ev.preflight(op, true, 0, 0, 0)
	default:
		if err := checkHeader(dbPath); err != nil {
			return nil, err
		}
		if pf, err = preflight(ctx, dbPath, known, latest); err != nil {
			return nil, err
		}
		ev.preflight(op, pf.fresh, pf.messages, pf.device, pf.compat)
	}
	registerFold()
	if opts.hooks.afterCheck != nil {
		opts.hooks.afterCheck()
	}

	st := &Store{
		dir: dataDir, path: dbPath, gate: newGate(), disk: newDiskGuard(dataDir), ev: ev, hooks: &opts.hooks,
	}
	if opts.hooks.free != nil {
		st.disk.free = opts.hooks.free
	}
	defer func() {
		if err != nil {
			st.closePools()
		}
	}()

	if st.writer, err = openPool(ctx, dbPath, writeParams, writeConns); err != nil {
		return nil, err
	}
	if err := probeWritable(ctx, st.writer); err != nil {
		return nil, err
	}
	if err := st.recheck(known); err != nil {
		return nil, err
	}
	if st.reader, err = openPool(ctx, dbPath, readParams, readConns); err != nil {
		return nil, err
	}
	if err := st.recheck(known); err != nil {
		return nil, err
	}

	deviceVersion := pf.device
	if err := st.upgradeDevice(ctx, op, &deviceVersion); err != nil {
		return nil, err
	}
	if err := st.recheck(known); err != nil {
		return nil, err
	}
	reached, err := migrate(ctx, st.writer, pf.messages, migrations, ev, op)
	if err != nil {
		var me *MigrationError
		if errors.As(err, &me) {
			me.Completed = fmt.Sprintf("device %d, messages %d", deviceVersion, reached)
		}
		return nil, err
	}
	if err := st.recheck(known); err != nil {
		return nil, err
	}
	ev.ready(op, reached, deviceVersion)
	return st, nil
}

func isRefusal(err error) bool {
	return errors.Is(err, ErrUnsafeStorage) || errors.Is(err, ErrNotDatabase) || errors.Is(err, ErrSchemaTooNew) ||
		errors.Is(err, ErrSchemaInvalid)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func sizeOf(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return -1
}

// fileDSN builds the SQLite URI for a file path, escaping the characters that a URI reads specially.
func fileDSN(path, params string) string {
	p := filepath.ToSlash(path)
	p = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(p)
	if runtime.GOOS == "windows" {
		p = "///" + p
	}
	return "file:" + p + "?" + params
}

func openPool(ctx context.Context, path, params string, conns int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", fileDSN(path, params))
	if err != nil {
		return nil, ioError("io")
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, mapDriverError(err)
	}
	return db, nil
}

// recheck compares the files on disk with the identities recorded before SQLite opened them, and checks the new
// sidecars. A difference means a file was replaced between the check and the open.
func (s *Store) recheck(known map[string]private.FileID) error {
	now, err := checkArtifacts(s.dir, true)
	if err != nil {
		return err
	}
	return sameArtifacts(known, now)
}

// upgradeDevice runs whatsmeow's own schema upgrade on the writer connection. Each of its steps is one
// transaction with its version record, so a failure leaves the device schema at the last completed step.
func (s *Store) upgradeDevice(ctx context.Context, op string, version *int) error {
	wrapped, err := dbutil.NewWithDB(s.writer, "sqlite")
	if err != nil {
		return ioError("io")
	}
	wrapped.VersionTable = "whatsmeow_version"
	wrapped.UpgradeTable = upgrades.Table
	wrapped.ReadOnlyDB = s.reader
	s.container = sqlstore.NewWithWrappedDB(wrapped, newWALog(s.ev.log))

	from := *version
	if err := s.container.Upgrade(ctx); err != nil {
		var cur sql.NullInt64
		_ = s.writer.QueryRowContext(context.WithoutCancel(ctx), "SELECT version FROM whatsmeow_version").Scan(&cur)
		*version = int(cur.Int64)
		err := &MigrationError{
			Schema: "device", Step: fmt.Sprintf("to version %d", len(upgrades.Table)),
			Completed: fmt.Sprintf("device %d", *version),
		}
		s.ev.upgrade(op, from, *version, err)
		return err
	}
	*version = len(upgrades.Table)
	if from != *version {
		s.ev.upgrade(op, from, *version, nil)
	}
	return nil
}

// Device returns whatsmeow's device-store container, which shares the store's file and writer connection.
// Feature 003 builds the WhatsApp client from it. It is the only handle on the database outside this package.
func (s *Store) Device() *sqlstore.Container { return s.container }

func (s *Store) closePools() {
	if s.reader != nil {
		s.reader.Close()
	}
	if s.writer != nil {
		s.writer.Close()
	}
}

// Close stops admitting operations, waits up to 2 s for running ones, cancels what is still running, waits up to
// 1 s more, then closes both pools. It is safe to call twice. ctx can shorten every wait. Any call made during or
// after Close returns ErrClosed.
func (s *Store) Close(ctx context.Context) error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	start := time.Now()
	s.gate.close(ctx)
	var errs []error
	if s.reader != nil {
		errs = append(errs, s.reader.Close())
	}
	if s.writer != nil {
		errs = append(errs, s.writer.Close())
	}
	s.ev.closed(newOpID(), time.Since(start))
	if err := errors.Join(errs...); err != nil {
		return mapDriverError(err)
	}
	return nil
}

// probeWritable writes the header page in a transaction and rolls it back. SQLite quietly opens a read-only file read-only, and a database
// whose schema is current would then start without a single write, to fail on the first message.
func probeWritable(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return mapDriverError(err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return mapDriverError(err)
	}
	// Rewriting the header with its own value is a write that the rollback then discards.
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(version)); err != nil {
		return mapDriverError(err)
	}
	return nil
}
