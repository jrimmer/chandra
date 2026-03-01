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
	convID := agent.ComputeConversationID("channel-1", "user-1")
	sess, err := mgr.GetOrCreate(ctx, convID, "channel-1", "user-1")
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
	convID := agent.ComputeConversationID("channel-1", "user-1")

	// First call creates a new session.
	sess1, err := mgr.GetOrCreate(ctx, convID, "channel-1", "user-1")
	require.NoError(t, err)
	require.NotNil(t, sess1)

	// Second call without advancing time returns the same session.
	sess2, err := mgr.GetOrCreate(ctx, convID, "channel-1", "user-1")
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
	convID := agent.ComputeConversationID("channel-exp", "user-exp")

	sess1, err := mgr.GetOrCreate(ctx, convID, "channel-exp", "user-exp")
	require.NoError(t, err)
	require.NotNil(t, sess1)

	// Wait for the session to expire.
	time.Sleep(5 * time.Millisecond)

	sess2, err := mgr.GetOrCreate(ctx, convID, "channel-exp", "user-exp")
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
	convID := agent.ComputeConversationID("channel-touch", "user-touch")

	sess, err := mgr.GetOrCreate(ctx, convID, "channel-touch", "user-touch")
	require.NoError(t, err)
	require.NotNil(t, sess)

	before := sess.LastActive

	// Small sleep to ensure time advances.
	time.Sleep(10 * time.Millisecond)

	err = mgr.Touch(sess.ID)
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
	convID := agent.ComputeConversationID("channel-close", "user-close")

	sess, err := mgr.GetOrCreate(ctx, convID, "channel-close", "user-close")
	require.NoError(t, err)
	require.NotNil(t, sess)
	firstID := sess.ID

	// Close the session.
	err = mgr.Close(sess.ID)
	require.NoError(t, err)

	// Verify it's gone from DB.
	var count int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ?`, firstID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "closed session should be deleted from DB")

	// GetOrCreate should create a brand new session.
	sess2, err := mgr.GetOrCreate(ctx, convID, "channel-close", "user-close")
	require.NoError(t, err)
	require.NotNil(t, sess2)
	assert.NotEqual(t, firstID, sess2.ID, "after Close, GetOrCreate should produce a new session ID")
}

func TestSessionManager_Get(t *testing.T) {
	db := newTestDB(t)
	mgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	ctx := context.Background()
	convID := agent.ComputeConversationID("channel-get", "user-get")

	// Get on an unknown ID should return nil.
	assert.Nil(t, mgr.Get("nonexistent-id"), "Get should return nil for unknown session ID")

	sess, err := mgr.GetOrCreate(ctx, convID, "channel-get", "user-get")
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Get should return the session by its ID.
	found := mgr.Get(sess.ID)
	require.NotNil(t, found, "Get should find the session by ID")
	assert.Equal(t, sess.ID, found.ID)
}

func TestSessionManager_ActiveCount(t *testing.T) {
	db := newTestDB(t)
	mgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	ctx := context.Background()
	assert.Equal(t, 0, mgr.ActiveCount(), "ActiveCount should be 0 with no sessions")

	convID1 := agent.ComputeConversationID("channel-ac-1", "user-ac-1")
	_, err = mgr.GetOrCreate(ctx, convID1, "channel-ac-1", "user-ac-1")
	require.NoError(t, err)
	assert.Equal(t, 1, mgr.ActiveCount())

	convID2 := agent.ComputeConversationID("channel-ac-2", "user-ac-2")
	_, err = mgr.GetOrCreate(ctx, convID2, "channel-ac-2", "user-ac-2")
	require.NoError(t, err)
	assert.Equal(t, 2, mgr.ActiveCount())
}

func TestSessionManager_SetMaxConcurrent(t *testing.T) {
	db := newTestDB(t)
	mgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	ctx := context.Background()

	// Set limit to 2 concurrent sessions.
	mgr.SetMaxConcurrent(2)

	convID1 := agent.ComputeConversationID("channel-mc-1", "user-mc-1")
	_, err = mgr.GetOrCreate(ctx, convID1, "channel-mc-1", "user-mc-1")
	require.NoError(t, err)

	convID2 := agent.ComputeConversationID("channel-mc-2", "user-mc-2")
	_, err = mgr.GetOrCreate(ctx, convID2, "channel-mc-2", "user-mc-2")
	require.NoError(t, err)

	// Third session should be rejected.
	convID3 := agent.ComputeConversationID("channel-mc-3", "user-mc-3")
	_, err = mgr.GetOrCreate(ctx, convID3, "channel-mc-3", "user-mc-3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max concurrent sessions reached")
}

func TestSessionManager_ConcurrentGetOrCreate(t *testing.T) {
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
			convID := agent.ComputeConversationID(channelID, userID)
			sess, err := mgr.GetOrCreate(ctx, convID, channelID, userID)
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

	// Create 3 sessions and record the ID of the first one for later cache check.
	convID0 := agent.ComputeConversationID("channel-0", "user-0")
	firstSess, err := mgr.GetOrCreate(ctx, convID0, "channel-0", "user-0")
	require.NoError(t, err, "creating session 0")
	firstID := firstSess.ID

	for i := 1; i < 3; i++ {
		channelID := fmt.Sprintf("channel-%d", i)
		userID := fmt.Sprintf("user-%d", i)
		convID := agent.ComputeConversationID(channelID, userID)
		_, err := mgr.GetOrCreate(ctx, convID, channelID, userID)
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

	// Verify the cache was also evicted: GetOrCreate for the first channel/user
	// pair must return a brand-new session ID, not the stale cached one.
	sess2, err := mgr.GetOrCreate(ctx, convID0, "channel-0", "user-0")
	require.NoError(t, err)
	require.NotNil(t, sess2)
	assert.NotEqual(t, firstID, sess2.ID,
		"cache should have been evicted by CleanupExpired; expected a new session ID")
}
