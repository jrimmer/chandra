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

func TestErrRequiresConfirmation(t *testing.T) {
	err := ErrRequiresConfirmation
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !errors.Is(err, ErrRequiresConfirmation) {
		t.Error("expected ErrRequiresConfirmation")
	}
}
