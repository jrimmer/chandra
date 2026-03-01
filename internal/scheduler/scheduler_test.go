package scheduler_test

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
	"github.com/jrimmer/chandra/internal/scheduler"
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

// createDueIntent creates an intent with NextCheck in the past so it is immediately due.
func createDueIntent(t *testing.T, st intent.IntentStore, ctx context.Context) intent.Intent {
	t.Helper()
	id := store.NewID()
	require.NoError(t, st.Create(ctx, intent.Intent{
		ID:          id,
		Description: "due intent",
		Condition:   "cond",
		Action:      "do something",
	}))
	in := intent.Intent{
		ID:          id,
		Description: "due intent",
		Condition:   "cond",
		Action:      "do something",
		Status:      intent.IntentActive,
		NextCheck:   time.Now().Add(-1 * time.Minute),
	}
	require.NoError(t, st.Update(ctx, in))
	return in
}

func TestScheduler_EmitsDueTurns(t *testing.T) {
	db := newTestDB(t)
	st := intent.NewStore(db)
	ctx := context.Background()

	in := createDueIntent(t, st, ctx)

	sched := scheduler.NewScheduler(st, 50*time.Millisecond, 10)
	require.NoError(t, sched.Start(ctx))
	defer sched.Stop() //nolint:errcheck

	select {
	case turn := <-sched.Turns():
		assert.Equal(t, in.ID, turn.IntentID)
		assert.Equal(t, in.Action, turn.Prompt)
		assert.Equal(t, "scheduler", turn.SessionID)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for ScheduledTurn")
	}
}

func TestScheduler_UpdatesLastChecked(t *testing.T) {
	db := newTestDB(t)
	st := intent.NewStore(db)
	ctx := context.Background()

	in := createDueIntent(t, st, ctx)
	originalLastChecked := in.LastChecked

	// Use a long tick interval so the first tick fires, updates the intent with
	// NextCheck = now + 500ms, and the second tick does not fire before we check.
	tickInterval := 500 * time.Millisecond
	sched := scheduler.NewScheduler(st, tickInterval, 10)
	require.NoError(t, sched.Start(ctx))
	defer sched.Stop() //nolint:errcheck

	// Wait for a turn to be emitted, confirming the tick ran.
	select {
	case <-sched.Turns():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ScheduledTurn")
	}

	// Stop the scheduler so no further ticks can re-set the intent.
	require.NoError(t, sched.Stop())

	// Check that LastChecked advanced. We query Active() to find the intent.
	active, err := st.Active(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.True(t, active[0].LastChecked.After(originalLastChecked),
		"LastChecked should have advanced after a tick")

	// The intent should not be due since NextCheck was pushed into the future.
	due, err := st.Due(ctx)
	require.NoError(t, err)
	dueIDs := make(map[string]bool)
	for _, d := range due {
		dueIDs[d.ID] = true
	}
	assert.False(t, dueIDs[in.ID], "intent should not be due after update")
}

func TestScheduler_SkipsWhenChannelFull(t *testing.T) {
	db := newTestDB(t)
	st := intent.NewStore(db)
	ctx := context.Background()

	// Create 3 due intents.
	for i := 0; i < 3; i++ {
		createDueIntent(t, st, ctx)
	}

	// Buffer size 0 is not valid for make(chan, 0) send in non-blocking select,
	// but size 1 lets us verify drop behavior without deadlock.
	sched := scheduler.NewScheduler(st, 50*time.Millisecond, 1)
	require.NoError(t, sched.Start(ctx))

	// Don't read from the channel — this forces drops after the first item.
	// The scheduler should not deadlock.
	time.Sleep(200 * time.Millisecond)

	require.NoError(t, sched.Stop())
}

func TestScheduler_StopsCleanly(t *testing.T) {
	db := newTestDB(t)
	st := intent.NewStore(db)
	ctx := context.Background()

	sched := scheduler.NewScheduler(st, 50*time.Millisecond, 10)
	require.NoError(t, sched.Start(ctx))

	// Stop should return promptly without leaking goroutines.
	done := make(chan struct{})
	go func() {
		defer close(done)
		require.NoError(t, sched.Stop())
	}()

	select {
	case <-done:
		// Stopped cleanly.
	case <-time.After(1 * time.Second):
		t.Fatal("Stop() did not return within 1 second — possible goroutine leak")
	}
}
