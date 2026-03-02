package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPlanStore_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	plan := &PlanRecord{
		ID:        NewID(),
		Goal:      "deploy staging",
		Status:    "planning",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}

	err := s.CreatePlan(ctx, plan)
	require.NoError(t, err)

	got, err := s.GetPlan(ctx, plan.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.ID, got.ID)
	assert.Equal(t, "deploy staging", got.Goal)
	assert.Equal(t, "planning", got.Status)
}

func TestPlanStore_UpdateStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	plan := &PlanRecord{
		ID:        NewID(),
		Goal:      "test plan",
		Status:    "planning",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
	require.NoError(t, s.CreatePlan(ctx, plan))

	err := s.UpdatePlanStatus(ctx, plan.ID, "executing", 0)
	require.NoError(t, err)

	got, err := s.GetPlan(ctx, plan.ID)
	require.NoError(t, err)
	assert.Equal(t, "executing", got.Status)
}

func TestPlanStore_ListByStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, status := range []string{"executing", "executing", "completed"} {
		plan := &PlanRecord{
			ID:        NewID(),
			Goal:      "plan " + status,
			Status:    status,
			CreatedAt: time.Now().Unix(),
			UpdatedAt: time.Now().Unix(),
		}
		require.NoError(t, s.CreatePlan(ctx, plan))
	}

	plans, err := s.ListPlansByStatus(ctx, "executing")
	require.NoError(t, err)
	assert.Len(t, plans, 2)
}

func TestStepStore_CreateAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	planID := NewID()
	plan := &PlanRecord{
		ID:        planID,
		Goal:      "test steps",
		Status:    "planning",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
	require.NoError(t, s.CreatePlan(ctx, plan))

	step := &StepRecord{
		ID:          NewID(),
		PlanID:      planID,
		StepIndex:   0,
		Description: "first step",
		Action:      "exec",
		Status:      "pending",
	}
	require.NoError(t, s.CreateStep(ctx, step))

	steps, err := s.ListSteps(ctx, planID)
	require.NoError(t, err)
	require.Len(t, steps, 1)
	assert.Equal(t, "first step", steps[0].Description)
}

func TestStepStore_UpdateStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	planID := NewID()
	plan := &PlanRecord{
		ID:        planID,
		Goal:      "test step update",
		Status:    "executing",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
	require.NoError(t, s.CreatePlan(ctx, plan))

	stepID := NewID()
	step := &StepRecord{
		ID:          stepID,
		PlanID:      planID,
		StepIndex:   0,
		Description: "step one",
		Action:      "exec",
		Status:      "pending",
	}
	require.NoError(t, s.CreateStep(ctx, step))

	now := time.Now().Unix()
	output := json.RawMessage(`{"result":"ok"}`)
	err := s.UpdateStepStatus(ctx, stepID, "completed", &output, nil, &now)
	require.NoError(t, err)

	steps, err := s.ListSteps(ctx, planID)
	require.NoError(t, err)
	assert.Equal(t, "completed", steps[0].Status)
	assert.NotNil(t, steps[0].CompletedAt)
}

func TestStepStore_BatchUpdateHeartbeats(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	planID := NewID()
	plan := &PlanRecord{
		ID:        planID,
		Goal:      "heartbeat test",
		Status:    "executing",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
	require.NoError(t, s.CreatePlan(ctx, plan))

	stepIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		stepIDs[i] = NewID()
		step := &StepRecord{
			ID:          stepIDs[i],
			PlanID:      planID,
			StepIndex:   i,
			Description: "step",
			Action:      "exec",
			Status:      "running",
		}
		require.NoError(t, s.CreateStep(ctx, step))
	}

	now := time.Now()
	updates := map[string]time.Time{
		stepIDs[0]: now,
		stepIDs[2]: now.Add(-1 * time.Minute),
	}
	err := s.BatchUpdateHeartbeats(updates)
	require.NoError(t, err)

	// Verify heartbeats were written
	steps, err := s.ListSteps(ctx, planID)
	require.NoError(t, err)

	assert.NotNil(t, steps[0].Heartbeat)
	assert.Nil(t, steps[1].Heartbeat, "step 1 should have nil heartbeat")
	assert.NotNil(t, steps[2].Heartbeat)
}

func TestApprovedCommandStore_CreateAndQuery(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cmd := &ApprovedCommandRecord{
		ID:               NewID(),
		SkillName:        "git-ops",
		CommandTemplate:  "git *",
		ApprovedBy:       "user1",
		ApprovedAt:       time.Now().Unix(),
	}
	require.NoError(t, s.CreateApprovedCommand(ctx, cmd))

	cmds, err := s.ListApprovedCommands(ctx, "git-ops")
	require.NoError(t, err)
	require.Len(t, cmds, 1)
	assert.Equal(t, "git *", cmds[0].CommandTemplate)
}

func TestNotificationStore_CreateAndDeliver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	notif := &NotificationRecord{
		ID:         NewID(),
		UserID:     "user1",
		Message:    "Plan completed",
		SourceType: "plan",
		SourceID:   stringPtr("plan-123"),
		CreatedAt:  time.Now().Unix(),
		ExpiresAt:  time.Now().Add(24 * time.Hour).Unix(),
	}
	require.NoError(t, s.CreateNotification(ctx, notif))

	pending, err := s.ListPendingNotifications(ctx, "user1")
	require.NoError(t, err)
	require.Len(t, pending, 1)

	err = s.MarkNotificationDelivered(ctx, notif.ID)
	require.NoError(t, err)

	pending, err = s.ListPendingNotifications(ctx, "user1")
	require.NoError(t, err)
	assert.Len(t, pending, 0)
}

func TestNotificationStore_Cleanup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create an expired, delivered notification
	notif := &NotificationRecord{
		ID:         NewID(),
		UserID:     "user1",
		Message:    "Old notification",
		SourceType: "plan",
		CreatedAt:  time.Now().Add(-48 * time.Hour).Unix(),
		ExpiresAt:  time.Now().Add(-24 * time.Hour).Unix(),
	}
	require.NoError(t, s.CreateNotification(ctx, notif))
	require.NoError(t, s.MarkNotificationDelivered(ctx, notif.ID))

	deleted, err := s.CleanupNotifications(ctx, 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
}

func stringPtr(s string) *string {
	return &s
}
