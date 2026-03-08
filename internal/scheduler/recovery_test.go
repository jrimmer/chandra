package scheduler_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/internal/scheduler"
	"github.com/jrimmer/chandra/store"
)

func TestRecoverMissedJobs_RecurringOverdue(t *testing.T) {
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	db := s.DB()
	t.Cleanup(func() { db.Close() })

	st := intent.NewStore(db)
	ctx := context.Background()

	past := time.Now().Add(-2 * time.Hour)

	for i := 0; i < 3; i++ {
		in := intent.Intent{
			Description:        "heartbeat",
			Action:             "check things",
			RecurrenceInterval: 30 * time.Minute,
			NextCheck:          past,
			ChannelID:          "ch1",
			UserID:             "u1",
		}
		if err := st.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	n, err := scheduler.RecoverMissedJobs(ctx, st, 5*time.Second)
	if err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 recovered, got %d", n)
	}

	due, err := st.Due(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("expected 0 due after recovery, got %d", len(due))
	}
}

func TestRecoverMissedJobs_OneShotLeftAlone(t *testing.T) {
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	db := s.DB()
	t.Cleanup(func() { db.Close() })

	st := intent.NewStore(db)
	ctx := context.Background()

	in := intent.Intent{
		Description:        "reminder",
		Action:             "remind user",
		RecurrenceInterval: 0,
		NextCheck:          time.Now().Add(-1 * time.Hour),
		ChannelID:          "ch1",
		UserID:             "u1",
	}
	if err := st.Create(ctx, in); err != nil {
		t.Fatal(err)
	}

	n, err := scheduler.RecoverMissedJobs(ctx, st, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 recovered (one-shot left alone), got %d", n)
	}

	due, err := st.Due(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due one-shot intent, got %d", len(due))
	}
}

func TestRecoverMissedJobs_FutureIntentUntouched(t *testing.T) {
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	db := s.DB()
	t.Cleanup(func() { db.Close() })

	st := intent.NewStore(db)
	ctx := context.Background()

	in := intent.Intent{
		Description:        "future check",
		Action:             "check later",
		RecurrenceInterval: 30 * time.Minute,
		NextCheck:          time.Now().Add(1 * time.Hour),
		ChannelID:          "ch1",
		UserID:             "u1",
	}
	if err := st.Create(ctx, in); err != nil {
		t.Fatal(err)
	}

	n, err := scheduler.RecoverMissedJobs(ctx, st, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 recovered for future intent, got %d", n)
	}
}
