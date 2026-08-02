// Package store is tokenwarden's persistence layer: SQLite via
// modernc.org/sqlite, a pure-Go driver with no cgo. That constraint is
// deliberate (see CLAUDE.md and REQUIREMENTS.md NFR-BUILD-1) — it's what
// lets `GOOS=windows GOARCH=amd64 go build` succeed from a Mac with no C
// toolchain, which is what keeps cross-platform CD cheap.
//
// This package holds no business logic — dependency gating, priority
// ordering, and the rest live in internal/queue on top of it.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned by Get/Update/Delete operations that reference a
// job ID that doesn't exist.
var ErrNotFound = errors.New("job not found")

// Store wraps a SQLite database holding the job queue.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path and
// applies any pending migrations. path may be ":memory:" for tests.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// SQLite allows only one writer at a time regardless of connection
	// pooling; pinning to a single connection avoids SQLITE_BUSY errors
	// under concurrent access rather than papering over them with retries.
	// Phase 2's write volume doesn't need more than this — revisit if the
	// scheduler (Phase 4) turns out to want concurrent writers.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("setting %q: %w", p, err)
		}
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("reading embedded migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		version, err := migrationVersion(e.Name())
		if err != nil {
			return err
		}

		var alreadyApplied int
		if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, version).Scan(&alreadyApplied); err != nil {
			return fmt.Errorf("checking migration %s: %w", e.Name(), err)
		}
		if alreadyApplied > 0 {
			continue
		}

		body, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", e.Name(), err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("beginning transaction for migration %s: %w", e.Name(), err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("applying migration %s: %w", e.Name(), err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, time.Now().Unix()); err != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %s: %w", e.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %s: %w", e.Name(), err)
		}
	}

	return nil
}

// migrationVersion extracts the leading integer from a filename like
// "0001_init.sql" -> 1. Migrations apply in filename order, so the numeric
// prefix is what determines sequencing; keep it zero-padded and monotonic.
func migrationVersion(filename string) (int, error) {
	prefix, _, ok := strings.Cut(filename, "_")
	if !ok {
		return 0, fmt.Errorf("migration filename %q missing '_' separator", filename)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("migration filename %q has non-numeric prefix: %w", filename, err)
	}
	return version, nil
}

// unixOrNil converts a *time.Time to a nullable unix-seconds value for
// binding into a query.
func unixOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}
