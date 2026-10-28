// Package store owns the database file <data dir>/whatsapp.db, which holds both the whatsmeow device store and
// the message history. No other package issues database queries; Device is the only handle on the database that
// leaves the package. A Store is safe for concurrent use.
package store
