package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"modernc.org/sqlite"
)

// The sentinels classify every error the store returns; test them with errors.Is.
var (
	ErrInvalid       = errors.New("invalid input")
	ErrNotFound      = errors.New("not found")
	ErrNotMedia      = errors.New("message has no media")
	ErrResourceLimit = errors.New("resource limit exceeded")
	ErrBusy          = errors.New("store is busy")
	ErrClosed        = errors.New("store is closed")
	ErrDiskLow       = errors.New("free disk space is below the reserve")
	ErrIndeterminate = errors.New("the outcome of the write cannot be established")
	ErrSchemaTooNew  = errors.New("schema is newer than this binary supports")
	ErrSchemaInvalid = errors.New("schema version metadata is invalid")
	ErrUnsafeStorage = errors.New("unsafe storage")
	ErrNotDatabase   = errors.New("not a usable SQLite database")
	ErrIO            = errors.New("storage I/O error")
)

// ValidationError says which field failed validation and why. It never repeats the rejected value.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string   { return "invalid " + e.Field + ": " + e.Reason }
func (e *ValidationError) Is(t error) bool { return t == ErrInvalid }

func invalid(field, reason string) error { return &ValidationError{Field: field, Reason: reason} }

// NotFoundError names the chat and message that were looked up; ID is empty for a chat lookup, ChatJID for a
// lookup by ID alone.
type NotFoundError struct {
	ChatJID string
	ID      string
}

func (e *NotFoundError) Error() string {
	switch {
	case e.ID == "":
		return "chat " + safePath(e.ChatJID) + " not found"
	case e.ChatJID == "":
		return "message " + safePath(e.ID) + " not found"
	}
	return "message " + safePath(e.ID) + " in chat " + safePath(e.ChatJID) + " not found"
}
func (e *NotFoundError) Is(t error) bool { return t == ErrNotFound }

// AmbiguousIDError lists the chats that hold the same message ID.
type AmbiguousIDError struct {
	ID       string
	ChatJIDs []string
}

func (e *AmbiguousIDError) Error() string {
	q := make([]string, len(e.ChatJIDs))
	for i, c := range e.ChatJIDs {
		q[i] = safePath(c)
	}
	return "message ID " + safePath(e.ID) + " exists in " + strconv.Itoa(len(q)) + " chats: " + strings.Join(q, ", ")
}

// SchemaError reports a refused schema: Schema is "messages" or "device".
type SchemaError struct {
	Schema    string
	Observed  int
	Supported int
	sentinel  error
}

func (e *SchemaError) Error() string {
	if e.sentinel == ErrSchemaTooNew {
		if e.Schema == "device" {
			return fmt.Sprintf("device schema compatible from version %d, this binary supports up to %d; use a newer whatsapp-mcp", e.Observed, e.Supported)
		}
		return fmt.Sprintf("message schema version %d is newer than this binary supports (%d); use a newer whatsapp-mcp", e.Observed, e.Supported)
	}
	if e.Schema == "device" {
		return "device schema version metadata is invalid"
	}
	return "message schema version metadata is invalid"
}
func (e *SchemaError) Is(t error) bool { return t == e.sentinel }

// MigrationError reports a failed upgrade step and the versions that completed before it.
type MigrationError struct {
	Schema    string // "messages" or "device"
	Step      string
	Completed string
}

func (e *MigrationError) Error() string {
	name := "message migration"
	if e.Schema == "device" {
		name = "device upgrade"
	}
	return fmt.Sprintf("%s %s failed; completed: %s; see OPERATIONS.md \"Recovering from a refused start\"", name, e.Step, e.Completed)
}

// StorageError is an unsafe artifact or path, a file that is not a database, or an I/O failure. Code is a stable
// snake_case reason; Fix says what the operator can do.
type StorageError struct {
	Artifact string
	Code     string
	Reason   string
	Fix      string
	sentinel error
}

func (e *StorageError) Error() string {
	s := e.Reason
	if e.Artifact != "" {
		s = e.Artifact + " " + s
	}
	if e.Fix != "" {
		s += "; " + e.Fix
	}
	return s
}
func (e *StorageError) Is(t error) bool { return t == e.sentinel }
func (e *StorageError) Unwrap() error   { return e.sentinel }

func unsafeStorage(artifact, code, reason, fix string) error {
	return &StorageError{Artifact: safePath(artifact), Code: code, Reason: reason, Fix: fix, sentinel: ErrUnsafeStorage}
}

func notDatabase(artifact string) error {
	return &StorageError{
		Artifact: safePath(artifact), Code: "not_database", sentinel: ErrNotDatabase,
		Reason: "is not a usable SQLite database; it was left unchanged",
	}
}

func ioError(code string) error {
	return &StorageError{Code: code, Reason: "storage I/O error (" + code + ")", sentinel: ErrIO}
}

// ResourceLimitError names the limit that was exceeded.
type ResourceLimitError struct{ Limit string }

func (e *ResourceLimitError) Error() string   { return "resource limit exceeded: " + e.Limit }
func (e *ResourceLimitError) Is(t error) bool { return t == ErrResourceLimit }

func limitErr(name string) error { return &ResourceLimitError{Limit: name} }

// DiskLowError carries the measured free space and the reserve.
type DiskLowError struct{ Free, Reserve uint64 }

func (e *DiskLowError) Error() string {
	return fmt.Sprintf("free disk space (%d bytes) is below the reserve (%d bytes)", e.Free, e.Reserve)
}
func (e *DiskLowError) Is(t error) bool { return t == ErrDiskLow }

// SQLite primary result codes the store distinguishes.
const (
	sqliteBusy      = 5
	sqliteLocked    = 6
	sqliteNoMem     = 7
	sqliteReadonly  = 8
	sqliteInterrupt = 9
	sqliteCorrupt   = 11
	sqliteFull      = 13
	sqliteNotADB    = 26
)

// sqliteCode returns the primary SQLite result code of err, or 0 when it is not a driver error.
func sqliteCode(err error) int {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() & 0xff
	}
	return 0
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// mapDriverError converts a driver error into the store's error set. It keeps the SQLite result code and never
// the driver's message text, which can echo SQL fragments or values. Errors the store already classified, and
// context errors, pass through.
func mapDriverError(err error) error {
	if err == nil {
		return nil
	}
	for _, s := range []error{ErrInvalid, ErrNotFound, ErrNotMedia, ErrResourceLimit, ErrBusy, ErrClosed, ErrDiskLow,
		ErrIndeterminate, ErrSchemaTooNew, ErrSchemaInvalid, ErrUnsafeStorage, ErrNotDatabase, ErrIO} {
		if errors.Is(err, s) {
			return err
		}
	}
	var (
		ae *AmbiguousIDError
		me *MigrationError
	)
	if errors.As(err, &ae) || errors.As(err, &me) || isContextErr(err) {
		return err
	}
	switch sqliteCode(err) {
	case sqliteNoMem:
		return limitErr("memory")
	case sqliteFull:
		return ioError("disk_full")
	case sqliteReadonly:
		return ioError("readonly")
	case sqliteCorrupt:
		return ioError("corrupt")
	case sqliteNotADB:
		return notDatabase("")
	case sqliteBusy, sqliteLocked:
		return ioError("busy")
	case sqliteInterrupt:
		return ioError("interrupted")
	}
	return ioError("io")
}

// codeOf returns the stable code of an error, for events.
func codeOf(err error) string {
	var (
		st *StorageError
		se *SchemaError
		rl *ResourceLimitError
		me *MigrationError
	)
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &st):
		return st.Code
	case errors.As(err, &se):
		if se.sentinel == ErrSchemaTooNew {
			return "schema_too_new"
		}
		return "schema_invalid"
	case errors.As(err, &rl):
		return strings.ReplaceAll(rl.Limit, " ", "_")
	case errors.As(err, &me):
		if me.Schema == "device" {
			return "device_upgrade_failed"
		}
		return "migration_failed"
	case errors.Is(err, ErrDiskLow):
		return "disk_low"
	case errors.Is(err, ErrBusy):
		return "busy"
	case errors.Is(err, ErrClosed):
		return "closed"
	case errors.Is(err, ErrIndeterminate):
		return "indeterminate"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case isContextErr(err):
		return "canceled"
	}
	return "error"
}
