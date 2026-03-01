package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/internal/scheduler"
	"github.com/jrimmer/chandra/store"
)

// TestIntegration_SchedulerFiresIntent verifies that a due intent is picked up
// by the scheduler and emitted as a ScheduledTurn within a reasonable time.
func TestIntegration_SchedulerFiresIntent(t *testing.T) {
	ctx := context.Background()

	// 1. Real SQLite DB with migrations.
	s := openTestDB(t)
	db := s.DB()

	// 2. Create intent store and seed an already-due intent.
	intentStore := intent.NewStore(db)
	createdID := store.NewID()
	if err := intentStore.Create(ctx, intent.Intent{
		ID:          createdID,
		Description: "integration test intent",
		Condition:   "always",
		Action:      "say hello",
	}); err != nil {
		t.Fatalf("intentStore.Create: %v", err)
	}

	// Back-date NextCheck so the intent is immediately due.
	if err := intentStore.Update(ctx, intent.Intent{
		ID:          createdID,
		Description: "integration test intent",
		Condition:   "always",
		Action:      "say hello",
		Status:      intent.IntentActive,
		NextCheck:   time.Now().Add(-1 * time.Minute),
		LastChecked: time.Now().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("intentStore.Update (backdating): %v", err)
	}

	// 3. Create scheduler with 100ms tick interval.
	sched := scheduler.NewScheduler(intentStore, 100*time.Millisecond, 10)

	// 4. Start scheduler.
	if err := sched.Start(ctx); err != nil {
		t.Fatalf("sched.Start: %v", err)
	}
	defer func() {
		if err := sched.Stop(); err != nil {
			t.Logf("sched.Stop: %v", err)
		}
	}()

	// 5. Wait up to 500ms (5x tick) for a ScheduledTurn to be emitted.
	timeout := time.After(500 * time.Millisecond)
	select {
	case turn := <-sched.Turns():
		// 6. Assert: turn was emitted with the correct IntentID.
		if turn.IntentID != createdID {
			t.Errorf("expected IntentID %q, got %q", createdID, turn.IntentID)
		}
		if turn.Prompt != "say hello" {
			t.Errorf("expected prompt %q, got %q", "say hello", turn.Prompt)
		}
	case <-timeout:
		t.Fatal("timed out waiting for ScheduledTurn from scheduler")
	}
}
