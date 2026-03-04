package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	vec.Auto()
}

// Store wraps the SQLite database connection with Chandra-specific setup.
type Store struct {
	db *sql.DB
}

// NewDB opens (or creates) the SQLite database at path with WAL mode,
// foreign keys, busy timeout, and sqlite-vec loaded.
func NewDB(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	// Create the DB file at 0600 if it doesn't exist.
	// sql.Open does not let us control the creation mode, so we pre-create
	// the file ourselves to avoid a window where it exists at the OS default umask.
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		f, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr != nil && !os.IsExist(createErr) {
			return nil, fmt.Errorf("create database file: %w", createErr)
		}
		if f != nil {
			f.Close()
		}
	} else if statErr == nil {
		// File exists — verify permissions are 0600 and tighten if not.
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("set database permissions: %w", err)
		}
	}

	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=1&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// Verify critical PRAGMAs took effect.
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		db.Close()
		return nil, fmt.Errorf("check journal mode: %w", err)
	}
	if journalMode != "wal" {
		db.Close()
		return nil, fmt.Errorf("expected WAL journal mode, got %q", journalMode)
	}

	// SQLite WAL mode supports concurrent readers but serializes writes.
	// Limit write connections to prevent "database is locked" errors.
	db.SetMaxOpenConns(1)

	return &Store{db: db}, nil
}

// DB returns the underlying *sql.DB for direct queries.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
