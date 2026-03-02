package infra

import "testing"

func TestHost_HasCapabilities(t *testing.T) {
	h := Host{
		ID:           "node-1",
		Name:         "proxmox-01",
		Type:         "proxmox_node",
		Address:      "10.1.0.1",
		Capabilities: []string{"docker", "lxc"},
	}
	if len(h.Capabilities) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(h.Capabilities))
	}
}

func TestService_CreatedByPlan(t *testing.T) {
	s := Service{
		ID:        "svc-1",
		Name:      "nginx-web",
		Type:      "docker_container",
		Host:      "node-1",
		Status:    "running",
		CreatedBy: "plan-abc",
	}
	if s.CreatedBy != "plan-abc" {
		t.Errorf("expected plan-abc, got %q", s.CreatedBy)
	}
}

func TestHost_AccessMethod(t *testing.T) {
	h := Host{
		ID:   "node-2",
		Name: "remote-host",
		Access: AccessMethod{
			Type:        "ssh",
			Endpoint:    "10.1.0.2:22",
			Credentials: "encrypted-ref-123",
		},
	}
	if h.Access.Type != "ssh" {
		t.Errorf("expected ssh access type, got %q", h.Access.Type)
	}
	if h.Access.Endpoint != "10.1.0.2:22" {
		t.Errorf("expected 10.1.0.2:22, got %q", h.Access.Endpoint)
	}
}

func TestInfrastructureState_Resources(t *testing.T) {
	state := InfrastructureState{
		Resources: map[string]Resource{
			"cpu": {Type: "cpu", Total: 16, Available: 8, Unit: "cores"},
			"mem": {Type: "memory", Total: 65536, Available: 32768, Unit: "MB"},
		},
	}
	if len(state.Resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(state.Resources))
	}
	cpu := state.Resources["cpu"]
	if cpu.Available != 8 {
		t.Errorf("expected 8 available cores, got %d", cpu.Available)
	}
}
