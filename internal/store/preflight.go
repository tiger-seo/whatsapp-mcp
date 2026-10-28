package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
	"go.mau.fi/whatsmeow/store/sqlstore/upgrades"
)

// checkHeader reads the first 100 bytes of the database and requires the SQLite magic and a valid page size, so
// a file that is not a database is refused without SQLite ever opening it.
func checkHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return ioError("io")
	}
	defer f.Close()
	hdr := make([]byte, dbHeaderSize)
	if _, err := io.ReadFull(f, hdr); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return notDatabase(path)
		}
		return ioError("io")
	}
	ps, err := pageSizeFromHeader(hdr)
	if err != nil || !bytes.HasPrefix(hdr, dbMagic) {
		return notDatabase(path)
	}
	// A database holds at least its first page, so a shorter file was cut off.
	if fi, err := f.Stat(); err != nil {
		return ioError("io")
	} else if fi.Size() < ps {
		return notDatabase(path)
	}
	return nil
}

// preflightResult is what the read-only preflight found.
type preflightResult struct {
	fresh    bool // none of the store's tables exists
	messages int  // mcp_version, 0 when absent
	device   int  // whatsmeow_version.version, 0 when absent
	compat   int  // whatsmeow_version.compat, or version when compat is NULL or 0
}

// preflight reads the schema versions of the database at path as of its last commit, through the committed view,
// so no file is created, changed or deleted. known is the identity of each artifact checkArtifacts saw; the files
// the view opened must be the same ones. latest is the newest bundled message migration.
func preflight(ctx context.Context, path string, known map[string]private.FileID, latest int) (res preflightResult, err error) {
	dsn, release, ids, err := openCommittedViewIDs(path)
	if err != nil {
		if errors.Is(err, ErrNotDatabase) {
			return res, notDatabase(path)
		}
		return res, ioError("io")
	}
	defer func() {
		if rerr := release(); rerr != nil && err == nil {
			err = ioError("io")
		}
	}()
	if !sameFile(known, path, ids.main, true) || !sameFile(known, path+"-wal", ids.wal, ids.hasWAL) {
		return res, unsafeStorage(path, "unsafe_artifact", "was replaced while the store was opening; startup stopped before any change", recoverySectionFix)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return res, ioError("io")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	res, err = readVersions(ctx, db, latest)
	if err != nil {
		if sqliteCode(err) != 0 {
			if mapped := mapDriverError(err); errors.Is(mapped, ErrNotDatabase) {
				return res, notDatabase(path)
			} else {
				return res, mapped
			}
		}
		return res, err
	}
	return res, nil
}

// sameFile checks one file the view opened against the identity recorded earlier. A file that existed at the check
// and is gone now, or the other way round, is a mismatch too.
func sameFile(known map[string]private.FileID, path string, id private.FileID, present bool) bool {
	want, existed := known[path]
	if present != existed {
		return false
	}
	return !present || want == id
}

func readVersions(ctx context.Context, db *sql.DB, latest int) (res preflightResult, err error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return res, err
	}
	tables := map[string]bool{}
	otherDevice := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return res, err
		}
		tables[name] = true
		if strings.HasPrefix(name, "whatsmeow_") && name != "whatsmeow_version" {
			otherDevice = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return res, err
	}
	rows.Close()

	hasMessages := tables["chats"] || tables["messages"]
	res.fresh = !tables["mcp_version"] && !hasMessages && !tables["whatsmeow_version"] && !otherDevice
	if res.fresh {
		return res, nil
	}

	switch {
	case tables["mcp_version"]:
		var n int
		var max sql.NullInt64
		if err := db.QueryRowContext(ctx, "SELECT count(*), max(version) FROM mcp_version").Scan(&n, &max); err != nil {
			return res, err
		}
		if n != 1 {
			return res, &SchemaError{Schema: "messages", sentinel: ErrSchemaInvalid}
		}
		res.messages = int(max.Int64)
		if res.messages > latest {
			return res, &SchemaError{Schema: "messages", Observed: res.messages, Supported: latest, sentinel: ErrSchemaTooNew}
		}
	case hasMessages:
		return res, &SchemaError{Schema: "messages", sentinel: ErrSchemaInvalid}
	}

	supported := len(upgrades.Table)
	switch {
	case tables["whatsmeow_version"]:
		hasCompat, err := hasColumn(ctx, db, "whatsmeow_version", "compat")
		if err != nil {
			return res, err
		}
		compatExpr := "NULL"
		if hasCompat {
			compatExpr = "compat"
		}
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM whatsmeow_version").Scan(&n); err != nil {
			return res, err
		}
		switch {
		case n > 1, n == 0 && otherDevice:
			return res, &SchemaError{Schema: "device", sentinel: ErrSchemaInvalid}
		case n == 1:
			var version int
			var compat sql.NullInt64
			if err := db.QueryRowContext(ctx, "SELECT version, "+compatExpr+" FROM whatsmeow_version").Scan(&version, &compat); err != nil {
				return res, err
			}
			res.device, res.compat = version, version
			if compat.Valid && compat.Int64 > 0 {
				res.compat = int(compat.Int64)
			}
			if res.compat > supported {
				return res, &SchemaError{Schema: "device", Observed: res.compat, Supported: supported, sentinel: ErrSchemaTooNew}
			}
		}
	case otherDevice:
		return res, &SchemaError{Schema: "device", sentinel: ErrSchemaInvalid}
	}
	return res, nil
}

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_info('"+table+"')")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
