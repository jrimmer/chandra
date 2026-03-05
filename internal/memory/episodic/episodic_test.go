package episodic_test

import (
	"context"
	"fmt"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/memory/episodic"
	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
)

// newTestDB creates a temporary database with migrations applied.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	db := s.DB()
	t.Cleanup(func() { db.Close() })
	return db
}

// insertSession inserts a session row required by the FK constraint on episodes.
func insertSession(t *testing.T, db *sql.DB, sessionID string) {
	t.Helper()
	now := time.Now().Unix()
	_, err := db.Exec(
		`INSERT INTO sessions (id, conversation_id, channel_id, user_id, started_at, last_active) VALUES (?,?,?,?,?,?)`,
		sessionID, "conv-1", "ch-1", "user-1", now, now,
	)
	require.NoError(t, err)
}

func TestEpisodicStore_AppendAndRecent(t *testing.T) {
	cases := []struct {
		name    string
		insert  int
		limit   int
		wantLen int
	}{
		{"one episode limit 1", 1, 1, 1},
		{"five episodes limit 3", 5, 3, 3},
		{"ten episodes limit 10", 10, 10, 10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			insertSession(t, db, "session-1")
			st := episodic.NewStore(db)
			ctx := context.Background()

			baseTime := time.Now().Truncate(time.Second).UTC()
			for i := 0; i < tc.insert; i++ {
				ep := pkg.Episode{
					SessionID: "session-1",
					Role:      "user",
					Content:   "message",
					Timestamp: baseTime.Add(time.Duration(i) * time.Second),
				}
				require.NoError(t, st.Append(ctx, ep))
			}

			got, err := st.Recent(ctx, "session-1", tc.limit)
			require.NoError(t, err)
			assert.Len(t, got, tc.wantLen)

			// Verify newest-first order.
			for i := 1; i < len(got); i++ {
				assert.True(t, !got[i].Timestamp.After(got[i-1].Timestamp),
					"episodes should be newest-first: index %d (%v) is after index %d (%v)",
					i, got[i].Timestamp, i-1, got[i-1].Timestamp)
			}
		})
	}
}

func TestEpisodicStore_Since(t *testing.T) {
	db := newTestDB(t)
	insertSession(t, db, "session-1")
	st := episodic.NewStore(db)
	ctx := context.Background()

	base := time.Now().Truncate(time.Second).UTC()
	cutoff := base.Add(2 * time.Second)

	// Insert 5 episodes: 2 before cutoff, 3 after.
	for i := 0; i < 5; i++ {
		ep := pkg.Episode{
			SessionID: "session-1",
			Role:      "user",
			Content:   "message",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, st.Append(ctx, ep))
	}

	got, err := st.Since(ctx, cutoff)
	require.NoError(t, err)
	// Episodes at base+2s, base+3s, base+4s are strictly after the cutoff.
	assert.Len(t, got, 2, "Since should return only episodes strictly after the cutoff")
	for _, ep := range got {
		assert.True(t, ep.Timestamp.After(cutoff),
			"episode timestamp %v should be after cutoff %v", ep.Timestamp, cutoff)
	}
}

func TestEpisodicStore_TagsRoundTrip(t *testing.T) {
	db := newTestDB(t)
	insertSession(t, db, "session-1")
	st := episodic.NewStore(db)
	ctx := context.Background()

	ep := pkg.Episode{
		SessionID: "session-1",
		Role:      "user",
		Content:   "tagged message",
		Timestamp: time.Now().Truncate(time.Second).UTC(),
		Tags:      []string{"work", "important"},
	}
	require.NoError(t, st.Append(ctx, ep))

	got, err := st.Recent(ctx, "session-1", 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, []string{"work", "important"}, got[0].Tags)
}

func TestEpisodicStore_SessionIsolation(t *testing.T) {
	db := newTestDB(t)
	insertSession(t, db, "session-1")
	insertSession(t, db, "session-2")
	st := episodic.NewStore(db)
	ctx := context.Background()

	base := time.Now().Truncate(time.Second).UTC()

	// Insert 3 episodes into session-1 and 2 into session-2.
	for i := 0; i < 3; i++ {
		ep := pkg.Episode{
			SessionID: "session-1",
			Role:      "user",
			Content:   "session-1 message",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, st.Append(ctx, ep))
	}
	for i := 0; i < 2; i++ {
		ep := pkg.Episode{
			SessionID: "session-2",
			Role:      "assistant",
			Content:   "session-2 message",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, st.Append(ctx, ep))
	}

	got1, err := st.Recent(ctx, "session-1", 10)
	require.NoError(t, err)
	assert.Len(t, got1, 3, "session-1 should have 3 episodes")
	for _, ep := range got1 {
		assert.Equal(t, "session-1", ep.SessionID)
	}

	got2, err := st.Recent(ctx, "session-2", 10)
	require.NoError(t, err)
	assert.Len(t, got2, 2, "session-2 should have 2 episodes")
	for _, ep := range got2 {
		assert.Equal(t, "session-2", ep.SessionID)
	}
}

// TestEpisodicStore_RecentAcrossSessions verifies that RecentAcrossSessions
// returns episodes from multiple sessions for the same channel+user pair,
// in descending timestamp order, up to the requested limit.
func TestEpisodicStore_RecentAcrossSessions(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Two sessions for the same user/channel.
	_, err := db.Exec(
		`INSERT INTO sessions (id, conversation_id, channel_id, user_id, started_at, last_active) VALUES (?,?,?,?,?,?)`,
		"sess-A", "conv-A", "ch-X", "user-X", time.Now().UnixMilli(), time.Now().UnixMilli(),
	)
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO sessions (id, conversation_id, channel_id, user_id, started_at, last_active) VALUES (?,?,?,?,?,?)`,
		"sess-B", "conv-B", "ch-X", "user-X", time.Now().UnixMilli(), time.Now().UnixMilli(),
	)
	require.NoError(t, err)

	// A third session for a different user — should NOT appear in results.
	_, err = db.Exec(
		`INSERT INTO sessions (id, conversation_id, channel_id, user_id, started_at, last_active) VALUES (?,?,?,?,?,?)`,
		"sess-other", "conv-other", "ch-X", "user-Y", time.Now().UnixMilli(), time.Now().UnixMilli(),
	)
	require.NoError(t, err)

	st := episodic.NewStore(db)
	base := time.Now()

	// Append two episodes to sess-A and two to sess-B, interleaved timestamps.
	epA1 := pkg.Episode{ID: "eA1", SessionID: "sess-A", Role: "user", Content: "session A first", Timestamp: base.Add(-4 * time.Second)}
	epA2 := pkg.Episode{ID: "eA2", SessionID: "sess-A", Role: "assistant", Content: "session A second", Timestamp: base.Add(-2 * time.Second)}
	epB1 := pkg.Episode{ID: "eB1", SessionID: "sess-B", Role: "user", Content: "session B first", Timestamp: base.Add(-3 * time.Second)}
	epB2 := pkg.Episode{ID: "eB2", SessionID: "sess-B", Role: "assistant", Content: "session B second", Timestamp: base.Add(-1 * time.Second)}
	epOther := pkg.Episode{ID: "eOther", SessionID: "sess-other", Role: "user", Content: "other user episode", Timestamp: base}

	for _, ep := range []pkg.Episode{epA1, epA2, epB1, epB2, epOther} {
		require.NoError(t, st.Append(ctx, ep))
	}

	// Should return all 4 episodes for user-X, newest first, excluding user-Y.
	got, err := st.RecentAcrossSessions(ctx, "ch-X", "user-X", 10)
	require.NoError(t, err)
	assert.Len(t, got, 4, "should return episodes from both sessions")

	// Newest first: eB2 (-1s), eA2 (-2s), eB1 (-3s), eA1 (-4s)
	assert.Equal(t, "eB2", got[0].ID, "newest episode first")
	assert.Equal(t, "eA2", got[1].ID)
	assert.Equal(t, "eB1", got[2].ID)
	assert.Equal(t, "eA1", got[3].ID, "oldest episode last")

	// Verify user-Y episode is absent.
	for _, ep := range got {
		assert.NotEqual(t, "eOther", ep.ID, "other user episode must not appear")
	}
}

// TestEpisodicStore_RecentAcrossSessions_Limit verifies the n parameter is respected.
func TestEpisodicStore_RecentAcrossSessions_Limit(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	_, err := db.Exec(
		`INSERT INTO sessions (id, conversation_id, channel_id, user_id, started_at, last_active) VALUES (?,?,?,?,?,?)`,
		"sess-lim", "conv-lim", "ch-lim", "user-lim", time.Now().UnixMilli(), time.Now().UnixMilli(),
	)
	require.NoError(t, err)

	st := episodic.NewStore(db)
	base := time.Now()
	for i := 0; i < 5; i++ {
		ep := pkg.Episode{
			ID:        fmt.Sprintf("e%d", i),
			SessionID: "sess-lim",
			Role:      "user",
			Content:   fmt.Sprintf("message %d", i),
			Timestamp: base.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, st.Append(ctx, ep))
	}

	got, err := st.RecentAcrossSessions(ctx, "ch-lim", "user-lim", 3)
	require.NoError(t, err)
	assert.Len(t, got, 3, "limit should be respected")
	// Most recent 3 are e4, e3, e2
	assert.Equal(t, "e4", got[0].ID)
	assert.Equal(t, "e3", got[1].ID)
	assert.Equal(t, "e2", got[2].ID)
}

// TestEpisodicStore_RecentAcrossSessions_Empty verifies empty result for unknown user.
func TestEpisodicStore_RecentAcrossSessions_Empty(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	st := episodic.NewStore(db)

	got, err := st.RecentAcrossSessions(ctx, "no-such-channel", "no-such-user", 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}
