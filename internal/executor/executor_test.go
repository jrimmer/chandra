package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/jrimmer/chandra/internal/planner"
)

func TestExecutor_Run_SimpleSteps(t *testing.T) {
	exec := NewExecutor(nil)

	plan := &planner.ExecutionPlan{
		ID:   "plan-1",
		Goal: "test plan",
		Steps: []planner.ExecutionStep{
			{ID: "s1", StepIndex: 0, Description: "step one", Action: "noop", Status: "pending"},
			{ID: "s2", StepIndex: 1, Description: "step two", Action: "noop", Status: "pending"},
		},
		Status: planner.PlanExecuting,
	}

	result, err := exec.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if result.StepsRun != 2 {
		t.Errorf("expected 2 steps run, got %d", result.StepsRun)
	}
}

func TestExecutor_Run_PausesAtCheckpoint(t *testing.T) {
	exec := NewExecutor(nil)

	plan := &planner.ExecutionPlan{
		ID: "plan-2",
		Steps: []planner.ExecutionStep{
			{ID: "s1", StepIndex: 0, Action: "noop", Status: "pending"},
			{ID: "s2", StepIndex: 1, Action: "noop", Status: "pending", Checkpoint: true},
			{ID: "s3", StepIndex: 2, Action: "noop", Status: "pending"},
		},
		Status: planner.PlanExecuting,
	}

	result, err := exec.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected pause, not success")
	}
	if result.StepsRun != 1 {
		t.Errorf("expected 1 step run before checkpoint, got %d", result.StepsRun)
	}
}

func TestExecContext_Values(t *testing.T) {
	if ExecFromBuiltinSkill != 0 {
		t.Errorf("expected ExecFromBuiltinSkill == 0, got %d", ExecFromBuiltinSkill)
	}
	if ExecFromApprovedSkill != 1 {
		t.Errorf("expected ExecFromApprovedSkill == 1, got %d", ExecFromApprovedSkill)
	}
	if ExecFromAgentReasoning != 2 {
		t.Errorf("expected ExecFromAgentReasoning == 2, got %d", ExecFromAgentReasoning)
	}
}

func TestCommandExecution_Values(t *testing.T) {
	if ExecDirect != 0 {
		t.Errorf("expected ExecDirect == 0, got %d", ExecDirect)
	}
	if ExecShellSafe != 1 {
		t.Errorf("expected ExecShellSafe == 1, got %d", ExecShellSafe)
	}
	if ExecShellFull != 2 {
		t.Errorf("expected ExecShellFull == 2, got %d", ExecShellFull)
	}
}

func TestExecContext_ContextPropagation(t *testing.T) {
	ctx := context.Background()

	// Default should be most restrictive
	if getExecContext(ctx) != ExecFromAgentReasoning {
		t.Errorf("expected ExecFromAgentReasoning from empty context, got %d", getExecContext(ctx))
	}

	// Set and retrieve
	ctx = withExecContext(ctx, ExecFromBuiltinSkill)
	if getExecContext(ctx) != ExecFromBuiltinSkill {
		t.Errorf("expected ExecFromBuiltinSkill, got %d", getExecContext(ctx))
	}
}

func TestMatchesDestructivePattern(t *testing.T) {
	tests := []struct {
		command     string
		destructive bool
	}{
		{"rm -rf /tmp/app", true},
		{"rm -rf /", true},
		{"DROP TABLE users", true},
		{"DELETE FROM sessions", true},
		{"mkfs.ext4 /dev/sda1", true},
		{"dd if=/dev/zero of=/dev/sda", true},
		{"ls /tmp", false},
		{"docker ps", false},
		{"gh pr list", false},
		{"cat /etc/hosts", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := matchesDestructivePattern(tt.command)
			if got != tt.destructive {
				t.Errorf("matchesDestructivePattern(%q) = %v, want %v", tt.command, got, tt.destructive)
			}
		})
	}
}

func TestIsAllowedBinary(t *testing.T) {
	tests := []struct {
		name    string
		allowed bool
	}{
		{"ls", true},
		{"cat", true},
		{"git", true},
		{"docker", true},
		{"kubectl", true},
		{"gh", true},
		{"curl", true},
		{"malware", false},
		{"unknown_binary", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllowedBinary(tt.name)
			if got != tt.allowed {
				t.Errorf("isAllowedBinary(%q) = %v, want %v", tt.name, got, tt.allowed)
			}
		})
	}
}

func TestExecutor_Resume_Approved(t *testing.T) {
	exec := NewExecutor(nil)

	plan := &planner.ExecutionPlan{
		ID: "plan-resume-1",
		Steps: []planner.ExecutionStep{
			{ID: "s1", StepIndex: 0, Action: "noop", Status: planner.StepCompleted},
			{ID: "s2", StepIndex: 1, Action: "checkpoint", Status: planner.StepAwaitingConfirmation, Checkpoint: true},
			{ID: "s3", StepIndex: 2, Action: "noop", Status: planner.StepPending},
		},
		Status:      planner.PlanPaused,
		CurrentStep: 1,
	}

	// Run first to register the plan in the store.
	exec.storePlan(plan)

	result, err := exec.Resume(context.Background(), "plan-resume-1", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success after approved resume")
	}
	if plan.Status != planner.PlanCompleted {
		t.Errorf("expected plan status %q, got %q", planner.PlanCompleted, plan.Status)
	}
}

func TestExecutor_Resume_Rejected(t *testing.T) {
	exec := NewExecutor(nil)

	plan := &planner.ExecutionPlan{
		ID: "plan-resume-2",
		Steps: []planner.ExecutionStep{
			{ID: "s1", StepIndex: 0, Action: "noop", Status: planner.StepCompleted},
			{ID: "s2", StepIndex: 1, Action: "checkpoint", Status: planner.StepAwaitingConfirmation, Checkpoint: true},
		},
		Status:      planner.PlanPaused,
		CurrentStep: 1,
	}

	exec.storePlan(plan)

	result, err := exec.Resume(context.Background(), "plan-resume-2", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for rejected resume")
	}
	if plan.Status != planner.PlanFailed {
		t.Errorf("expected plan status %q, got %q", planner.PlanFailed, plan.Status)
	}
}

func TestExecutor_Resume_NotFound(t *testing.T) {
	exec := NewExecutor(nil)

	_, err := exec.Resume(context.Background(), "nonexistent", true)
	if err == nil {
		t.Fatal("expected error for unknown plan ID")
	}
}

func TestExecutor_Status_Found(t *testing.T) {
	exec := NewExecutor(nil)

	plan := &planner.ExecutionPlan{
		ID: "plan-status-1",
		Steps: []planner.ExecutionStep{
			{ID: "s1", StepIndex: 0, Status: planner.StepCompleted},
			{ID: "s2", StepIndex: 1, Status: planner.StepAwaitingConfirmation, Checkpoint: true},
			{ID: "s3", StepIndex: 2, Status: planner.StepPending},
		},
		Status:         planner.PlanPaused,
		CurrentStep:    1,
		CheckpointStep: 1,
	}

	exec.storePlan(plan)

	status, err := exec.Status("plan-status-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.PlanID != "plan-status-1" {
		t.Errorf("expected plan ID %q, got %q", "plan-status-1", status.PlanID)
	}
	if status.State != planner.PlanPaused {
		t.Errorf("expected state %q, got %q", planner.PlanPaused, status.State)
	}
	if status.CurrentStep != 1 {
		t.Errorf("expected current step 1, got %d", status.CurrentStep)
	}
	if status.CheckpointStep != 1 {
		t.Errorf("expected checkpoint step 1, got %d", status.CheckpointStep)
	}
}

func TestExecutor_Status_NotFound(t *testing.T) {
	exec := NewExecutor(nil)

	_, err := exec.Status("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown plan ID")
	}
}

func TestErrRequiresConfirmation(t *testing.T) {
	err := ErrRequiresConfirmation
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !errors.Is(err, ErrRequiresConfirmation) {
		t.Error("expected ErrRequiresConfirmation")
	}
}
