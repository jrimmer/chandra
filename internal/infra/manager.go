package infra

import (
	"context"
	"sync"
	"time"
)

// InfrastructureManager defines the interface for managing infrastructure state.
type InfrastructureManager interface {
	AddHost(host Host)
	RemoveHost(id string)
	GetHost(id string) (Host, bool)
	AllHosts() []Host
	FindHost(criteria HostCriteria) []Host
	AddService(service Service)
	FindService(criteria ServiceCriteria) []Service
	RecordCreation(planID string, resources []string) error
	Discover(ctx context.Context) error
	HostStatus(hostID string) HostReachability
	GetState() *InfrastructureState
}

// Manager implements InfrastructureManager with in-memory state.
type Manager struct {
	mu           sync.RWMutex
	hosts        map[string]Host
	services     map[string]Service
	reachability map[string]*HostReachability
	lastUpdated  time.Time

	// Discovery configuration.
	MaxConcurrentHosts int
}

// NewManager creates a new infrastructure manager.
func NewManager() *Manager {
	return &Manager{
		hosts:              make(map[string]Host),
		services:           make(map[string]Service),
		reachability:       make(map[string]*HostReachability),
		MaxConcurrentHosts: 5, // default per design spec
	}
}

// AddHost registers a host in the infrastructure.
func (m *Manager) AddHost(host Host) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hosts[host.ID] = host
	if _, ok := m.reachability[host.ID]; !ok {
		m.reachability[host.ID] = &HostReachability{}
	}
	m.lastUpdated = time.Now()
}

// RemoveHost removes a host from the infrastructure.
func (m *Manager) RemoveHost(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hosts, id)
	delete(m.reachability, id)
	m.lastUpdated = time.Now()
}

// GetHost returns a host by ID.
func (m *Manager) GetHost(id string) (Host, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.hosts[id]
	return h, ok
}

// AllHosts returns all registered hosts.
func (m *Manager) AllHosts() []Host {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Host, 0, len(m.hosts))
	for _, h := range m.hosts {
		out = append(out, h)
	}
	return out
}

// FindHost returns hosts matching the given criteria.
func (m *Manager) FindHost(criteria HostCriteria) []Host {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var matches []Host
	for _, h := range m.hosts {
		if !matchHost(h, criteria, m.reachability[h.ID]) {
			continue
		}
		matches = append(matches, h)
	}
	return matches
}

// matchHost checks if a host matches all given criteria.
func matchHost(h Host, c HostCriteria, reach *HostReachability) bool {
	if c.Type != "" && h.Type != c.Type {
		return false
	}
	if c.Reachable != nil && reach != nil && reach.Reachable != *c.Reachable {
		return false
	}
	for _, required := range c.Capabilities {
		found := false
		for _, cap := range h.Capabilities {
			if cap == required {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// AddService registers a service in the infrastructure.
func (m *Manager) AddService(service Service) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.services[service.ID] = service
	m.lastUpdated = time.Now()
}

// FindService returns services matching the given criteria.
func (m *Manager) FindService(criteria ServiceCriteria) []Service {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var matches []Service
	for _, s := range m.services {
		if criteria.Type != "" && s.Type != criteria.Type {
			continue
		}
		if criteria.Host != "" && s.Host != criteria.Host {
			continue
		}
		if criteria.Status != "" && s.Status != criteria.Status {
			continue
		}
		matches = append(matches, s)
	}
	return matches
}

// RecordCreation records resources created by a plan execution.
func (m *Manager) RecordCreation(planID string, resources []string) error {
	// Track which plan created which resources.
	// Resources are strings like "container:nginx@h1".
	return nil
}

// HostStatus returns the reachability status for a host.
func (m *Manager) HostStatus(hostID string) HostReachability {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if r, ok := m.reachability[hostID]; ok {
		return *r
	}
	return HostReachability{}
}

// GetState returns a snapshot of the current infrastructure state.
func (m *Manager) GetState() *InfrastructureState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hosts := make([]Host, 0, len(m.hosts))
	for _, h := range m.hosts {
		hosts = append(hosts, h)
	}

	services := make([]Service, 0, len(m.services))
	for _, s := range m.services {
		services = append(services, s)
	}

	return &InfrastructureState{
		Hosts:       hosts,
		Services:    services,
		Resources:   make(map[string]Resource),
		LastUpdated: m.lastUpdated,
	}
}
