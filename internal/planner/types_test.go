package planner

import (
	"testing"
	"time"
)

func TestExecutionPlan_IsCheckpoint(t *testing.T) {
	plan := ExecutionPlan{
		ID:   "plan-1",
		Goal: "deploy nginx",
		Steps: []ExecutionStep{
			{ID: "s1", Description: "pull image", Checkpoint: false},
			{ID: "s2", Description: "confirm deploy", Checkpoint: true},
			{ID: "s3", Description: "run container", Checkpoint: false},
		},
	}
	if plan.Steps[0].Checkpoint {
		t.Error("step 0 should not be checkpoint")
	}
	if !plan.Steps[1].Checkpoint {
		t.Error("step 1 should be checkpoint")
	}
}

func TestExecutionStep_HasRollback(t *testing.T) {
	step := ExecutionStep{
		ID: "s1",
		Rollback: &RollbackAction{
			Description: "remove container",
			Action:      "docker rm -f nginx",
		},
	}
	if step.Rollback == nil {
		t.Error("expected rollback action")
	}
}

func TestExecutionStatus_Values(t *testing.T) {
	statuses := []string{
		PlanPlanning, PlanExecuting, PlanPaused,
		PlanCompleted, PlanFailed, PlanRolledBack,
		PlanTimeout, PlanTimeoutPartial,
		PlanRolledBackPartial, PlanFailedAwaitingDecision,
	}
	for _, s := range statuses {
		if s == "" {
			t.Error("status constant should not be empty")
		}
	}
}

func TestStepIdempotency_Values(t *testing.T) {
	if IdempotentTrue != 0 {
		t.Errorf("expected IdempotentTrue == 0, got %d", IdempotentTrue)
	}
	if IdempotentFalse != 1 {
		t.Errorf("expected IdempotentFalse == 1, got %d", IdempotentFalse)
	}
	if IdempotentUnknown != 2 {
		t.Errorf("expected IdempotentUnknown == 2, got %d", IdempotentUnknown)
	}
}

func TestOutputMode_Values(t *testing.T) {
	if OutputAuto != 0 {
		t.Errorf("expected OutputAuto == 0, got %d", OutputAuto)
	}
	if OutputPersistent != 1 {
		t.Errorf("expected OutputPersistent == 1, got %d", OutputPersistent)
	}
	if OutputEphemeral != 2 {
		t.Errorf("expected OutputEphemeral == 2, got %d", OutputEphemeral)
	}
}

func TestCheckpointReason_Constants(t *testing.T) {
	reasons := []string{
		CheckpointInstallSoftware, CheckpointCreateInfra,
		CheckpointModifyData, CheckpointExternalAction,
		CheckpointDestructive, CheckpointCostImplication,
	}
	for _, r := range reasons {
		if r == "" {
			t.Error("checkpoint reason should not be empty")
		}
	}
}

func TestStepOutput_PersistentEphemeral(t *testing.T) {
	out := StepOutput{
		Persistent: map[string]any{"image_id": "sha256:abc"},
		Ephemeral:  map[string]any{"token": "secret123"},
	}
	if out.Persistent["image_id"] != "sha256:abc" {
		t.Error("expected persistent image_id")
	}
	if out.Ephemeral["token"] != "secret123" {
		t.Error("expected ephemeral token")
	}
}

func TestResourceRef(t *testing.T) {
	ref := ResourceRef{Type: "container", ID: "nginx-1", Host: "node-1"}
	if ref.Type != "container" {
		t.Error("expected container type")
	}
}

func TestExecutionStep_AllFields(t *testing.T) {
	step := ExecutionStep{
		ID:                "s1",
		PlanID:            "plan-1",
		StepIndex:         0,
		Description:       "deploy",
		SkillName:         "docker",
		Action:            "docker run nginx",
		DependsOn:         []string{"s0"},
		Creates:           []string{"container:nginx"},
		Checkpoint:        true,
		CheckpointReason:  CheckpointCreateInfra,
		CheckpointTimeout: 24 * time.Hour,
		ExpectedDuration:  5 * time.Minute,
		HeartbeatTimeout:  10 * time.Minute,
		Idempotency:       IdempotentTrue,
		OutputMode:        OutputPersistent,
		DependsOnEphemeral: true,
		Heartbeat:         time.Now(),
		Status:            StepPending,
	}
	if step.CheckpointReason != CheckpointCreateInfra {
		t.Errorf("expected checkpoint reason %q, got %q", CheckpointCreateInfra, step.CheckpointReason)
	}
	if !step.DependsOnEphemeral {
		t.Error("expected DependsOnEphemeral to be true")
	}
}

func TestCheckpointConfig(t *testing.T) {
	cfg := CheckpointConfig{
		DefaultTimeout: 24 * time.Hour,
		MaxTimeout:     7 * 24 * time.Hour,
		Extendable:     true,
	}
	if cfg.DefaultTimeout != 24*time.Hour {
		t.Error("expected 24h default timeout")
	}
	if !cfg.Extendable {
		t.Error("expected extendable")
	}
}

func TestExecutionPlan_Checkpoints(t *testing.T) {
	plan := ExecutionPlan{
		ID: "plan-1",
		Steps: []ExecutionStep{
			{ID: "s0", StepIndex: 0, Checkpoint: false},
			{ID: "s1", StepIndex: 1, Checkpoint: true},
			{ID: "s2", StepIndex: 2, Checkpoint: false},
			{ID: "s3", StepIndex: 3, Checkpoint: true},
			{ID: "s4", StepIndex: 4, Checkpoint: false},
		},
		Checkpoints: []int{1, 3},
	}
	if len(plan.Checkpoints) != 2 {
		t.Fatalf("expected 2 checkpoints, got %d", len(plan.Checkpoints))
	}
	if plan.Checkpoints[0] != 1 || plan.Checkpoints[1] != 3 {
		t.Errorf("expected [1,3], got %v", plan.Checkpoints)
	}
}
