package executor

import (
	"sync"
	"time"

	"github.com/jrimmer/chandra/internal/planner"
)

// RecoveryAction describes what to do with an orphaned/stale step.
type RecoveryAction int

const (
	RecoveryNoAction RecoveryAction = iota // Step not running — nothing to do
	RecoveryRollback                       // Step is orphaned — rollback
	RecoveryMonitor                        // Step is still running — keep watching
)

// minHeartbeatTimeout is the minimum heartbeat timeout floor.
const minHeartbeatTimeout = 5 * time.Minute

// RecoverStep determines the recovery action for a step based on its heartbeat.
func RecoverStep(step *planner.ExecutionStep, lastHeartbeat time.Time) RecoveryAction {
	if step.Status != planner.StepRunning {
		return RecoveryNoAction
	}

	timeout := step.HeartbeatTimeout
	if timeout == 0 {
		// Default: 2x expected duration
		timeout = step.ExpectedDuration * 2
	}
	// Enforce 5-minute minimum floor
	if timeout < minHeartbeatTimeout {
		timeout = minHeartbeatTimeout
	}

	since := time.Since(lastHeartbeat)
	if since > timeout {
		return RecoveryRollback
	}
	return RecoveryMonitor
}

// StepStore is the interface for persisting heartbeat updates.
type StepStore interface {
	BatchUpdateHeartbeats(updates map[string]time.Time) error
}

// HeartbeatBatcher collects heartbeat updates and flushes them to the store
// in batches to reduce write pressure.
type HeartbeatBatcher struct {
	pending map[string]time.Time
	mu      sync.Mutex
	store   StepStore
}

// NewHeartbeatBatcher creates a new batcher backed by the given store.
func NewHeartbeatBatcher(store StepStore) *HeartbeatBatcher {
	return &HeartbeatBatcher{
		pending: make(map[string]time.Time),
		store:   store,
	}
}

// Record adds a heartbeat for the given step key (format: "planID:stepIndex").
func (b *HeartbeatBatcher) Record(stepKey string) {
	b.mu.Lock()
	b.pending[stepKey] = time.Now()
	b.mu.Unlock()
}

// Start begins the periodic flush loop. Call Stop to terminate.
func (b *HeartbeatBatcher) Start(interval time.Duration) func() {
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				b.flush()
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}

// flush writes all pending heartbeats to the store.
func (b *HeartbeatBatcher) flush() {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return
	}
	updates := b.pending
	b.pending = make(map[string]time.Time)
	b.mu.Unlock()

	_ = b.store.BatchUpdateHeartbeats(updates)
}

// identifyEphemeralDeps returns step indices that need re-execution because
// they depend on ephemeral data that was lost (e.g., after daemon restart).
func identifyEphemeralDeps(steps []planner.ExecutionStep) []int {
	var rerun []int
	for _, s := range steps {
		if s.DependsOnEphemeral && s.Status == planner.StepCompleted {
			rerun = append(rerun, s.StepIndex)
		}
	}
	return rerun
}
