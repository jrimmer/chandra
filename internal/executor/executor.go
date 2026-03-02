package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jrimmer/chandra/internal/planner"
	"github.com/jrimmer/chandra/internal/skills"
)

// ErrRequiresConfirmation is returned when a command requires user confirmation.
var ErrRequiresConfirmation = errors.New("executor: command requires confirmation")

// ExecContext identifies who is requesting command execution.
type ExecContext int

const (
	ExecFromBuiltinSkill   ExecContext = iota // Builtin skill — most trusted
	ExecFromApprovedSkill                     // Approved user skill — medium trust
	ExecFromAgentReasoning                    // Agent reasoning — least trusted
)

// CommandExecution determines how a command is invoked.
type CommandExecution int

const (
	ExecDirect    CommandExecution = iota // exec.Command("binary", args...)
	ExecShellSafe                        // exec.Command("sh", "-c", sanitized)
	ExecShellFull                        // exec.Command("sh", "-c", raw) — Tier 4 only
)

// Context key for ExecContext propagation.
type execContextKey struct{}

func withExecContext(ctx context.Context, ec ExecContext) context.Context {
	return context.WithValue(ctx, execContextKey{}, ec)
}

func getExecContext(ctx context.Context) ExecContext {
	if ec, ok := ctx.Value(execContextKey{}).(ExecContext); ok {
		return ec
	}
	return ExecFromAgentReasoning // Default to most restrictive
}

// ExecutorInterface defines plan execution operations.
type ExecutorInterface interface {
	Run(ctx context.Context, plan *planner.ExecutionPlan) (*planner.ExecutionResult, error)
	Resume(ctx context.Context, planID string, approved bool) (*planner.ExecutionResult, error)
	Rollback(ctx context.Context, plan *planner.ExecutionPlan, upToStep int) error
	Status(planID string) (*planner.ExecutionStatus, error)
}

// Executor runs execution plans step by step.
type Executor struct {
	confirmations ConfirmationStore
	rollbackFunc  func(ctx context.Context, action *planner.RollbackAction) error
	mu            sync.RWMutex
	plans         map[string]*planner.ExecutionPlan
}

// ConfirmationStore is the interface for writing checkpoint confirmations.
type ConfirmationStore interface {
	WriteConfirmation(ctx context.Context, c PlanConfirmation) error
}

// PlanConfirmation is a confirmation row linked to a plan checkpoint.
type PlanConfirmation struct {
	PlanID      string
	StepIndex   int
	Description string
}

// NewExecutor creates a new Executor.
func NewExecutor(actionLog any) *Executor {
	return &Executor{
		plans: make(map[string]*planner.ExecutionPlan),
	}
}

// storePlan registers a plan in the executor's in-memory store.
func (e *Executor) storePlan(plan *planner.ExecutionPlan) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.plans == nil {
		e.plans = make(map[string]*planner.ExecutionPlan)
	}
	e.plans[plan.ID] = plan
}

// getPlan retrieves a plan from the in-memory store.
func (e *Executor) getPlan(planID string) (*planner.ExecutionPlan, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	plan, ok := e.plans[planID]
	if !ok {
		return nil, fmt.Errorf("executor: plan %q not found", planID)
	}
	return plan, nil
}

// Run executes a plan's steps sequentially, pausing at checkpoints.
func (e *Executor) Run(ctx context.Context, plan *planner.ExecutionPlan) (*planner.ExecutionResult, error) {
	e.storePlan(plan)
	return e.runFrom(ctx, plan, 0)
}

// runFrom executes plan steps starting from the given index.
func (e *Executor) runFrom(ctx context.Context, plan *planner.ExecutionPlan, startStep int) (*planner.ExecutionResult, error) {
	result := &planner.ExecutionResult{
		PlanID:  plan.ID,
		Outputs: make(map[string]any),
	}

	for i := startStep; i < len(plan.Steps); i++ {
		step := plan.Steps[i]
		if step.Checkpoint {
			// Write confirmation row if store is available.
			if e.confirmations != nil {
				_ = e.confirmations.WriteConfirmation(ctx, PlanConfirmation{
					PlanID:      plan.ID,
					StepIndex:   step.StepIndex,
					Description: step.Description,
				})
			}
			// Pause at checkpoint.
			plan.Status = planner.PlanPaused
			plan.CurrentStep = i
			plan.CheckpointStep = i
			result.StepsRun = i
			result.FailedAt = -1
			return result, nil
		}

		// Execute step.
		err := e.executeStep(ctx, plan, &step)
		if err != nil {
			result.FailedAt = i
			result.Error = err
			return result, nil
		}

		plan.Steps[i].Status = planner.StepCompleted
		result.StepsRun = i + 1
	}

	plan.Status = planner.PlanCompleted
	result.Success = true
	return result, nil
}

// executeStep dispatches a single step. Real implementation will dispatch to tool registry.
func (e *Executor) executeStep(ctx context.Context, plan *planner.ExecutionPlan, step *planner.ExecutionStep) error {
	return nil
}

// Resume continues a paused plan from its checkpoint.
func (e *Executor) Resume(ctx context.Context, planID string, approved bool) (*planner.ExecutionResult, error) {
	plan, err := e.getPlan(planID)
	if err != nil {
		return nil, err
	}

	if !approved {
		plan.Status = planner.PlanFailed
		plan.Error = "checkpoint rejected by user"
		return &planner.ExecutionResult{
			PlanID:  planID,
			Outputs: make(map[string]any),
		}, nil
	}

	// Mark checkpoint step as completed and continue from the next step.
	checkpointIdx := plan.CurrentStep
	plan.Steps[checkpointIdx].Status = planner.StepCompleted
	plan.Status = planner.PlanExecuting

	return e.runFrom(ctx, plan, checkpointIdx+1)
}

// Rollback reverses completed steps in reverse order.
func (e *Executor) Rollback(ctx context.Context, plan *planner.ExecutionPlan, upToStep int) error {
	for i := upToStep; i >= 0; i-- {
		step := plan.Steps[i]
		if step.Rollback == nil {
			continue
		}
		if e.rollbackFunc != nil {
			if err := e.rollbackFunc(ctx, step.Rollback); err != nil {
				return err
			}
		}
		plan.Steps[i].Status = planner.StepRolledBack
	}
	return nil
}

// Status returns the current execution state of a plan.
func (e *Executor) Status(planID string) (*planner.ExecutionStatus, error) {
	plan, err := e.getPlan(planID)
	if err != nil {
		return nil, err
	}

	return &planner.ExecutionStatus{
		PlanID:         plan.ID,
		State:          plan.Status,
		CurrentStep:    plan.CurrentStep,
		CheckpointStep: plan.CheckpointStep,
		Outputs:        plan.State,
	}, nil
}

// approvedTemplate wraps an approved command template string for matching.
type approvedTemplate struct {
	template string
}

// executeCommand selects execution mode based on trust context.
// templates is an optional list of approved command templates for skill-level trust.
func executeCommand(ctx context.Context, command string, templates []approvedTemplate) (CommandExecution, error) {
	ec := getExecContext(ctx)
	switch ec {
	case ExecFromBuiltinSkill:
		if matchesDestructivePattern(command) {
			return ExecDirect, ErrRequiresConfirmation
		}
		return ExecDirect, nil
	case ExecFromApprovedSkill:
		parts := strings.Fields(command)
		if len(parts) > 0 && isAllowedBinary(parts[0]) {
			return ExecShellSafe, nil
		}
		// Check approved command templates.
		skillTemplates := make([]skills.ApprovedTemplate, len(templates))
		for i, t := range templates {
			skillTemplates[i] = skills.ApprovedTemplate{Template: t.template}
		}
		if skills.HasApprovedTemplate(skillTemplates, command) {
			return ExecShellSafe, nil
		}
		return ExecShellSafe, ErrRequiresConfirmation
	case ExecFromAgentReasoning:
		return ExecShellFull, ErrRequiresConfirmation
	default:
		return ExecShellFull, ErrRequiresConfirmation
	}
}

// matchesDestructivePattern checks if a command matches known destructive patterns.
func matchesDestructivePattern(command string) bool {
	upper := strings.ToUpper(command)
	patterns := []string{
		"RM -RF",
		"DROP TABLE",
		"DELETE FROM",
		"FORMAT",
		"MKFS",
		"DD IF=",
		"> /DEV/",
	}
	for _, p := range patterns {
		if strings.Contains(upper, p) {
			return true
		}
	}
	return false
}

// isAllowedBinary checks a binary name against the default allowlist.
var defaultAllowlist = map[string]bool{
	"ls": true, "cat": true, "echo": true, "grep": true, "find": true,
	"head": true, "tail": true, "wc": true, "sort": true, "uniq": true,
	"cut": true, "tr": true, "sed": true, "awk": true, "jq": true,
	"git": true, "gh": true,
	"docker": true, "docker-compose": true, "podman": true,
	"kubectl": true, "helm": true,
	"curl": true, "wget": true,
	"ssh": true, "scp": true, "rsync": true,
	"systemctl": true, "journalctl": true,
	"apt": true, "apt-get": true, "dnf": true, "yum": true, "brew": true,
	"pip": true, "npm": true, "yarn": true, "go": true, "cargo": true,
	"make": true, "cmake": true,
	"terraform": true, "ansible": true,
	"python": true, "python3": true, "node": true, "ruby": true,
	"mkdir": true, "cp": true, "mv": true, "touch": true, "chmod": true,
	"chown": true, "ln": true, "stat": true, "file": true, "du": true,
	"df": true, "free": true, "top": true, "ps": true, "whoami": true,
	"hostname": true, "uname": true, "date": true, "uptime": true,
}

func isAllowedBinary(name string) bool {
	return defaultAllowlist[name]
}

// ListPlans returns all plans, optionally filtered by status.
func (e *Executor) ListPlans(status string) []*planner.ExecutionPlan {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var result []*planner.ExecutionPlan
	for _, p := range e.plans {
		if status == "" || p.Status == status {
			result = append(result, p)
		}
	}
	return result
}

// ExtendCheckpoint extends a paused plan's checkpoint timeout.
func (e *Executor) ExtendCheckpoint(planID string, duration string) error {
	plan, err := e.getPlan(planID)
	if err != nil {
		return err
	}
	if plan.Status != planner.PlanPaused {
		return fmt.Errorf("plan %s is not paused (status: %s)", planID, plan.Status)
	}
	// Duration is accepted but checkpoint timeout extension is a no-op in the
	// in-memory implementation — it prevents the plan from expiring when a
	// persistent store with TTL is wired.
	_ = duration
	return nil
}

// Cancel marks a plan as cancelled.
func (e *Executor) Cancel(planID string) error {
	plan, err := e.getPlan(planID)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	plan.Status = planner.PlanFailed
	return nil
}
