package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migration is one numbered step of the message schema.
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads NNNN_<name>.sql files from dir of fsys. The numbers must run 1..N with no gap or
// duplicate.
func loadMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var list []migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, rest, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || len(num) != 4 || v < 1 {
			return nil, fmt.Errorf("migration file %q is not named NNNN_<name>.sql", name)
		}
		b, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return nil, err
		}
		list = append(list, migration{version: v, name: rest, sql: string(b)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	for i, m := range list {
		if m.version != i+1 {
			return nil, fmt.Errorf("migrations are not contiguous: expected %04d, found %04d", i+1, m.version)
		}
	}
	return list, nil
}

// migrate applies every migration above current, in order. Each runs with its mcp_version update in one
// immediate transaction on the writer, so a failing step leaves the database at the previous version. It
// returns the version reached.
func migrate(ctx context.Context, writer *sql.DB, current int, list []migration, ev *events, op string) (int, error) {
	for _, m := range list {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, writer, m); err != nil {
			ev.migrate(op, current, m.version, m.name, err)
			return current, &MigrationError{
				Schema: "messages", Step: fmt.Sprintf("%04d (%s)", m.version, m.name),
				Completed: strconv.Itoa(current),
			}
		}
		ev.migrate(op, current, m.version, m.name, nil)
		current = m.version
	}
	return current, nil
}

func applyMigration(ctx context.Context, writer *sql.DB, m migration) (err error) {
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if m.version > 1 {
		if _, err = tx.ExecContext(ctx, "UPDATE mcp_version SET version = ?", m.version); err != nil {
			return err
		}
	}
	return tx.Commit()
}
