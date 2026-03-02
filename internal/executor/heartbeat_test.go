package executor

import (
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/planner"
)

func TestRecoverStep_Orphaned(t *testing.T) {
	step := &planner.ExecutionStep{
		Status:           planner.StepRunning,
		ExpectedDuration: 30 * time.Second,
	}
	heartbeat := time.Now().Add(-10 * time.Minute)

	action := RecoverStep(step, heartbeat)
	if action != RecoveryRollback {
		t.Errorf("expected rollback, got %v", action)
	}
}

func TestRecoverStep_StillRunning(t *testing.T) {
	step := &planner.ExecutionStep{
		Status:           planner.StepRunning,
		ExpectedDuration: 10 * time.Minute,
	}
	heartbeat := time.Now().Add(-30 * time.Second)

	action := RecoverStep(step, heartbeat)
	if action != RecoveryMonitor {
		t.Errorf("expected monitor, got %v", action)
	}
}

func TestRecoverStep_NotRunning(t *testing.T) {
	step := &planner.ExecutionStep{
		Status: planner.StepCompleted,
	}
	heartbeat := time.Now()

	action := RecoverStep(step, heartbeat)
	if action != RecoveryNoAction {
		t.Errorf("expected no action, got %v", action)
	}
}

func TestRecoverStep_PendingNoAction(t *testing.T) {
	step := &planner.ExecutionStep{
		Status: planner.StepPending,
	}

	action := RecoverStep(step, time.Now())
	if action != RecoveryNoAction {
		t.Errorf("expected no action for pending step, got %v", action)
	}
}

func TestRecoverStep_MinTimeout5Min(t *testing.T) {
	step := &planner.ExecutionStep{
		Status:           planner.StepRunning,
		ExpectedDuration: 1 * time.Minute, // very short
	}
	// Heartbeat 3 min ago: would be stale if timeout = 2min, but 5min floor saves it
	heartbeat := time.Now().Add(-3 * time.Minute)

	action := RecoverStep(step, heartbeat)
	if action != RecoveryMonitor {
		t.Errorf("expected monitor (5min floor should keep step alive), got %v", action)
	}
}

func TestHeartbeatBatcher_RecordAndFlush(t *testing.T) {
	store := &mockStepStore{heartbeats: make(map[string]time.Time)}
	batcher := NewHeartbeatBatcher(store)

	batcher.Record("plan-1:0")
	batcher.Record("plan-1:1")

	if len(batcher.pending) != 2 {
		t.Errorf("expected 2 pending, got %d", len(batcher.pending))
	}

	batcher.flush()

	if len(store.heartbeats) != 2 {
		t.Errorf("expected 2 flushed to store, got %d", len(store.heartbeats))
	}
	if len(batcher.pending) != 0 {
		t.Errorf("expected 0 pending after flush, got %d", len(batcher.pending))
	}
}

func TestIdentifyEphemeralDeps(t *testing.T) {
	steps := []planner.ExecutionStep{
		{ID: "s0", StepIndex: 0, Status: planner.StepCompleted, DependsOnEphemeral: false},
		{ID: "s1", StepIndex: 1, Status: planner.StepCompleted, DependsOnEphemeral: true},
		{ID: "s2", StepIndex: 2, Status: planner.StepRunning, DependsOnEphemeral: false},
	}

	rerun := identifyEphemeralDeps(steps)
	if len(rerun) != 1 {
		t.Fatalf("expected 1 step to re-run, got %d", len(rerun))
	}
	if rerun[0] != 1 {
		t.Errorf("expected step index 1, got %d", rerun[0])
	}
}

type mockStepStore struct {
	heartbeats map[string]time.Time
}

func (m *mockStepStore) BatchUpdateHeartbeats(updates map[string]time.Time) error {
	for k, v := range updates {
		m.heartbeats[k] = v
	}
	return nil
}
