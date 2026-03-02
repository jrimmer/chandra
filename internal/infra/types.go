package infra

import "time"

// InfrastructureState holds the complete infrastructure view.
type InfrastructureState struct {
	Hosts       []Host
	Services    []Service
	Resources   map[string]Resource
	LastUpdated time.Time
}

// Host represents a managed host (bare metal, VM, LXC, etc.).
type Host struct {
	ID           string
	Name         string
	Type         string   // "proxmox_node", "vm", "lxc", "bare_metal"
	Address      string
	Capabilities []string // "docker", "k8s", "lxc"
	Parent       string   // Parent host ID
	Access       AccessMethod
}

// AccessMethod describes how to connect to a host.
type AccessMethod struct {
	Type        string // "ssh", "api", "local"
	Endpoint    string // SSH address, API URL, etc.
	Credentials string // Encrypted credential reference
}

// Service represents a running service on a host.
type Service struct {
	ID        string
	Name      string
	Type      string // "docker_container", "systemd", "k8s_pod"
	Host      string
	Status    string // "running", "stopped", "unknown"
	Ports     []PortMapping
	CreatedBy string // Plan ID that created this
}

// PortMapping maps a host port to a container port.
type PortMapping struct {
	Host      int
	Container int
	Protocol  string
}

// Resource describes a quantifiable resource on a host.
type Resource struct {
	Type      string // "cpu", "memory", "disk"
	Total     int64
	Available int64
	Unit      string // "cores", "MB", "GB"
}

// HostReachability tracks connectivity status for a host.
type HostReachability struct {
	Reachable   bool
	LastChecked time.Time
	LastSuccess time.Time
	ErrorCount  int
	LastError   string
}

// IsStale returns true if the last success is older than the given TTL.
func (h *HostReachability) IsStale(ttl time.Duration) bool {
	if h.LastSuccess.IsZero() {
		return true
	}
	return time.Since(h.LastSuccess) > ttl
}

// NeedsWarning returns true if the host hasn't been successfully contacted in 24 hours.
func (h *HostReachability) NeedsWarning() bool {
	if h.LastSuccess.IsZero() {
		return true
	}
	return time.Since(h.LastSuccess) > 24*time.Hour
}

// HostCriteria filters hosts by type and capabilities.
type HostCriteria struct {
	Type         string
	Capabilities []string
	Reachable    *bool
}

// ServiceCriteria filters services by type, host, and status.
type ServiceCriteria struct {
	Type   string
	Host   string
	Status string
}
