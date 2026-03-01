package agent_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/agent"
	"github.com/jrimmer/chandra/store"
)

// newTestDB creates a temp file DB with migrations applied.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	db := s.DB()
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSessionManager_GetOrCreate_NewSession(t *testing.T) {
	db := newTestDB(t)
	mgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	ctx := context.Background()
	sess, err := mgr.GetOrCreate(ctx, "channel-1", "user-1")
	require.NoError(t, err)
	require.NotNil(t, sess)

	assert.NotEmpty(t, sess.ID, "session ID should be non-empty")
	assert.Equal(t, "channel-1", sess.ChannelID)
	assert.Equal(t, "user-1", sess.UserID)
	assert.NotEmpty(t, sess.ConversationID)
	assert.Len(t, sess.ConversationID, 16, "ConversationID should be 16 chars (SHA256[:16])")

	// Verify it's the correct SHA256 prefix
	wantConvID := agent.ComputeConversationID("channel-1", "user-1")
	assert.Equal(t, wantConvID, sess.ConversationID)
}

func TestSessionManager_GetOrCreate_ResumeActive(t *testing.T) {
	db := newTestDB(t)
	mgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	ctx := context.Background()

	// First call creates a new session.
	sess1, err := mgr.GetOrCreate(ctx, "channel-1", "user-1")
	require.NoError(t, err)
	require.NotNil(t, sess1)

	// Second call without advancing time returns the same session.
	sess2, err := mgr.GetOrCreate(ctx, "channel-1", "user-1")
	require.NoError(t, err)
	require.NotNil(t, sess2)

	assert.Equal(t, sess1.ID, sess2.ID, "active session should be resumed, same ID expected")
	assert.Equal(t, sess1.ConversationID, sess2.ConversationID)
}

func TestSessionManager_GetOrCreate_ExpiredCreatesNew(t *testing.T) {
	db := newTestDB(t)
	// Use a very short timeout so we can expire it quickly.
	mgr, err := agent.NewManager(db, 1*time.Millisecond)
	require.NoError(t, err)

	ctx := context.Background()

	sess1, err := mgr.GetOrCreate(ctx, "channel-exp", "user-exp")
	require.NoError(t, err)
	require.NotNil(t, sess1)

	// Wait for the session to expire.
	time.Sleep(5 * time.Millisecond)

	sess2, err := mgr.GetOrCreate(ctx, "channel-exp", "user-exp")
	require.NoError(t, err)
	require.NotNil(t, sess2)

	assert.NotEqual(t, sess1.ID, sess2.ID, "expired session should produce a new session ID")
	assert.Equal(t, sess1.ConversationID, sess2.ConversationID, "ConversationID should remain stable across sessions")
}

func TestSessionManager_Touch(t *testing.T) {
	db := newTestDB(t)
	mgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	ctx := context.Background()

	sess, err := mgr.GetOrCreate(ctx, "channel-touch", "user-touch")
	require.NoError(t, err)
	require.NotNil(t, sess)

	before := sess.LastActive

	// Small sleep to ensure time advances.
	time.Sleep(10 * time.Millisecond)

	err = mgr.Touch(ctx, sess.ID)
	require.NoError(t, err)

	// Verify DB was updated.
	var lastActive int64
	err = db.QueryRowContext(ctx, `SELECT last_active FROM sessions WHERE id = ?`, sess.ID).Scan(&lastActive)
	require.NoError(t, err)

	// last_active in DB is stored as unix milliseconds.
	updated := time.UnixMilli(lastActive).UTC()
	assert.True(t, updated.After(before) || updated.Equal(before),
		"Touch should update last_active to a time >= original (got %v, before %v)", updated, before)
}

func TestSessionManager_Close(t *testing.T) {
	db := newTestDB(t)
	mgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	ctx := context.Background()

	sess, err := mgr.GetOrCreate(ctx, "channel-close", "user-close")
	require.NoError(t, err)
	require.NotNil(t, sess)
	firstID := sess.ID

	// Close the session.
	err = mgr.Close(ctx, sess.ID)
	require.NoError(t, err)

	// Verify it's gone from DB.
	var count int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ?`, firstID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "closed session should be deleted from DB")

	// GetOrCreate should create a brand new session.
	sess2, err := mgr.GetOrCreate(ctx, "channel-close", "user-close")
	require.NoError(t, err)
	require.NotNil(t, sess2)
	assert.NotEqual(t, firstID, sess2.ID, "after Close, GetOrCreate should produce a new session ID")
}

func TestSessionManager_MaxConcurrent(t *testing.T) {
	db := newTestDB(t)
	mgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	ctx := context.Background()
	const n = 10

	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			channelID := fmt.Sprintf("channel-%d", i)
			userID := fmt.Sprintf("user-%d", i)
			sess, err := mgr.GetOrCreate(ctx, channelID, userID)
			errs[i] = err
			if sess != nil {
				ids[i] = sess.ID
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d returned error", i)
	}

	// All session IDs should be non-empty and unique.
	seen := make(map[string]bool)
	for i, id := range ids {
		assert.NotEmpty(t, id, "session %d has empty ID", i)
		assert.False(t, seen[id], "session ID %s is duplicated", id)
		seen[id] = true
	}
}

func TestSessionManager_CleanupExpired(t *testing.T) {
	db := newTestDB(t)
	// Very short timeout so sessions expire immediately.
	mgr, err := agent.NewManager(db, 1*time.Millisecond)
	require.NoError(t, err)

	ctx := context.Background()

	// Create 3 sessions.
	for i := 0; i < 3; i++ {
		_, err := mgr.GetOrCreate(ctx, fmt.Sprintf("channel-%d", i), fmt.Sprintf("user-%d", i))
		require.NoError(t, err, "creating session %d", i)
	}

	// Wait for them to expire.
	time.Sleep(10 * time.Millisecond)

	// CleanupExpired should remove all 3.
	n, err := mgr.CleanupExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n, "CleanupExpired should return 3 for 3 expired sessions")

	// Verify DB is empty.
	var count int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "all expired sessions should be deleted from DB")
}
