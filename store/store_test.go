package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDB_CreatesDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = os.Stat(dbPath)
	assert.NoError(t, err, "database file should exist")
}

func TestNewDB_EnablesWAL(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer db.Close()

	var mode string
	err = db.DB().QueryRow("PRAGMA journal_mode").Scan(&mode)
	require.NoError(t, err)
	assert.Equal(t, "wal", mode)
}

func TestNewDB_EnablesForeignKeys(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer db.Close()

	var fk int
	err = db.DB().QueryRow("PRAGMA foreign_keys").Scan(&fk)
	require.NoError(t, err)
	assert.Equal(t, 1, fk)
}

func TestNewDB_SqliteVecLoaded(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer db.Close()

	// Verify sqlite-vec is available by creating a vec0 table
	_, err = db.DB().Exec(`CREATE VIRTUAL TABLE test_vec USING vec0(id TEXT PRIMARY KEY, embedding FLOAT[3])`)
	assert.NoError(t, err, "sqlite-vec should be loaded and vec0 available")
}
