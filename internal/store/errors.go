package store

import "errors"

// ErrNotDatabase means a file in the data directory is not a SQLite database the store can use.
var ErrNotDatabase = errors.New("not a usable SQLite database")
