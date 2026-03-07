package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openOperatorTestDB opens a test DB with all migrations applied.
func openOperatorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	s := openTestDB(t)
	return s.DB()
}

// ────────────────────────────────────────────────────────────────────────────
// Token tracking
// ────────────────────────────────────────────────────────────────────────────

// TestOperator_TokenUsage_Insert verifies the token_usage table schema and
// basic insert/query round-trip.
func TestOperator_TokenUsage_Insert(t *testing.T) {
	db := openOperatorTestDB(t)
	ctx := context.Background()
	now := time.Now().Unix()

	var insertID int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO token_usage (conv_id, user_id, channel_id, model, prompt_tokens, completion_tokens, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		"conv-abc", "user-123", "chan-456", "kimi-k2", 100, 50, now,
	).Scan(&insertID)
	require.NoError(t, err, "token_usage INSERT should succeed")

	var promptTok, completionTok int
	err = db.QueryRowContext(ctx,
		`SELECT prompt_tokens, completion_tokens FROM token_usage WHERE id = ?`, insertID,
	).Scan(&promptTok, &completionTok)
	require.NoError(t, err)
	assert.Equal(t, 100, promptTok)
	assert.Equal(t, 50, completionTok)
}

// TestOperator_TokenUsage_AggregationByDay verifies that token_usage can be
// aggregated per day (the pattern used by get_usage_stats and daemon.health).
func TestOperator_TokenUsage_AggregationByDay(t *testing.T) {
	db := openOperatorTestDB(t)
	ctx := context.Background()

	now := time.Now().Unix()
	yesterday := now - 86400

	rows := []struct {
		prompt, compl int
		ts            int64
	}{
		{100, 50, now},
		{200, 80, now},
		{150, 60, now},
		{300, 100, yesterday},
		{250, 90, yesterday},
	}
	for _, r := range rows {
		_, err := db.ExecContext(ctx,
			`INSERT INTO token_usage (conv_id, user_id, channel_id, model, prompt_tokens, completion_tokens, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"conv-1", "u1", "c1", "kimi-k2", r.prompt, r.compl, r.ts,
		)
		require.NoError(t, err)
	}

	todayStart := time.Now().Truncate(24 * time.Hour).Unix()

	var todayPrompt, todayCompl int
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0)
		 FROM token_usage WHERE created_at >= ?`, todayStart,
	).Scan(&todayPrompt, &todayCompl)
	require.NoError(t, err)
	assert.Equal(t, 450, todayPrompt, "today prompt total")
	assert.Equal(t, 190, todayCompl, "today completion total")

	var allPrompt, allCompl int
	err = db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0) FROM token_usage`,
	).Scan(&allPrompt, &allCompl)
	require.NoError(t, err)
	assert.Equal(t, 1000, allPrompt, "alltime prompt total")
	assert.Equal(t, 380, allCompl, "alltime completion total")
}

// ────────────────────────────────────────────────────────────────────────────
// Pending messages
// ────────────────────────────────────────────────────────────────────────────

// TestOperator_PendingMessages_DrainPattern verifies the insert→query→delete
// drain pattern used on daemon startup.
func TestOperator_PendingMessages_DrainPattern(t *testing.T) {
	db := openOperatorTestDB(t)
	ctx := context.Background()

	for _, msg := range []string{"Restarting with new config…", "Config confirmed ✅"} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO pending_messages (channel_id, content) VALUES (?, ?)`,
			"chan-test", msg,
		)
		require.NoError(t, err)
	}

	type pmRow struct {
		id      int64
		content string
	}
	var drained []pmRow
	qrows, err := db.QueryContext(ctx, `SELECT id, content FROM pending_messages ORDER BY created_at ASC`)
	require.NoError(t, err)
	defer qrows.Close()
	for qrows.Next() {
		var r pmRow
		require.NoError(t, qrows.Scan(&r.id, &r.content))
		drained = append(drained, r)
	}
	require.Len(t, drained, 2)
	assert.Equal(t, "Restarting with new config…", drained[0].content)

	for _, r := range drained {
		_, err = db.ExecContext(ctx, `DELETE FROM pending_messages WHERE id = ?`, r.id)
		require.NoError(t, err)
	}

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pending_messages`).Scan(&count))
	assert.Equal(t, 0, count, "pending_messages should be empty after drain")
}

// TestOperator_PendingMessages_OnlyDrainsOnce verifies that draining an empty
// table returns nothing, not an error.
func TestOperator_PendingMessages_OnlyDrainsOnce(t *testing.T) {
	db := openOperatorTestDB(t)
	ctx := context.Background()

	rows, err := db.QueryContext(ctx, `SELECT id, content FROM pending_messages ORDER BY created_at ASC`)
	require.NoError(t, err)
	defer rows.Close()
	var count int
	for rows.Next() {
		count++
	}
	assert.Equal(t, 0, count)
}

// ────────────────────────────────────────────────────────────────────────────
// Config confirmations
// ────────────────────────────────────────────────────────────────────────────

// TestOperator_ConfigConfirmations_ExpiredAutoRevert verifies that expired
// config_confirmation rows are correctly identified for auto-revert.
func TestOperator_ConfigConfirmations_ExpiredAutoRevert(t *testing.T) {
	db := openOperatorTestDB(t)
	ctx := context.Background()

	past := time.Now().Add(-2 * time.Minute).Unix()
	future := time.Now().Add(30 * time.Second).Unix()

	_, err := db.ExecContext(ctx,
		`INSERT INTO config_confirmations (key, old_value, new_value, channel_id, user_id, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"provider.base_url", "https://old.example.com/v1", "https://new.example.com/v1",
		"chan-1", "user-1", past, past-1,
	)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`INSERT INTO config_confirmations (key, old_value, new_value, channel_id, user_id, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"provider.default_model", "gpt-4o", "kimi-k2",
		"chan-1", "user-1", future, past,
	)
	require.NoError(t, err)

	now := time.Now().Unix()

	// Expired row: expires_at < now.
	var expiredKey, expiredOldVal string
	err = db.QueryRowContext(ctx,
		`SELECT key, old_value FROM config_confirmations WHERE expires_at < ? ORDER BY created_at ASC LIMIT 1`,
		now,
	).Scan(&expiredKey, &expiredOldVal)
	require.NoError(t, err)
	assert.Equal(t, "provider.base_url", expiredKey)
	assert.Equal(t, "https://old.example.com/v1", expiredOldVal, "old_value preserved for revert")

	// Live row: expires_at >= now.
	var liveKey string
	err = db.QueryRowContext(ctx,
		`SELECT key FROM config_confirmations WHERE expires_at >= ? ORDER BY created_at ASC LIMIT 1`,
		now,
	).Scan(&liveKey)
	require.NoError(t, err)
	assert.Equal(t, "provider.default_model", liveKey)
}

// TestOperator_ConfigConfirmations_PerChannelUser verifies that confirmations
// are scoped to the channel+user that initiated the change.
func TestOperator_ConfigConfirmations_PerChannelUser(t *testing.T) {
	db := openOperatorTestDB(t)
	ctx := context.Background()
	future := time.Now().Add(30 * time.Second).Unix()
	now := time.Now().Unix()

	for _, chanID := range []string{"chan-A", "chan-B"} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO config_confirmations (key, old_value, new_value, channel_id, user_id, expires_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"provider.default_model", "gpt-4o", "kimi-k2",
			chanID, "user-1", future, now,
		)
		require.NoError(t, err)
	}

	var gotChan string
	err := db.QueryRowContext(ctx,
		`SELECT channel_id FROM config_confirmations WHERE channel_id = ? AND user_id = ? AND expires_at >= ? ORDER BY created_at DESC LIMIT 1`,
		"chan-A", "user-1", now,
	).Scan(&gotChan)
	require.NoError(t, err)
	assert.Equal(t, "chan-A", gotChan, "should return chan-A confirmation only")
}

// ────────────────────────────────────────────────────────────────────────────
// Conversations list
// ────────────────────────────────────────────────────────────────────────────

// TestOperator_ListConversations_BasicQuery verifies the conversation list
// aggregation query used by list_conversations tool and CLI.
func TestOperator_ListConversations_BasicQuery(t *testing.T) {
	db := openOperatorTestDB(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	type sess struct{ id, convID, chanID string; startedAt, lastActive int64 }
	sessions := []sess{
		{"s1", "conv-1", "chan-A", now - 3000, now - 1000},
		{"s2", "conv-1", "chan-A", now - 2000, now - 500},
		{"s3", "conv-2", "chan-B", now - 5000, now - 4000},
	}
	for _, s := range sessions {
		_, err := db.ExecContext(ctx,
			`INSERT INTO sessions (id, conversation_id, channel_id, user_id, started_at, last_active) VALUES (?, ?, ?, ?, ?, ?)`,
			s.id, s.convID, s.chanID, "user-test", s.startedAt, s.lastActive,
		)
		require.NoError(t, err)
	}

	rows, err := db.QueryContext(ctx,
		`SELECT conversation_id, channel_id, COUNT(*) as turn_count, MIN(started_at), MAX(last_active)
		 FROM sessions GROUP BY conversation_id ORDER BY MAX(last_active) DESC LIMIT 10`,
	)
	require.NoError(t, err)
	defer rows.Close()

	type convRow struct {
		convID, chanID  string
		turnCount       int
		firstAt, lastAt int64
	}
	var results []convRow
	for rows.Next() {
		var r convRow
		require.NoError(t, rows.Scan(&r.convID, &r.chanID, &r.turnCount, &r.firstAt, &r.lastAt))
		results = append(results, r)
	}

	require.Len(t, results, 2)
	assert.Equal(t, "conv-1", results[0].convID, "most recently active first")
	assert.Equal(t, 2, results[0].turnCount, "conv-1 has 2 turns")
	assert.Equal(t, "conv-2", results[1].convID)
	assert.Equal(t, 1, results[1].turnCount)
}

// TestOperator_ListConversations_ChannelFilter verifies channel_id filter.
func TestOperator_ListConversations_ChannelFilter(t *testing.T) {
	db := openOperatorTestDB(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	for _, s := range []struct{ id, conv, ch string }{
		{"s1", "conv-A", "chan-1"},
		{"s2", "conv-B", "chan-2"},
	} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO sessions (id, conversation_id, channel_id, user_id, started_at, last_active) VALUES (?, ?, ?, ?, ?, ?)`,
			s.id, s.conv, s.ch, "user-test", now, now,
		)
		require.NoError(t, err)
	}

	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT conversation_id) FROM sessions WHERE channel_id = ?`, "chan-1",
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "filter should return only chan-1 conversations")
}
