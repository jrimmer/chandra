package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/jrimmer/chandra/internal/planner"
)

func TestRollback_ReverseOrder(t *testing.T) {
	var rollbackOrder []string
	exec := NewExecutor(nil)
	exec.rollbackFunc = func(ctx context.Context, action *planner.RollbackAction) error {
		rollbackOrder = append(rollbackOrder, action.Description)
		return nil
	}

	plan := &planner.ExecutionPlan{
		Steps: []planner.ExecutionStep{
			{ID: "s1", StepIndex: 0, Status: "completed", Rollback: &planner.RollbackAction{Description: "undo-1"}},
			{ID: "s2", StepIndex: 1, Status: "completed", Rollback: &planner.RollbackAction{Description: "undo-2"}},
			{ID: "s3", StepIndex: 2, Status: "completed", Rollback: &planner.RollbackAction{Description: "undo-3"}},
		},
	}

	err := exec.Rollback(context.Background(), plan, 2)
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	expected := []string{"undo-3", "undo-2", "undo-1"}
	if len(rollbackOrder) != len(expected) {
		t.Fatalf("expected %d rollbacks, got %d", len(expected), len(rollbackOrder))
	}
	for i, desc := range rollbackOrder {
		if desc != expected[i] {
			t.Errorf("step %d: expected %q, got %q", i, expected[i], desc)
		}
	}
}

func TestRollback_SkipsNilRollback(t *testing.T) {
	exec := NewExecutor(nil)
	plan := &planner.ExecutionPlan{
		Steps: []planner.ExecutionStep{
			{ID: "s1", StepIndex: 0, Status: "completed", Rollback: nil},
		},
	}

	err := exec.Rollback(context.Background(), plan, 0)
	if err != nil {
		t.Fatalf("rollback should succeed even with nil rollback: %v", err)
	}
}

func TestRollback_PartialFailure(t *testing.T) {
	exec := NewExecutor(nil)
	exec.rollbackFunc = func(ctx context.Context, action *planner.RollbackAction) error {
		if action.Description == "undo-2" {
			return errors.New("rollback step 2 failed")
		}
		return nil
	}

	plan := &planner.ExecutionPlan{
		Steps: []planner.ExecutionStep{
			{ID: "s1", StepIndex: 0, Status: "completed", Rollback: &planner.RollbackAction{Description: "undo-1"}},
			{ID: "s2", StepIndex: 1, Status: "completed", Rollback: &planner.RollbackAction{Description: "undo-2"}},
			{ID: "s3", StepIndex: 2, Status: "completed", Rollback: &planner.RollbackAction{Description: "undo-3"}},
		},
	}

	err := exec.RollbackWithPartial(context.Background(), plan, 2)
	if err != nil {
		t.Fatalf("RollbackWithPartial should not return error: %v", err)
	}

	if plan.Status != planner.PlanRolledBackPartial {
		t.Errorf("expected status %q, got %q", planner.PlanRolledBackPartial, plan.Status)
	}

	// Step 3 rolled back, step 2 failed, step 1 rolled back
	if plan.Steps[2].Status != planner.StepRolledBack {
		t.Errorf("step 3: expected rolled_back, got %q", plan.Steps[2].Status)
	}
	if plan.Steps[1].Status != planner.StepRollbackFailed {
		t.Errorf("step 2: expected rollback_failed, got %q", plan.Steps[1].Status)
	}
	if plan.Steps[0].Status != planner.StepRolledBack {
		t.Errorf("step 1: expected rolled_back, got %q", plan.Steps[0].Status)
	}
}

func TestRollback_IdempotencyAware(t *testing.T) {
	exec := NewExecutor(nil)
	exec.rollbackFunc = func(ctx context.Context, action *planner.RollbackAction) error {
		return nil
	}

	plan := &planner.ExecutionPlan{
		Steps: []planner.ExecutionStep{
			{ID: "s1", StepIndex: 0, Status: "completed", Idempotency: planner.IdempotentTrue,
				Rollback: &planner.RollbackAction{Description: "undo-1"}},
			{ID: "s2", StepIndex: 1, Status: "completed", Idempotency: planner.IdempotentTrue,
				Rollback: &planner.RollbackAction{Description: "undo-2"}},
			{ID: "s3", StepIndex: 2, Status: "completed", Idempotency: planner.IdempotentFalse,
				Rollback: &planner.RollbackAction{Description: "undo-3"}},
		},
	}

	// Mixed idempotency: should rollback only non-idempotent steps
	decision := classifyRollback(plan.Steps[:3])
	if decision != RollbackMixed {
		t.Errorf("expected RollbackMixed, got %v", decision)
	}
}

func TestRollback_AllIdempotent(t *testing.T) {
	steps := []planner.ExecutionStep{
		{ID: "s1", Idempotency: planner.IdempotentTrue},
		{ID: "s2", Idempotency: planner.IdempotentTrue},
	}

	decision := classifyRollback(steps)
	if decision != RollbackAllIdempotent {
		t.Errorf("expected RollbackAllIdempotent, got %v", decision)
	}
}

func TestRollback_AllNonIdempotent(t *testing.T) {
	steps := []planner.ExecutionStep{
		{ID: "s1", Idempotency: planner.IdempotentFalse},
		{ID: "s2", Idempotency: planner.IdempotentFalse},
	}

	decision := classifyRollback(steps)
	if decision != RollbackAllNonIdempotent {
		t.Errorf("expected RollbackAllNonIdempotent, got %v", decision)
	}
}

func TestGenerateCleanupInstructions(t *testing.T) {
	steps := []planner.ExecutionStep{
		{ID: "s1", StepIndex: 0, Description: "create file /tmp/deploy.tar", Status: planner.StepRollbackFailed,
			Rollback: &planner.RollbackAction{Description: "remove file /tmp/deploy.tar"}},
		{ID: "s2", StepIndex: 1, Description: "run container", Status: planner.StepRolledBack},
	}

	instructions := generateCleanupInstructions(steps)
	if instructions == "" {
		t.Error("expected non-empty cleanup instructions")
	}
}
