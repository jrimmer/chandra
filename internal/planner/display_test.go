package planner

import (
	"strings"
	"testing"
	"time"
)

func TestFormatPlanTree_BasicPlan(t *testing.T) {
	plan := &ExecutionPlan{
		ID:   "plan-1",
		Goal: "deploy staging",
		Steps: []ExecutionStep{
			{StepIndex: 0, Description: "build image", Status: StepCompleted, SkillName: "docker"},
			{StepIndex: 1, Description: "push to registry", Status: StepRunning, SkillName: "docker"},
			{StepIndex: 2, Description: "deploy to k8s", Status: StepPending, SkillName: "kubectl", Checkpoint: true},
		},
		Status:      PlanExecuting,
		CurrentStep: 1,
		CreatedAt:   time.Now().Add(-10 * time.Minute),
	}

	output := FormatPlanTree(plan)
	if !strings.Contains(output, "deploy staging") {
		t.Errorf("expected goal in output, got:\n%s", output)
	}
	if !strings.Contains(output, "build image") {
		t.Errorf("expected step descriptions, got:\n%s", output)
	}
	// Should contain status indicators
	if !strings.Contains(output, "[completed]") && !strings.Contains(output, "completed") {
		t.Errorf("expected completed status indicator, got:\n%s", output)
	}
}

func TestFormatPlanTree_EmptyPlan(t *testing.T) {
	plan := &ExecutionPlan{
		ID:     "plan-empty",
		Goal:   "empty plan",
		Steps:  nil,
		Status: PlanPlanning,
	}

	output := FormatPlanTree(plan)
	if !strings.Contains(output, "empty plan") {
		t.Errorf("expected goal in output, got:\n%s", output)
	}
	if !strings.Contains(output, "(no steps)") {
		t.Errorf("expected '(no steps)' marker, got:\n%s", output)
	}
}

func TestFormatPlanTree_CheckpointMarker(t *testing.T) {
	plan := &ExecutionPlan{
		ID:   "plan-cp",
		Goal: "test checkpoints",
		Steps: []ExecutionStep{
			{StepIndex: 0, Description: "step 1", Status: StepPending},
			{StepIndex: 1, Description: "step 2", Status: StepPending, Checkpoint: true},
		},
		Status: PlanPlanning,
	}

	output := FormatPlanTree(plan)
	if !strings.Contains(output, "CHECKPOINT") {
		t.Errorf("expected CHECKPOINT marker, got:\n%s", output)
	}
}

func TestFormatStepStatus(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{StepCompleted, "completed"},
		{StepRunning, "running"},
		{StepPending, "pending"},
		{StepFailed, "FAILED"},
		{StepRolledBack, "rolled back"},
		{StepAwaitingConfirmation, "awaiting confirmation"},
	}
	for _, tt := range tests {
		got := formatStepStatus(tt.status)
		if got != tt.want {
			t.Errorf("formatStepStatus(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}
