// Package sqlite owns the lifecycle and schema of the Core SQLite database.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	coremigrations "github.com/yyZe0122/yunmengze-agent/migrations/core"
)

const (
	defaultBusyTimeoutMillis = 5000
	migrationTable           = "schema_migrations"
)

// DB is the Core database handle. It exposes database/sql only to other
// internal packages.
type DB struct {
	path string
	sql  *sql.DB
}

// Open creates the parent directory, applies safety PRAGMAs, and runs all Core
// migrations. Core uses one SQLite connection so writes are serialized.
func Open(ctx context.Context, path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}

	cleanedPath := filepath.Clean(path)
	if cleanedPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(cleanedPath), 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	handle, err := sql.Open("sqlite", cleanedPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	handle.SetMaxOpenConns(1)
	handle.SetMaxIdleConns(1)

	database := &DB{path: cleanedPath, sql: handle}
	if err := database.configure(ctx); err != nil {
		_ = handle.Close()
		return nil, err
	}
	if err := migrate(ctx, handle, coremigrations.Files); err != nil {
		_ = handle.Close()
		return nil, err
	}
	return database, nil
}

func (db *DB) configure(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		fmt.Sprintf("PRAGMA busy_timeout = %d", defaultBusyTimeoutMillis),
		"PRAGMA synchronous = FULL",
	}
	for _, statement := range pragmas {
		if _, err := db.sql.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure database with %q: %w", statement, err)
		}
	}

	var mode string
	if err := db.sql.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}
	if !strings.EqualFold(mode, "wal") && db.path != ":memory:" {
		return fmt.Errorf("enable WAL: SQLite selected %q", mode)
	}
	return nil
}

// SQL returns the internal database handle used by Core repositories.
func (db *DB) SQL() *sql.DB {
	return db.sql
}

// Health performs SQLite's lightweight structural consistency check.
func (db *DB) Health(ctx context.Context) error {
	var result string
	if err := db.sql.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("database quick check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("database quick check failed: %s", result)
	}
	return nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

func migrate(ctx context.Context, db *sql.DB, migrationFS fs.FS) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version TEXT PRIMARY KEY,
        applied_at TEXT NOT NULL
    )`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	entries, err := fs.Glob(migrationFS, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)

	for _, name := range entries {
		var applied int
		err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+migrationTable+" WHERE version = ?", name,
		).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied != 0 {
			continue
		}

		script, err := fs.ReadFile(migrationFS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := applyMigration(ctx, db, name, string(script)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, name, script string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The first write acquires SQLite's write lock and rechecks the unique version.
	// The claim is committed with the script, or both are rolled back.
	record, err := tx.ExecContext(ctx,
		"INSERT INTO "+migrationTable+" (version, applied_at) VALUES (?, ?) ON CONFLICT(version) DO NOTHING",
		name, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}
	inserted, err := record.RowsAffected()
	if err != nil || inserted == 0 {
		return err
	}
	if _, err := tx.ExecContext(ctx, script); err != nil {
		return err
	}
	return tx.Commit()
}
