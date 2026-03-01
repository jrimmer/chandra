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

// createIntent is a test helper that creates an intent with a generated ID and
// returns the ID so callers can reference it in subsequent operations.
func createIntent(t *testing.T, st intent.IntentStore, ctx context.Context, description, condition, action string) string {
	t.Helper()
	id := store.NewID()
	require.NoError(t, st.Create(ctx, intent.Intent{
		ID:          id,
		Description: description,
		Condition:   condition,
		Action:      action,
	}))
	return id
}

func TestIntentStore_CreateAndActive(t *testing.T) {
	cases := []struct {
		name       string
		statuses   []intent.IntentStatus
		wantActive int
	}{
		{
			name:       "all active",
			statuses:   []intent.IntentStatus{intent.IntentActive, intent.IntentActive, intent.IntentActive},
			wantActive: 3,
		},
		{
			name:       "mixed active and completed",
			statuses:   []intent.IntentStatus{intent.IntentActive, intent.IntentCompleted, intent.IntentActive},
			wantActive: 2,
		},
		{
			name:       "all completed",
			statuses:   []intent.IntentStatus{intent.IntentCompleted, intent.IntentCompleted},
			wantActive: 0,
		},
		{
			name:       "active and paused",
			statuses:   []intent.IntentStatus{intent.IntentActive, intent.IntentPaused, intent.IntentActive},
			wantActive: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			st := intent.NewStore(db)
			ctx := context.Background()

			for i, status := range tc.statuses {
				id := createIntent(t, st, ctx, "desc", "cond", "action")

				if status != intent.IntentActive {
					require.NoError(t, st.Update(ctx, intent.Intent{
						ID:          id,
						Description: "desc",
						Condition:   "cond",
						Action:      "action",
						Status:      status,
					}))
				}
				_ = i
			}

			active, err := st.Active(ctx)
			require.NoError(t, err)
			assert.Len(t, active, tc.wantActive)
			for _, a := range active {
				assert.Equal(t, intent.IntentActive, a.Status)
			}
		})
	}
}

func TestIntentStore_Due(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name      string
		nextCheck time.Time
		status    intent.IntentStatus
		wantInDue bool
	}{
		{
			name:      "past NextCheck appears in Due",
			nextCheck: now.Add(-1 * time.Minute),
			status:    intent.IntentActive,
			wantInDue: true,
		},
		{
			name:      "future NextCheck does not appear in Due",
			nextCheck: now.Add(10 * time.Minute),
			status:    intent.IntentActive,
			wantInDue: false,
		},
		{
			name:      "completed status with past NextCheck does not appear in Due",
			nextCheck: now.Add(-1 * time.Minute),
			status:    intent.IntentCompleted,
			wantInDue: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			st := intent.NewStore(db)
			ctx := context.Background()

			id := createIntent(t, st, ctx, "desc", "cond", "action")
			require.NoError(t, st.Update(ctx, intent.Intent{
				ID:        id,
				Condition: "cond",
				Action:    "action",
				NextCheck: tc.nextCheck,
				Status:    tc.status,
			}))

			due, err := st.Due(ctx)
			require.NoError(t, err)

			found := false
			for _, d := range due {
				if d.ID == id {
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

	id := createIntent(t, st, ctx, "to complete", "cond", "action")

	// Verify it appears in Active before completing.
	active, err := st.Active(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)

	// Complete it.
	require.NoError(t, st.Complete(ctx, id))

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

	id := createIntent(t, st, ctx, "original desc", "original cond", "original action")

	// Update all mutable fields.
	updated := intent.Intent{
		ID:          id,
		Description: "updated desc",
		Condition:   "updated cond",
		Action:      "updated action",
		Status:      intent.IntentActive,
		LastChecked: now.Add(-5 * time.Minute),
		NextCheck:   now.Add(-1 * time.Millisecond), // make it due
	}
	require.NoError(t, st.Update(ctx, updated))

	// Verify changes via Due()
	due, err := st.Due(ctx)
	require.NoError(t, err)
	require.Len(t, due, 1)

	got := due[0]
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "updated desc", got.Description)
	assert.Equal(t, "updated cond", got.Condition)
	assert.Equal(t, "updated action", got.Action)

	// Times are stored as unix ms so truncate for comparison.
	assert.Equal(t, updated.LastChecked.UnixMilli(), got.LastChecked.UnixMilli())
	assert.Equal(t, updated.NextCheck.UnixMilli(), got.NextCheck.UnixMilli())
}
