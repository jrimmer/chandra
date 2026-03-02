package executor

import "sync"

// PlanConcurrencyGuard limits the number of concurrently executing plans.
type PlanConcurrencyGuard struct {
	mu     sync.Mutex
	active map[string]struct{}
	max    int
}

// NewPlanConcurrencyGuard creates a guard with the given max concurrent plans.
// A max of 0 means unlimited.
func NewPlanConcurrencyGuard(max int) *PlanConcurrencyGuard {
	return &PlanConcurrencyGuard{
		active: make(map[string]struct{}),
		max:    max,
	}
}

// Acquire tries to register a plan as active. Returns false if the max is reached.
// Acquiring the same planID again is idempotent.
func (g *PlanConcurrencyGuard) Acquire(planID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Already active — idempotent re-acquire
	if _, ok := g.active[planID]; ok {
		return true
	}

	if g.max > 0 && len(g.active) >= g.max {
		return false
	}

	g.active[planID] = struct{}{}
	return true
}

// Release removes a plan from the active set.
func (g *PlanConcurrencyGuard) Release(planID string) {
	g.mu.Lock()
	delete(g.active, planID)
	g.mu.Unlock()
}

// ActivePlans returns the IDs of all currently active plans.
func (g *PlanConcurrencyGuard) ActivePlans() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	ids := make([]string, 0, len(g.active))
	for id := range g.active {
		ids = append(ids, id)
	}
	return ids
}
