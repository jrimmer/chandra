package executor

import (
	"context"
	"testing"

	"github.com/jrimmer/chandra/internal/planner"
)

type mockConfirmationStore struct {
	written []PlanConfirmation
}

func (m *mockConfirmationStore) WriteConfirmation(_ context.Context, c PlanConfirmation) error {
	m.written = append(m.written, c)
	return nil
}

func TestExecutor_Checkpoint_WritesConfirmation(t *testing.T) {
	store := &mockConfirmationStore{}
	exec := NewExecutor(nil)
	exec.confirmations = store

	plan := &planner.ExecutionPlan{
		ID: "plan-cp",
		Steps: []planner.ExecutionStep{
			{ID: "s1", StepIndex: 0, Action: "noop", Status: "pending"},
			{ID: "s2", StepIndex: 1, Action: "deploy", Status: "pending", Checkpoint: true, Description: "confirm deploy"},
		},
		Status: planner.PlanExecuting,
	}

	_, _ = exec.Run(context.Background(), plan)

	if len(store.written) != 1 {
		t.Fatalf("expected 1 confirmation written, got %d", len(store.written))
	}
	if store.written[0].PlanID != "plan-cp" {
		t.Errorf("expected plan-cp, got %q", store.written[0].PlanID)
	}
	if store.written[0].StepIndex != 1 {
		t.Errorf("expected step index 1, got %d", store.written[0].StepIndex)
	}
}
