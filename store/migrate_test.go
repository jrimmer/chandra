package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrate_AppliesInitialSchema(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer db.Close()

	err = db.Migrate()
	require.NoError(t, err)

	// Verify all tables exist
	tables := []string{
		"episodes", "memory_entries", "intents", "agent_profile",
		"user_profile", "relationship_state", "tool_telemetry",
		"sessions", "action_log", "action_rollups", "confirmations",
	}
	for _, table := range tables {
		var name string
		err := db.DB().QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		assert.NoError(t, err, "table %s should exist", table)
		assert.Equal(t, table, name)
	}
}

func TestMigrate_CreatesVecTable(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer db.Close()

	err = db.Migrate()
	require.NoError(t, err)

	// Verify memory_embeddings virtual table is queryable
	_, err = db.DB().Exec(
		"INSERT INTO memory_embeddings(id, embedding) VALUES (?, ?)",
		"test-id", SerializeFloat32(make([]float32, 1536)),
	)
	assert.NoError(t, err, "should be able to insert into memory_embeddings")
}

func TestMigrate_Idempotent(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer db.Close()

	err = db.Migrate()
	require.NoError(t, err)

	// Running again should not error
	err = db.Migrate()
	assert.NoError(t, err, "migrate should be idempotent")
}

