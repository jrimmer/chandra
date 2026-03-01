package actionlog_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/actionlog"
	"github.com/jrimmer/chandra/store"
)

// newTestDB opens a temp file-based SQLite DB with migrations applied.
// Importing store causes its init() to call vec.Auto(), registering sqlite-vec
// before any query runs.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	db := s.DB()
	t.Cleanup(func() { db.Close() })
	return db
}

// insertSession inserts a minimal session row to satisfy the FK constraint on
// action_log.session_id when a non-NULL session_id is required.
func insertSession(t *testing.T, db *sql.DB, sessionID string) {
	t.Helper()
	now := time.Now().Unix()
	_, err := db.Exec(
		`INSERT INTO sessions (id, conversation_id, channel_id, user_id, started_at, last_active)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, "conv-1", "ch-1", "user-1", now, now,
	)
	require.NoError(t, err)
}

func TestActionLog_RecordAndQuery(t *testing.T) {
	db := newTestDB(t)
	insertSession(t, db, "session-1")
	log, err := actionlog.NewLog(db)
	require.NoError(t, err)

	ctx := context.Background()
	base := time.Now().Truncate(time.Second).UTC()

	// Record 3 actions; all will fall within our query window.
	for i := 0; i < 3; i++ {
		require.NoError(t, log.Record(ctx, "session-1", actionlog.ActionToolCall, `{"tool":"search"}`))
	}

	since := base.Add(-time.Second)
	until := base.Add(time.Minute)

	actions, err := log.Query(ctx, since, until, "")
	require.NoError(t, err)
	assert.Len(t, actions, 3, "Query should return all 3 recorded actions")

	for _, a := range actions {
		assert.NotEmpty(t, a.ID)
		assert.Equal(t, "session-1", a.SessionID)
		assert.Equal(t, actionlog.ActionToolCall, a.Type)
		assert.False(t, a.Timestamp.IsZero())
	}
}

func TestActionLog_Recent(t *testing.T) {
	db := newTestDB(t)
	// Use empty session_id (nullable) so no FK required.
	log, err := actionlog.NewLog(db)
	require.NoError(t, err)

	ctx := context.Background()

	// Record 5 actions with no session (nullable FK).
	for i := 0; i < 5; i++ {
		require.NoError(t, log.Record(ctx, "", actionlog.ActionMessageSent, "msg"))
	}

	recent, err := log.Recent(ctx, 3)
	require.NoError(t, err)
	assert.Len(t, recent, 3, "Recent(3) should return exactly 3 actions")

	// Verify newest-first ordering.
	for i := 1; i < len(recent); i++ {
		assert.False(t, recent[i].Timestamp.After(recent[i-1].Timestamp),
			"actions should be newest-first: index %d (%v) after index %d (%v)",
			i, recent[i].Timestamp, i-1, recent[i-1].Timestamp)
	}
}

func TestActionLog_FilterByType(t *testing.T) {
	db := newTestDB(t)
	// Use empty session_id (nullable) so no FK required.
	log, err := actionlog.NewLog(db)
	require.NoError(t, err)

	ctx := context.Background()

	// Record a mix of action types (no session_id to avoid FK).
	require.NoError(t, log.Record(ctx, "", actionlog.ActionToolCall, `{"tool":"search"}`))
	require.NoError(t, log.Record(ctx, "", actionlog.ActionMessageSent, "hello"))
	require.NoError(t, log.Record(ctx, "", actionlog.ActionToolCall, `{"tool":"weather"}`))
	require.NoError(t, log.Record(ctx, "", actionlog.ActionError, "something failed"))
	require.NoError(t, log.Record(ctx, "", actionlog.ActionMessageSent, "world"))

	since := time.Now().Add(-time.Minute)
	until := time.Now().Add(time.Minute)

	toolCalls, err := log.Query(ctx, since, until, actionlog.ActionToolCall)
	require.NoError(t, err)
	assert.Len(t, toolCalls, 2, "should return only tool_call actions")
	for _, a := range toolCalls {
		assert.Equal(t, actionlog.ActionToolCall, a.Type)
	}

	messages, err := log.Query(ctx, since, until, actionlog.ActionMessageSent)
	require.NoError(t, err)
	assert.Len(t, messages, 2, "should return only message_sent actions")
	for _, a := range messages {
		assert.Equal(t, actionlog.ActionMessageSent, a.Type)
	}

	// Empty filter returns all.
	all, err := log.Query(ctx, since, until, "")
	require.NoError(t, err)
	assert.Len(t, all, 5, "empty filter should return all actions")
}

func TestActionLog_GenerateHourlyRollup(t *testing.T) {
	db := newTestDB(t)
	log, err := actionlog.NewLog(db)
	require.NoError(t, err)

	ctx := context.Background()

	// Insert 20 actions: tool_calls with JSON details containing the "tool" key,
	// plus 2 non-tool-call actions, totalling 20.
	toolDetails := []struct {
		tool  string
		count int
	}{
		{"search", 8},
		{"weather", 6},
		{"calendar", 4},
	}

	for _, td := range toolDetails {
		details, _ := json.Marshal(map[string]string{"tool": td.tool})
		for i := 0; i < td.count; i++ {
			require.NoError(t, log.Record(ctx, "", actionlog.ActionToolCall, string(details)))
		}
	}
	// 2 non-tool-call actions.
	require.NoError(t, log.Record(ctx, "", actionlog.ActionMessageSent, "hello"))
	require.NoError(t, log.Record(ctx, "", actionlog.ActionError, "oops"))

	hour := time.Now().Truncate(time.Hour).UTC()
	rollup, err := log.GenerateHourlyRollup(ctx, hour)
	require.NoError(t, err)
	require.NotNil(t, rollup)

	assert.Equal(t, "hourly", rollup.Period)
	assert.Equal(t, 20, rollup.ActionCount)
	assert.NotEmpty(t, rollup.Summary)
	assert.NotEmpty(t, rollup.TopTools, "top tools should be populated from tool_call details")
	assert.NotEmpty(t, rollup.ID)

	// Verify top tools JSON contains the most frequent tools.
	var topTools []string
	require.NoError(t, json.Unmarshal([]byte(rollup.TopTools), &topTools),
		"TopTools should be a valid JSON array")
	assert.Contains(t, topTools, "search", "most frequent tool 'search' should appear in top tools")
}

func TestActionLog_RollupIdempotent(t *testing.T) {
	db := newTestDB(t)
	log, err := actionlog.NewLog(db)
	require.NoError(t, err)

	ctx := context.Background()

	// Record a few actions.
	for i := 0; i < 5; i++ {
		details, _ := json.Marshal(map[string]string{"tool": "search"})
		require.NoError(t, log.Record(ctx, "", actionlog.ActionToolCall, string(details)))
	}

	hour := time.Now().Truncate(time.Hour).UTC()

	rollup1, err := log.GenerateHourlyRollup(ctx, hour)
	require.NoError(t, err)
	require.NotNil(t, rollup1)

	rollup2, err := log.GenerateHourlyRollup(ctx, hour)
	require.NoError(t, err)
	require.NotNil(t, rollup2)

	// Both calls should return equivalent results.
	assert.Equal(t, rollup1.Period, rollup2.Period)
	assert.Equal(t, rollup1.ActionCount, rollup2.ActionCount)
	assert.Equal(t, rollup1.Summary, rollup2.Summary)
	assert.Equal(t, rollup1.TopTools, rollup2.TopTools)

	// GetRollup should retrieve the persisted rollup.
	got, err := log.GetRollup(ctx, "hourly", hour)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "hourly", got.Period)
	assert.Equal(t, 5, got.ActionCount)
}
