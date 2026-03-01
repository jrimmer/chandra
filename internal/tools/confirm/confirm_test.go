package confirm_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/tools/confirm"
	"github.com/jrimmer/chandra/store"
)

func init() {
	vec.Auto()
}

// newTestStore creates an in-memory SQLite database and a confirm.Store.
// It uses the full migrated schema (for other tables) plus the confirm
// package's own table created by confirm.New().
func newTestStore(t *testing.T) *confirm.Store {
	t.Helper()

	// Use a file-backed temp db so foreign-key migrations apply cleanly.
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	db := s.DB()
	t.Cleanup(func() { db.Close() })

	gs, err := confirm.New(db)
	require.NoError(t, err)
	return gs
}

// newInMemStore creates a minimal in-memory SQLite store (no migrations) for
// tests that only need the confirmations table.
func newInMemStore(t *testing.T) (*confirm.Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	gs, err := confirm.New(db)
	require.NoError(t, err)
	return gs, db
}

func TestConfirmation_CreatePending(t *testing.T) {
	gs, _ := newInMemStore(t)
	ctx := context.Background()

	c, err := gs.Create(ctx, `{"name":"ha.set_state","params":{}}`, 5*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.NotEmpty(t, c.ID)
	assert.Equal(t, confirm.StatusPending, c.Status)
	assert.WithinDuration(t, time.Now(), c.CreatedAt, 2*time.Second)
	assert.WithinDuration(t, time.Now().Add(5*time.Minute), c.ExpiresAt, 2*time.Second)
}

func TestConfirmation_Approve(t *testing.T) {
	gs, _ := newInMemStore(t)
	ctx := context.Background()

	c, err := gs.Create(ctx, `{"name":"ha.restart"}`, 5*time.Minute)
	require.NoError(t, err)

	require.NoError(t, gs.Approve(ctx, c.ID))

	got, err := gs.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, confirm.StatusApproved, got.Status)
}

func TestConfirmation_Reject(t *testing.T) {
	gs, _ := newInMemStore(t)
	ctx := context.Background()

	c, err := gs.Create(ctx, `{"name":"ha.delete"}`, 5*time.Minute)
	require.NoError(t, err)

	require.NoError(t, gs.Reject(ctx, c.ID))

	got, err := gs.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, confirm.StatusRejected, got.Status)
}

func TestConfirmation_Expire(t *testing.T) {
	gs, _ := newInMemStore(t)
	ctx := context.Background()

	// Create a confirmation that expires immediately.
	c, err := gs.Create(ctx, `{"name":"ha.power_off"}`, 1*time.Millisecond)
	require.NoError(t, err)

	// Wait for it to expire.
	time.Sleep(5 * time.Millisecond)

	count, err := gs.ExpireStale(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "one pending confirmation should be expired")

	got, err := gs.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, confirm.StatusExpired, got.Status)
}

func TestConfirmation_CleanupOlderThan7Days(t *testing.T) {
	gs, db := newInMemStore(t)
	ctx := context.Background()

	// Insert a row with an old created_at directly (8 days ago).
	oldTime := time.Now().Add(-8 * 24 * time.Hour).UnixMilli()
	_, err := db.Exec(
		`INSERT INTO confirmations (id, tool_call, status, created_at, expires_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"old-id", `{"name":"old"}`, "approved",
		oldTime, oldTime+1000, oldTime,
	)
	require.NoError(t, err)

	// Create a recent one via the API.
	_, err = gs.Create(ctx, `{"name":"recent"}`, 5*time.Minute)
	require.NoError(t, err)

	deleted, err := gs.Cleanup(ctx, 7*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "only the old row should be deleted")

	// The recent one should still be there.
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM confirmations`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "recent confirmation should remain")
}

func TestConfirmation_ApproveExpired_Fails(t *testing.T) {
	gs, _ := newInMemStore(t)
	ctx := context.Background()

	// Create with 1ms expiry, then wait.
	c, err := gs.Create(ctx, `{"name":"ha.shutdown"}`, 1*time.Millisecond)
	require.NoError(t, err)

	time.Sleep(5 * time.Millisecond)

	err = gs.Approve(ctx, c.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, confirm.ErrExpired)
}

func TestConfirmation_Get_NotFound(t *testing.T) {
	gs, _ := newInMemStore(t)
	ctx := context.Background()

	_, err := gs.Get(ctx, "nonexistent-id")
	require.Error(t, err)
	assert.ErrorIs(t, err, confirm.ErrNotFound)
}

func TestConfirmation_Reject_NotFound(t *testing.T) {
	gs, _ := newInMemStore(t)
	ctx := context.Background()

	err := gs.Reject(ctx, "nonexistent-id")
	require.Error(t, err)
	assert.ErrorIs(t, err, confirm.ErrNotFound)
}

func TestConfirmation_ExpireStale_OnlyPending(t *testing.T) {
	gs, db := newInMemStore(t)
	ctx := context.Background()

	// Insert an already-approved row with a past expiry — should NOT be changed.
	oldExpiry := time.Now().Add(-1 * time.Minute).UnixMilli()
	now := time.Now().UnixMilli()
	_, err := db.Exec(
		`INSERT INTO confirmations (id, tool_call, status, created_at, expires_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"approved-id", `{"name":"approved"}`, "approved",
		now, oldExpiry, now,
	)
	require.NoError(t, err)

	// Insert a pending row with a past expiry — should be expired.
	_, err = db.Exec(
		`INSERT INTO confirmations (id, tool_call, status, created_at, expires_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"pending-id", `{"name":"pending"}`, "pending",
		now, oldExpiry, now,
	)
	require.NoError(t, err)

	count, err := gs.ExpireStale(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "only the pending row should be expired")

	// Approved row should still be approved.
	got, err := gs.Get(ctx, "approved-id")
	require.NoError(t, err)
	assert.Equal(t, confirm.StatusApproved, got.Status)
}
