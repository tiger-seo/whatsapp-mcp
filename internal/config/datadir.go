package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

func defaultDataDir(in Inputs) (string, error) {
	dir, err := in.UserConfigDir()
	if err != nil {
		return "", &UserConfigDirError{Err: err}
	}
	return filepath.Join(dir, appDirName), nil
}

// PrepareDataDir creates the data directory with mode 0700 if it does not exist. The permissions of an
// existing directory are left alone: the operator may have chosen them.
func PrepareDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		if fi, serr := os.Stat(dir); serr == nil && !fi.IsDir() {
			return fmt.Errorf("data directory %s: exists and is not a directory", dir)
		}
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return fmt.Errorf("data directory %s: %v", dir, err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("data directory %s: %v", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("data directory %s: exists and is not a directory", dir)
	}
	return nil
}
