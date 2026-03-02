package planner

import (
	"fmt"
	"strings"
)

// FormatPlanTree returns a tree-formatted string representation of an execution plan.
func FormatPlanTree(plan *ExecutionPlan) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Plan: %s\n", plan.ID)
	fmt.Fprintf(&b, "Goal: %s\n", plan.Goal)
	fmt.Fprintf(&b, "Status: %s\n", plan.Status)
	if plan.Error != "" {
		fmt.Fprintf(&b, "Error: %s\n", plan.Error)
	}

	if len(plan.Steps) == 0 {
		fmt.Fprintln(&b, "  (no steps)")
		return b.String()
	}

	fmt.Fprintf(&b, "Steps: %d\n", len(plan.Steps))
	fmt.Fprintln(&b, "")

	for i, step := range plan.Steps {
		prefix := "  "
		connector := "|--"
		if i == len(plan.Steps)-1 {
			connector = "`--"
		}

		// Current step indicator
		cursor := " "
		if i == plan.CurrentStep && (plan.Status == PlanExecuting || plan.Status == PlanPaused) {
			cursor = ">"
		}

		status := formatStepStatus(step.Status)

		fmt.Fprintf(&b, "%s%s%s [%d] %s [%s]", prefix, cursor, connector, step.StepIndex, step.Description, status)

		if step.SkillName != "" {
			fmt.Fprintf(&b, " (skill: %s)", step.SkillName)
		}

		if step.Checkpoint {
			fmt.Fprint(&b, " << CHECKPOINT")
		}

		fmt.Fprintln(&b)
	}

	return b.String()
}

// formatStepStatus returns a human-readable status string.
func formatStepStatus(status string) string {
	switch status {
	case StepCompleted:
		return "completed"
	case StepRunning:
		return "running"
	case StepPending:
		return "pending"
	case StepFailed:
		return "FAILED"
	case StepRolledBack:
		return "rolled back"
	case StepRollbackFailed:
		return "ROLLBACK FAILED"
	case StepAwaitingConfirmation:
		return "awaiting confirmation"
	case StepSkipped:
		return "skipped"
	default:
		return status
	}
}
