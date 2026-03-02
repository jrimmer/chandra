package executor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/planner"
)

// TestIntegration_PlanExecutionFlow tests the full lifecycle of a plan:
// create -> execute steps -> checkpoint pause -> resume -> complete
func TestIntegration_PlanExecutionFlow(t *testing.T) {
	ctx := context.Background()

	// Set up executor with mock confirmation store
	store := &mockConfStore{}
	exec := &Executor{confirmations: store}

	// Build a plan with a checkpoint
	plan := &planner.ExecutionPlan{
		ID:          "integration-plan-1",
		Goal:        "deploy staging end-to-end",
		Status:      planner.PlanExecuting,
		CurrentStep: 0,
		Steps: []planner.ExecutionStep{
			{
				StepIndex:   0,
				Description: "build Docker image",
				SkillName:   "docker",
				Action:      "build",
				Status:      planner.StepPending,
				Idempotency: planner.IdempotentTrue,
			},
			{
				StepIndex:        1,
				Description:      "push to registry",
				SkillName:        "docker",
				Action:           "push",
				Status:           planner.StepPending,
				Checkpoint:       true,
				CheckpointReason: planner.CheckpointExternalAction,
			},
			{
				StepIndex:   2,
				Description: "deploy to k8s",
				SkillName:   "kubectl",
				Action:      "apply",
				Status:      planner.StepPending,
				Idempotency: planner.IdempotentTrue,
				Rollback: &planner.RollbackAction{
					Description: "rollback deployment",
					SkillName:   "kubectl",
					Action:      "rollback",
				},
			},
		},
		CreatedAt: time.Now(),
	}

	// Run: should execute step 0 and pause at step 1 (checkpoint)
	result, err := exec.Run(ctx, plan)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if plan.Status != planner.PlanPaused {
		t.Errorf("expected plan paused at checkpoint, got %q", plan.Status)
	}
	if result.StepsRun != 1 {
		t.Errorf("expected 1 step run before checkpoint, got %d", result.StepsRun)
	}

	// Verify confirmation was written
	if len(store.written) != 1 {
		t.Errorf("expected 1 confirmation written, got %d", len(store.written))
	}
	if store.written[0].PlanID != "integration-plan-1" {
		t.Errorf("expected plan ID in confirmation, got %q", store.written[0].PlanID)
	}
}

// TestIntegration_RollbackFlow tests creating a plan, failing, and rolling back.
func TestIntegration_RollbackFlow(t *testing.T) {
	ctx := context.Background()

	rolledBack := make(map[string]bool)
	exec := &Executor{
		rollbackFunc: func(ctx context.Context, action *planner.RollbackAction) error {
			rolledBack[action.Description] = true
			return nil
		},
	}

	plan := &planner.ExecutionPlan{
		ID:     "integration-rollback-1",
		Goal:   "test rollback",
		Status: planner.PlanExecuting,
		Steps: []planner.ExecutionStep{
			{
				StepIndex:   0,
				Description: "create resource",
				Status:      planner.StepCompleted,
				Rollback: &planner.RollbackAction{
					Description: "delete resource",
				},
			},
			{
				StepIndex:   1,
				Description: "configure resource",
				Status:      planner.StepCompleted,
				Rollback: &planner.RollbackAction{
					Description: "unconfigure resource",
				},
			},
			{
				StepIndex:   2,
				Description: "failed step",
				Status:      planner.StepFailed,
			},
		},
	}

	// Rollback steps 0 and 1 (both completed)
	err := exec.Rollback(ctx, plan, 1)
	if err != nil {
		t.Fatalf("Rollback error: %v", err)
	}

	if !rolledBack["unconfigure resource"] {
		t.Error("expected step 1 to be rolled back")
	}
	if !rolledBack["delete resource"] {
		t.Error("expected step 0 to be rolled back")
	}

	// Verify steps are marked rolled back
	if plan.Steps[0].Status != planner.StepRolledBack {
		t.Errorf("expected step 0 rolled_back, got %q", plan.Steps[0].Status)
	}
	if plan.Steps[1].Status != planner.StepRolledBack {
		t.Errorf("expected step 1 rolled_back, got %q", plan.Steps[1].Status)
	}
}

// TestIntegration_ConcurrencyGuardLimits tests that the concurrency guard prevents
// exceeding the max concurrent plans.
func TestIntegration_ConcurrencyGuardLimits(t *testing.T) {
	guard := NewPlanConcurrencyGuard(2)

	if !guard.Acquire("plan-a") {
		t.Fatal("first acquire should succeed")
	}
	if !guard.Acquire("plan-b") {
		t.Fatal("second acquire should succeed")
	}
	if guard.Acquire("plan-c") {
		t.Fatal("third acquire should be rejected (max=2)")
	}

	guard.Release("plan-a")
	if !guard.Acquire("plan-c") {
		t.Fatal("acquire after release should succeed")
	}
}

// TestIntegration_HeartbeatRecovery tests heartbeat-based step recovery.
func TestIntegration_HeartbeatRecovery(t *testing.T) {
	// Step with expired heartbeat should trigger rollback
	step := &planner.ExecutionStep{
		Status:           planner.StepRunning,
		HeartbeatTimeout: 10 * time.Minute,
	}
	lastHeartbeat := time.Now().Add(-15 * time.Minute)

	action := RecoverStep(step, lastHeartbeat)
	if action != RecoveryRollback {
		t.Errorf("expected RecoveryRollback for expired heartbeat, got %d", action)
	}

	// Step with recent heartbeat should keep monitoring
	recentHeartbeat := time.Now().Add(-2 * time.Minute)
	action = RecoverStep(step, recentHeartbeat)
	if action != RecoveryMonitor {
		t.Errorf("expected RecoveryMonitor for recent heartbeat, got %d", action)
	}
}

// TestIntegration_StateLimits tests that output size enforcement works.
func TestIntegration_StateLimits(t *testing.T) {
	small := json.RawMessage(`{"ok":true}`)
	out, err := EnforceStepOutputLimit(small, DefaultMaxStepOutputBytes)
	if err != nil {
		t.Fatalf("small output should pass: %v", err)
	}
	if string(out) != string(small) {
		t.Error("output should be unchanged")
	}

	// Large output should fail
	large := make(json.RawMessage, DefaultMaxStepOutputBytes+1)
	for i := range large {
		large[i] = 'x'
	}
	_, err = EnforceStepOutputLimit(large, DefaultMaxStepOutputBytes)
	if err == nil {
		t.Error("expected error for oversized output")
	}
}

// TestIntegration_AuditTrailSSH tests that SSH commands are properly audited.
func TestIntegration_AuditTrailSSH(t *testing.T) {
	trail := NewAuditTrail(100)

	user, host := ExtractSSHTarget("ssh deploy@prod uptime")
	trail.Record(AuditEntry{
		PlanID:    "plan-1",
		StepIndex: 0,
		Command:   "ssh deploy@prod uptime",
		Host:      host,
		User:      user,
		ExitCode:  0,
		StartedAt: time.Now(),
		Duration:  1 * time.Second,
	})

	entries := trail.QueryByHost("prod")
	if len(entries) != 1 {
		t.Errorf("expected 1 entry for host prod, got %d", len(entries))
	}
	if entries[0].User != "deploy" {
		t.Errorf("expected user deploy, got %q", entries[0].User)
	}
}

// mockConfStore is a mock ConfirmationStore for integration tests.
type mockConfStore struct {
	written []PlanConfirmation
}

func (m *mockConfStore) WriteConfirmation(ctx context.Context, c PlanConfirmation) error {
	m.written = append(m.written, c)
	return nil
}
