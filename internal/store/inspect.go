package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Inspection is what Inspect observed in a data directory. The zero value is the unpaired snapshot
// of storage that does not exist.
type Inspection struct {
	// Present is true when a database with at least one table exists.
	Present bool
	// SchemaVersion is the message schema version found, 0 when none is recorded.
	SchemaVersion int
	// Legacy is true for a message schema older than the one carrying the unsafe-run marker: its
	// marker state is unknown, not clean.
	Legacy bool
	// Unsafe is true for an unsafe marker and for every legacy schema.
	Unsafe bool
	// IdentityPresent is true when exactly one saved device row exists. No key is read.
	IdentityPresent bool
	// RepairPending is true when a device repair marker exists. Inspect never runs a repair.
	RepairPending bool
}

// ArtifactsPresent reports whether the database or any of its sidecars exists, using only lstat. It
// reads no file, so status can decide whether anything needs the owner lock before touching it.
func ArtifactsPresent(dataDir string) (bool, error) {
	for _, p := range artifactNames(filepath.Join(dataDir, dbFileName)) {
		if _, err := os.Lstat(p); err == nil {
			return true, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, ioError("io")
		}
	}
	return false, nil
}

// markerSchema is the first message schema that records the unsafe-run marker.
const markerSchema = 3

// Inspect reports the local state of the data directory through the read-only committed view of the
// database and its log. It never opens the store for writing: nothing is created, migrated, upgraded,
// repaired or checkpointed, and no credential is loaded. A directory without a database is the empty
// snapshot, not an error; a missing directory is created by nobody.
//
// The caller must hold the data directory's lock for the result to describe a quiet store.
func Inspect(ctx context.Context, dataDir string) (Inspection, error) {
	if err := ctx.Err(); err != nil {
		return Inspection{}, err
	}
	if err := checkPathForm(dataDir); err != nil {
		return Inspection{}, err
	}
	if _, err := os.Stat(dataDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Inspection{}, nil
		}
		return Inspection{}, ioError("io")
	}
	known, err := checkArtifacts(dataDir, false)
	if err != nil {
		return Inspection{}, err
	}
	dbPath := filepath.Join(dataDir, dbFileName)
	if _, exists := known[dbPath]; !exists || sizeOf(dbPath) == 0 {
		return Inspection{}, nil
	}
	if err := checkHeader(dbPath); err != nil {
		return Inspection{}, err
	}
	migrations, err := loadMigrations(embeddedMigrations, "migrations")
	if err != nil {
		return Inspection{}, ioError("migrations")
	}
	latest := migrations[len(migrations)-1].version

	var out Inspection
	err = withCommittedView(ctx, dbPath, known, func(db *sql.DB) error {
		res, err := readVersions(ctx, db, latest)
		if err != nil {
			return err
		}
		if res.fresh {
			return nil
		}
		out, err = inspectView(ctx, db, dbPath, res)
		return err
	})
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return Inspection{}, cerr
		}
		return Inspection{}, err
	}
	return out, nil
}

// inspectView reads the marker, identity presence and repair markers from one committed view.
func inspectView(ctx context.Context, db *sql.DB, path string, res preflightResult) (Inspection, error) {
	out := Inspection{Present: true, SchemaVersion: res.messages}
	tables := map[string]bool{}
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return Inspection{}, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return Inspection{}, err
		}
		tables[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Inspection{}, err
	}
	rows.Close()

	if res.messages < markerSchema {
		// The marker did not exist: an older store may have attempted a connection, so it is unknown.
		out.Legacy, out.Unsafe = true, true
	} else {
		if !tables["unsafe_run"] {
			return Inspection{}, unsafeStorage(path, "marker_missing", "has no unsafe-run marker; the run state cannot be inferred", recoverySectionFix)
		}
		var rowsInTable, valid int
		var state string
		err := db.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM unsafe_run),
			(SELECT count(*) FROM unsafe_run WHERE id = 1 AND typeof(id) = 'integer' AND typeof(state) = 'text'
			   AND state IN ('clean', 'unsafe') AND typeof(repair_bits) = 'integer' AND repair_bits BETWEEN 0 AND 31),
			coalesce((SELECT state FROM unsafe_run WHERE id = 1), '')`).Scan(&rowsInTable, &valid, &state)
		if err != nil {
			return Inspection{}, err
		}
		if rowsInTable != 1 || valid != 1 {
			return Inspection{}, unsafeStorage(path, "marker_malformed", "has a missing or malformed unsafe-run marker; the run state cannot be inferred", recoverySectionFix)
		}
		out.Unsafe = state == "unsafe"
	}

	if tables["whatsmeow_device"] {
		var devices int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT 1 FROM whatsmeow_device LIMIT 2)").Scan(&devices); err != nil {
			return Inspection{}, err
		}
		if devices > 1 {
			return Inspection{}, unsafeStorage(path, "multiple_devices", "contains more than one saved device identity", "resolve the unexpected device rows before using this data directory")
		}
		out.IdentityPresent = devices == 1
	}
	if tables["device_repair"] {
		if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM device_repair)").Scan(&out.RepairPending); err != nil {
			return Inspection{}, err
		}
	}
	return out, nil
}
