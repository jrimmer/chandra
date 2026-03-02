package infra

import (
	"testing"
)

func TestManager_AddAndGetHost(t *testing.T) {
	mgr := NewManager()
	mgr.AddHost(Host{
		ID:           "h1",
		Name:         "docker-host",
		Type:         "lxc",
		Capabilities: []string{"docker"},
	})

	h, ok := mgr.GetHost("h1")
	if !ok {
		t.Fatal("expected to find host h1")
	}
	if h.Name != "docker-host" {
		t.Errorf("expected docker-host, got %q", h.Name)
	}

	_, ok = mgr.GetHost("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent host")
	}
}

func TestManager_AddAndFindHost(t *testing.T) {
	mgr := NewManager()
	mgr.AddHost(Host{
		ID:           "h1",
		Name:         "docker-host",
		Type:         "lxc",
		Capabilities: []string{"docker"},
	})

	hosts := mgr.FindHost(HostCriteria{Capabilities: []string{"docker"}})
	if len(hosts) != 1 || hosts[0].Name != "docker-host" {
		t.Errorf("expected docker-host, got %v", hosts)
	}

	hosts = mgr.FindHost(HostCriteria{Capabilities: []string{"k8s"}})
	if len(hosts) != 0 {
		t.Errorf("expected no k8s hosts, got %d", len(hosts))
	}
}

func TestManager_FindHost_ByType(t *testing.T) {
	mgr := NewManager()
	mgr.AddHost(Host{ID: "h1", Name: "lxc-host", Type: "lxc"})
	mgr.AddHost(Host{ID: "h2", Name: "vm-host", Type: "vm"})

	hosts := mgr.FindHost(HostCriteria{Type: "lxc"})
	if len(hosts) != 1 || hosts[0].Name != "lxc-host" {
		t.Errorf("expected lxc-host, got %v", hosts)
	}
}

func TestManager_RemoveHost(t *testing.T) {
	mgr := NewManager()
	mgr.AddHost(Host{ID: "h1", Name: "test-host"})

	mgr.RemoveHost("h1")
	_, ok := mgr.GetHost("h1")
	if ok {
		t.Error("expected host to be removed")
	}
}

func TestManager_AllHosts(t *testing.T) {
	mgr := NewManager()
	mgr.AddHost(Host{ID: "h1", Name: "host-1"})
	mgr.AddHost(Host{ID: "h2", Name: "host-2"})

	all := mgr.AllHosts()
	if len(all) != 2 {
		t.Errorf("expected 2 hosts, got %d", len(all))
	}
}

func TestManager_AddService(t *testing.T) {
	mgr := NewManager()
	mgr.AddService(Service{ID: "svc-1", Name: "nginx", Host: "h1", Status: "running"})

	services := mgr.FindService(ServiceCriteria{Host: "h1"})
	if len(services) != 1 || services[0].Name != "nginx" {
		t.Errorf("expected nginx service, got %v", services)
	}
}

func TestManager_RecordCreation(t *testing.T) {
	mgr := NewManager()
	err := mgr.RecordCreation("plan-1", []string{"container:nginx@h1"})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
}

func TestManager_GetState(t *testing.T) {
	mgr := NewManager()
	mgr.AddHost(Host{ID: "h1", Name: "host-1"})
	mgr.AddService(Service{ID: "svc-1", Name: "nginx"})

	state := mgr.GetState()
	if len(state.Hosts) != 1 {
		t.Errorf("expected 1 host in state, got %d", len(state.Hosts))
	}
	if len(state.Services) != 1 {
		t.Errorf("expected 1 service in state, got %d", len(state.Services))
	}
	if state.LastUpdated.IsZero() {
		t.Error("expected non-zero LastUpdated")
	}
}
