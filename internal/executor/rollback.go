package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/jrimmer/chandra/internal/planner"
)

// RollbackDecision classifies how rollback should proceed based on step idempotency.
type RollbackDecision int

const (
	RollbackAllIdempotent    RollbackDecision = iota // All steps idempotent — safe to leave
	RollbackAllNonIdempotent                         // All non-idempotent — full rollback
	RollbackMixed                                    // Mixed — rollback non-idempotent only
)

// classifyRollback examines completed steps and returns the rollback decision.
func classifyRollback(steps []planner.ExecutionStep) RollbackDecision {
	hasIdempotent := false
	hasNonIdempotent := false

	for _, s := range steps {
		switch s.Idempotency {
		case planner.IdempotentTrue:
			hasIdempotent = true
		case planner.IdempotentFalse, planner.IdempotentUnknown:
			hasNonIdempotent = true
		}
	}

	switch {
	case hasIdempotent && !hasNonIdempotent:
		return RollbackAllIdempotent
	case !hasIdempotent && hasNonIdempotent:
		return RollbackAllNonIdempotent
	default:
		return RollbackMixed
	}
}

// RollbackWithPartial performs rollback with partial failure handling.
// If a rollback step fails, the plan is marked rolled_back_partial and
// cleanup instructions are generated.
func (e *Executor) RollbackWithPartial(ctx context.Context, plan *planner.ExecutionPlan, upToStep int) error {
	anyFailed := false

	for i := upToStep; i >= 0; i-- {
		step := &plan.Steps[i]
		if step.Rollback == nil {
			continue
		}

		var err error
		if e.rollbackFunc != nil {
			err = e.rollbackFunc(ctx, step.Rollback)
		}

		if err != nil {
			step.Status = planner.StepRollbackFailed
			anyFailed = true
			// Continue rolling back remaining steps
		} else {
			step.Status = planner.StepRolledBack
		}
	}

	if anyFailed {
		plan.Status = planner.PlanRolledBackPartial
	} else {
		plan.Status = planner.PlanRolledBack
	}

	return nil
}

// generateCleanupInstructions creates human-readable instructions for steps
// that failed to rollback.
func generateCleanupInstructions(steps []planner.ExecutionStep) string {
	var lines []string
	for _, s := range steps {
		if s.Status != planner.StepRollbackFailed {
			continue
		}
		desc := "unknown action"
		if s.Rollback != nil {
			desc = s.Rollback.Description
		}
		lines = append(lines, fmt.Sprintf("Step %d (%s): %s manually", s.StepIndex, s.Description, desc))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Manual cleanup required:\n" + strings.Join(lines, "\n")
}
