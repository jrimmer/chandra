package infra

import "sync"

// CapabilityGraph is a DAG of capabilities with dependency resolution.
type CapabilityGraph struct {
	mu    sync.RWMutex
	nodes map[string][]string // capability -> dependencies
}

// NewCapabilityGraph creates a new capability graph.
func NewCapabilityGraph() *CapabilityGraph {
	return &CapabilityGraph{
		nodes: make(map[string][]string),
	}
}

// AddCapability registers a capability with its dependencies.
func (g *CapabilityGraph) AddCapability(name string, dependencies []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes[name] = dependencies
}

// FindPath returns the full dependency path from a capability to its root(s).
// Returns nil if the capability is not found.
func (g *CapabilityGraph) FindPath(capability string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	deps, ok := g.nodes[capability]
	if !ok {
		return nil
	}

	var path []string
	visited := make(map[string]bool)
	g.walk(capability, deps, &path, visited)
	return path
}

// walk recursively traverses the dependency graph.
func (g *CapabilityGraph) walk(name string, deps []string, path *[]string, visited map[string]bool) {
	if visited[name] {
		return
	}
	visited[name] = true
	*path = append(*path, name)

	for _, dep := range deps {
		childDeps, ok := g.nodes[dep]
		if !ok {
			*path = append(*path, dep)
			continue
		}
		g.walk(dep, childDeps, path, visited)
	}
}

// RequiredCapabilities returns the direct dependencies for a capability.
func (g *CapabilityGraph) RequiredCapabilities(capability string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	deps, ok := g.nodes[capability]
	if !ok {
		return nil
	}
	out := make([]string, len(deps))
	copy(out, deps)
	return out
}

// CanExecute returns true if the required capabilities for a given operation
// are available on at least one host in the infrastructure.
func (g *CapabilityGraph) CanExecute(capability string, mgr *Manager) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	deps, ok := g.nodes[capability]
	if !ok {
		return false
	}

	// Check each direct dependency.
	for _, dep := range deps {
		hosts := mgr.FindHost(HostCriteria{Capabilities: []string{dep}})
		if len(hosts) == 0 {
			return false
		}
	}
	return true
}

// AvailableOn returns hosts that have the specified capability.
func (g *CapabilityGraph) AvailableOn(capability string, mgr *Manager) []Host {
	return mgr.FindHost(HostCriteria{Capabilities: []string{capability}})
}
