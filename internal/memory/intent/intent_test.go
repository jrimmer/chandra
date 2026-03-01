package intent_test

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

	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/store"
)

func init() {
	vec.Auto()
}

// newTestDB opens a migrated SQLite database in a temp dir for tests.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	db := s.DB()
	t.Cleanup(func() { db.Close() })
	return db
}

func TestIntentStore_CreateAndActive(t *testing.T) {
	cases := []struct {
		name        string
		statuses    []intent.IntentStatus
		wantActive  int
	}{
		{
			name:       "all active",
			statuses:   []intent.IntentStatus{intent.StatusActive, intent.StatusActive, intent.StatusActive},
			wantActive: 3,
		},
		{
			name:       "mixed active and completed",
			statuses:   []intent.IntentStatus{intent.StatusActive, intent.StatusCompleted, intent.StatusActive},
			wantActive: 2,
		},
		{
			name:       "all completed",
			statuses:   []intent.IntentStatus{intent.StatusCompleted, intent.StatusCompleted},
			wantActive: 0,
		},
		{
			name:       "active and paused",
			statuses:   []intent.IntentStatus{intent.StatusActive, intent.StatusPaused, intent.StatusActive},
			wantActive: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			st := intent.NewStore(db)
			ctx := context.Background()

			for i, status := range tc.statuses {
				in, err := st.Create(ctx, "desc", "cond", "action")
				require.NoError(t, err)
				require.NotEmpty(t, in.ID)

				if status != intent.StatusActive {
					in.Status = status
					require.NoError(t, st.Update(ctx, in))
				}
				_ = i
			}

			active, err := st.Active(ctx)
			require.NoError(t, err)
			assert.Len(t, active, tc.wantActive)
			for _, a := range active {
				assert.Equal(t, intent.StatusActive, a.Status)
			}
		})
	}
}

func TestIntentStore_Due(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name          string
		nextCheck     time.Time
		status        intent.IntentStatus
		wantInDue     bool
	}{
		{
			name:      "past NextCheck appears in Due",
			nextCheck: now.Add(-1 * time.Minute),
			status:    intent.StatusActive,
			wantInDue: true,
		},
		{
			name:      "future NextCheck does not appear in Due",
			nextCheck: now.Add(10 * time.Minute),
			status:    intent.StatusActive,
			wantInDue: false,
		},
		{
			name:      "completed status with past NextCheck does not appear in Due",
			nextCheck: now.Add(-1 * time.Minute),
			status:    intent.StatusCompleted,
			wantInDue: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			st := intent.NewStore(db)
			ctx := context.Background()

			in, err := st.Create(ctx, "desc", "cond", "action")
			require.NoError(t, err)
			require.NotEmpty(t, in.ID)

			in.NextCheck = tc.nextCheck
			in.Status = tc.status
			require.NoError(t, st.Update(ctx, in))

			due, err := st.Due(ctx)
			require.NoError(t, err)

			found := false
			for _, d := range due {
				if d.ID == in.ID {
					found = true
				}
			}
			assert.Equal(t, tc.wantInDue, found)
		})
	}
}

func TestIntentStore_Complete(t *testing.T) {
	db := newTestDB(t)
	st := intent.NewStore(db)
	ctx := context.Background()

	in, err := st.Create(ctx, "to complete", "cond", "action")
	require.NoError(t, err)

	// Verify it appears in Active before completing.
	active, err := st.Active(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)

	// Complete it.
	require.NoError(t, st.Complete(ctx, in.ID))

	// Verify it is gone from Active.
	active, err = st.Active(ctx)
	require.NoError(t, err)
	assert.Len(t, active, 0, "completed intent should not appear in Active()")
}

func TestIntentStore_Update(t *testing.T) {
	db := newTestDB(t)
	st := intent.NewStore(db)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	in, err := st.Create(ctx, "original desc", "original cond", "original action")
	require.NoError(t, err)

	// Update all mutable fields.
	in.Description = "updated desc"
	in.Condition = "updated cond"
	in.Action = "updated action"
	in.LastChecked = now.Add(-5 * time.Minute)
	in.NextCheck = now.Add(-1 * time.Millisecond) // make it due
	require.NoError(t, st.Update(ctx, in))

	// Verify changes via Due()
	due, err := st.Due(ctx)
	require.NoError(t, err)
	require.Len(t, due, 1)

	got := due[0]
	assert.Equal(t, in.ID, got.ID)
	assert.Equal(t, "updated desc", got.Description)
	assert.Equal(t, "updated cond", got.Condition)
	assert.Equal(t, "updated action", got.Action)

	// Times are stored as unix ms so truncate for comparison.
	assert.Equal(t, in.LastChecked.UnixMilli(), got.LastChecked.UnixMilli())
	assert.Equal(t, in.NextCheck.UnixMilli(), got.NextCheck.UnixMilli())
}
