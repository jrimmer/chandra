package infra

import "testing"

func TestCapabilityGraph_FindPath(t *testing.T) {
	graph := NewCapabilityGraph()
	graph.AddCapability("deploy_docker", []string{"docker_host"})
	graph.AddCapability("docker_host", []string{"lxc_container"})
	graph.AddCapability("lxc_container", []string{"proxmox_node"})
	graph.AddCapability("proxmox_node", nil) // root capability

	path := graph.FindPath("deploy_docker")
	if len(path) != 4 {
		t.Errorf("expected path length 4, got %d: %v", len(path), path)
	}
	// Path should be: deploy_docker -> docker_host -> lxc_container -> proxmox_node
	expected := []string{"deploy_docker", "docker_host", "lxc_container", "proxmox_node"}
	for i, exp := range expected {
		if path[i] != exp {
			t.Errorf("path[%d] = %q, want %q", i, path[i], exp)
		}
	}
}

func TestCapabilityGraph_NoPath(t *testing.T) {
	graph := NewCapabilityGraph()
	path := graph.FindPath("nonexistent")
	if path != nil {
		t.Error("expected nil path for nonexistent capability")
	}
}

func TestCapabilityGraph_CanExecute(t *testing.T) {
	graph := NewCapabilityGraph()
	graph.AddCapability("deploy_docker", []string{"docker_host"})
	graph.AddCapability("docker_host", nil)

	mgr := NewManager()
	mgr.AddHost(Host{
		ID:           "h1",
		Name:         "docker-host",
		Capabilities: []string{"docker_host"},
	})

	if !graph.CanExecute("deploy_docker", mgr) {
		t.Error("expected CanExecute to return true when docker_host is available")
	}

	if graph.CanExecute("deploy_k8s", mgr) {
		t.Error("expected CanExecute to return false for unknown capability")
	}
}

func TestCapabilityGraph_RequiredCapabilities(t *testing.T) {
	graph := NewCapabilityGraph()
	graph.AddCapability("deploy_docker", []string{"docker_host"})
	graph.AddCapability("docker_host", []string{"lxc_container"})

	deps := graph.RequiredCapabilities("deploy_docker")
	if len(deps) != 1 || deps[0] != "docker_host" {
		t.Errorf("expected [docker_host], got %v", deps)
	}
}

func TestCapabilityGraph_AvailableOn(t *testing.T) {
	graph := NewCapabilityGraph()
	graph.AddCapability("docker", nil)

	mgr := NewManager()
	mgr.AddHost(Host{ID: "h1", Name: "host-1", Capabilities: []string{"docker"}})
	mgr.AddHost(Host{ID: "h2", Name: "host-2", Capabilities: []string{"k8s"}})

	hosts := graph.AvailableOn("docker", mgr)
	if len(hosts) != 1 || hosts[0].ID != "h1" {
		t.Errorf("expected host h1, got %v", hosts)
	}
}

func TestCapabilityGraph_CycleDetection(t *testing.T) {
	graph := NewCapabilityGraph()
	graph.AddCapability("a", []string{"b"})
	graph.AddCapability("b", []string{"a"}) // cycle

	path := graph.FindPath("a")
	// Should not infinite loop; path may be partial.
	if path == nil {
		t.Error("expected non-nil path even with cycle")
	}
}
