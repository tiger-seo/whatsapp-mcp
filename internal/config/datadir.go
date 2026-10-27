package config

import (
	"fmt"
	"path/filepath"

	"github.com/tiger-seo/whatsapp-mcp/internal/private"
)

// appDirName is the directory created under the per-user configuration directory.
const appDirName = "whatsapp-mcp"

// UserConfigDirError means the default data directory cannot be found.
type UserConfigDirError struct {
	Err error
}

func (e *UserConfigDirError) Error() string {
	return fmt.Sprintf("cannot find the per-user configuration directory (%v); set --data-dir or %sDATA_DIR", e.Err, EnvPrefix)
}

func (e *UserConfigDirError) Unwrap() error { return e.Err }

// DataDirError means the data directory cannot be used: it is not a directory, it cannot be created, or other
// users can reach it.
type DataDirError struct {
	Dir string
	Err error
}

func (e *DataDirError) Error() string {
	return fmt.Sprintf("data directory %s: %v", e.Dir, e.Err)
}

func (e *DataDirError) Unwrap() error { return e.Err }

func defaultDataDir(in Inputs) (string, error) {
	dir, err := in.UserConfigDir()
	if err != nil {
		return "", &UserConfigDirError{Err: err}
	}
	return filepath.Join(dir, appDirName), nil
}

// PrepareDataDir creates the data directory if it does not exist, readable only by this user, and checks that
// an existing one is private. An existing directory is never changed: the error says how to fix it.
func PrepareDataDir(dir string) error {
	if err := private.MkdirAll(dir); err != nil {
		return &DataDirError{Dir: dir, Err: err}
	}
	return nil
}
