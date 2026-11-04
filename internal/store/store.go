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
	"github.com/tiger-seo/whatsapp-mcp/internal/walog"
	"go.mau.fi/util/dbutil"
	wastore "go.mau.fi/whatsmeow/store"
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
	wrapped   *dbutil.Database
	hooks     *testHooks

	deviceMu sync.Mutex
	device   *deviceSession

	closeMu sync.Mutex
	closed  bool // pools closed after quiescence; never set while work is active
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
	devicePutHold    func(ctx context.Context) error        // a guarded device Save holds here, before the raw put
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
// transaction with its version record, so a failure leaves the device schema at the last completed step. The
// configured dbutil.Database is kept on the store: the device container and every later device delegate share
// that one handle, so dbutil's transaction key is the same for all of them.
func (s *Store) upgradeDevice(ctx context.Context, op string, version *int) error {
	wrapped, err := dbutil.NewWithDB(s.writer, "sqlite")
	if err != nil {
		return ioError("io")
	}
	wrapped.VersionTable = "whatsmeow_version"
	wrapped.UpgradeTable = upgrades.Table
	wrapped.ReadOnlyDB = s.reader
	s.wrapped = wrapped
	s.container = sqlstore.NewWithWrappedDB(wrapped, walog.New(s.ev.log))

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

// LoadDevice loads the saved device identity, or a fresh unwired device when none is saved, through the shared
// admission policy. It returns the guarded device and the barrier of that one generation. Only one device
// session may be active: a second call fails until the previous generation is retired and fully drained, and no
// raw container or store is ever handed out.
func (s *Store) LoadDevice(ctx context.Context) (*wastore.Device, *DeviceBarrier, error) {
	if s.gate.isClosed() {
		return nil, nil, ErrClosed
	}
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	if s.device != nil && !s.device.drained() {
		return nil, nil, ErrSessionExists
	}
	if s.device != nil {
		s.device.mu.Lock()
		pendingRemoval := s.device.deleted && !s.device.removed
		s.device.mu.Unlock()
		if pendingRemoval {
			return nil, nil, ErrDeleteDeferred
		}
	}

	op := newOpID()
	release, opCtx, err := s.gate.enter(ctx)
	if err != nil {
		return nil, nil, s.report(op, err)
	}
	defer release()
	if err := s.disk.check(); err != nil {
		return nil, nil, s.report(op, err)
	}
	var count int
	if err := s.reader.QueryRowContext(opCtx, "SELECT count(*) FROM (SELECT 1 FROM whatsmeow_device LIMIT 2)").Scan(&count); err != nil {
		return nil, nil, s.report(op, err)
	}
	if count > 1 {
		err = unsafeStorage(filepath.Base(s.path), "multiple_devices",
			"contains more than one saved device identity; this binary never creates that state",
			"keep exactly one row in the whatsmeow_device table, then restart")
		s.ev.refused(op, err, s.path, "", 0, 0)
		return nil, nil, err
	}
	if count == 1 {
		var size int64
		err = s.reader.QueryRowContext(opCtx, `SELECT
			4 * (coalesce(length(jid), 0) + coalesce(length(lid), 0) +
			coalesce(length(platform), 0) + coalesce(length(business_name), 0) +
			coalesce(length(push_name), 0) + coalesce(length(facebook_uuid), 0) +
			coalesce(length(companion_meta_nonce), 0)) +
			coalesce(length(noise_key), 0) + coalesce(length(identity_key), 0) +
			coalesce(length(signed_pre_key), 0) + coalesce(length(signed_pre_key_sig), 0) +
			coalesce(length(adv_key), 0) + coalesce(length(adv_details), 0) +
			coalesce(length(adv_account_sig), 0) + coalesce(length(adv_account_sig_key), 0) +
			coalesce(length(adv_device_sig), 0)
			FROM whatsmeow_device LIMIT 1`).Scan(&size)
		if err != nil {
			return nil, nil, s.report(op, err)
		}
		if size > maxResultBytes {
			return nil, nil, s.report(op, limitErr("result_bytes"))
		}
	}
	devices, err := s.container.GetAllDevices(opCtx)
	if err != nil {
		err = s.report(op, err)
		if isRefusal(err) {
			s.ev.refused(op, err, s.path, "", 0, 0)
		}
		return nil, nil, err
	}
	switch {
	case len(devices) > 1:
		err = unsafeStorage(filepath.Base(s.path), "multiple_devices",
			"contains more than one saved device identity; this binary never creates that state",
			"keep exactly one row in the whatsmeow_device table, then restart")
		s.ev.refused(op, err, s.path, "", 0, 0)
		return nil, nil, err
	case len(devices) == 1:
		if err := validateDeviceIdentity(devices[0].ID, devices[0].LID); err != nil {
			err = unsafeStorage(filepath.Base(s.path), "device_identity",
				"the saved device identity is not a valid phone-number JID: "+err.Error(),
				"verify the whatsmeow_device row, then restart")
			s.ev.refused(op, err, s.path, "", 0, 0)
			return nil, nil, err
		}
		s.device = newDeviceSession(s, devices[0])
		s.device.bind(*devices[0].ID, devices[0].LID)
		return s.device.dev, &DeviceBarrier{sess: s.device}, nil
	default:
		s.device = newDeviceSession(s, s.container.NewDevice())
		return s.device.dev, &DeviceBarrier{sess: s.device}, nil
	}
}

func (s *Store) closePools() {
	if s.reader != nil {
		s.reader.Close()
	}
	if s.writer != nil {
		s.writer.Close()
	}
}

// Quiesce permanently closes shared admission: new and waiting calls are refused at once, running operations
// keep their grace period, are then cancelled and must still finish. It reports nil only when quiescence is
// proved — zero admitted operations and no future admissions — and ErrNotQuiescent while work is active. The
// pools stay open either way; Close finishes after quiescence. ctx can shorten every wait.
func (s *Store) Quiesce(ctx context.Context) error { return s.gate.quiesce(ctx) }

// Quiesced returns the store's completion signal. It closes exactly once, after Quiesce or Close has shut
// shared admission and the last admitted operation has finished. A signal is not implied by a bounded wait
// returning: only this channel proves the drain.
func (s *Store) Quiesced() <-chan struct{} { return s.gate.quiesced }

// Close quiesces the store and then closes both pools. It reports ErrNotQuiescent without closing the pools or
// marking the store closed while work is active, so a later call can finish the close with a live context. Only
// after the drain does it close the pools and report their errors; pool-close errors after quiescence mean an
// unsuccessful exit, not an active writer. It is safe to call again. ctx can shorten every wait. Any call made
// during or after the closed state returns ErrClosed.
func (s *Store) Close(ctx context.Context) error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	start := time.Now()
	if err := s.gate.quiesce(ctx); err != nil {
		return err
	}
	var errs []error
	if s.reader != nil {
		errs = append(errs, s.reader.Close())
	}
	if s.writer != nil {
		errs = append(errs, s.writer.Close())
	}
	s.closed = true
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
