package planner

import "context"

// PlannerInterface defines goal decomposition.
type PlannerInterface interface {
	Decompose(ctx context.Context, goal string) (*ExecutionPlan, error)
	IdentifyGaps(ctx context.Context, plan *ExecutionPlan) ([]Gap, error)
	Replan(ctx context.Context, plan *ExecutionPlan, failedStep int) (*ExecutionPlan, error)
}

// Planner decomposes goals into execution plans.
type Planner struct {
	// LLM and skill registry injected at construction.
}

// NewPlanner creates a new Planner. LLM and skill registry are injected at construction;
// actual LLM integration is wired in a later phase.
func NewPlanner(llm any, skillRegistry any) *Planner {
	return &Planner{}
}

// Decompose is the primary entry point -- LLM-driven goal decomposition.
// Phase 3 provides the interface; actual LLM integration is wired later.
func (p *Planner) Decompose(ctx context.Context, goal string) (*ExecutionPlan, error) {
	return &ExecutionPlan{
		Goal:   goal,
		Status: PlanPlanning,
	}, nil
}

// IdentifyGaps inspects a plan for missing capabilities.
func (p *Planner) IdentifyGaps(ctx context.Context, plan *ExecutionPlan) ([]Gap, error) {
	return nil, nil
}

// Replan adjusts a plan after a step failure.
func (p *Planner) Replan(ctx context.Context, plan *ExecutionPlan, failedStep int) (*ExecutionPlan, error) {
	return plan, nil
}
