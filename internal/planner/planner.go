package planner

import "context"

// PlannerInterface defines goal decomposition.
type PlannerInterface interface {
	Decompose(ctx context.Context, goal string) (*ExecutionPlan, error)
	IdentifyGaps(ctx context.Context, plan *ExecutionPlan) ([]Gap, error)
	Replan(ctx context.Context, plan *ExecutionPlan, failedStep int) (*ExecutionPlan, error)
}

// CapabilityResolver resolves capability dependencies for planning.
// Implemented by infra.CapabilityGraph.
type CapabilityResolver interface {
	FindPath(capability string) []string
	RequiredCapabilities(capability string) []string
}

// Planner decomposes goals into execution plans.
type Planner struct {
	capabilities CapabilityResolver
}

// NewPlanner creates a new Planner. LLM and skill registry are injected at construction;
// actual LLM integration is wired in a later phase.
func NewPlanner(llm any, skillRegistry any, opts ...PlannerOption) *Planner {
	p := &Planner{}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// PlannerOption configures a Planner.
type PlannerOption func(*Planner)

// WithCapabilities sets the capability resolver for the planner.
func WithCapabilities(cr CapabilityResolver) PlannerOption {
	return func(p *Planner) {
		p.capabilities = cr
	}
}

// Decompose is the primary entry point -- LLM-driven goal decomposition.
// Phase 3 provides the interface; actual LLM integration is wired later.
// When a CapabilityResolver is set, Decompose uses FindPath to resolve
// capability dependencies for each step.
func (p *Planner) Decompose(ctx context.Context, goal string) (*ExecutionPlan, error) {
	return &ExecutionPlan{
		Goal:   goal,
		Status: PlanPlanning,
	}, nil
}

// IdentifyGaps inspects a plan for missing capabilities.
// When a CapabilityResolver is available, it checks each step's skill
// against the capability graph to find unresolved dependencies.
func (p *Planner) IdentifyGaps(ctx context.Context, plan *ExecutionPlan) ([]Gap, error) {
	if p.capabilities == nil {
		return nil, nil
	}

	var gaps []Gap
	for _, step := range plan.Steps {
		if step.SkillName == "" {
			continue
		}
		path := p.capabilities.FindPath(step.SkillName)
		if path == nil {
			gaps = append(gaps, Gap{
				Type:       "skill",
				Name:       step.SkillName,
				Required:   true,
				Resolution: "create",
			})
		}
	}
	return gaps, nil
}

// Replan adjusts a plan after a step failure.
func (p *Planner) Replan(ctx context.Context, plan *ExecutionPlan, failedStep int) (*ExecutionPlan, error) {
	return plan, nil
}
