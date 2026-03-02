package integration

import (
	"context"
	"testing"

	"github.com/jrimmer/chandra/internal/infra"
)

func TestIntegration_Infrastructure(t *testing.T) {
	mgr := infra.NewManager()

	// Add a host.
	mgr.AddHost(infra.Host{
		ID:           "local",
		Name:         "localhost",
		Address:      "127.0.0.1",
		Type:         "bare_metal",
		Capabilities: []string{"docker"},
	})

	// Discover should succeed for localhost.
	err := mgr.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	status := mgr.HostStatus("local")
	if !status.Reachable {
		t.Error("localhost should be reachable")
	}

	// Find by capability.
	hosts := mgr.FindHost(infra.HostCriteria{Capabilities: []string{"docker"}})
	if len(hosts) != 1 {
		t.Errorf("expected 1 docker host, got %d", len(hosts))
	}

	// Credential encryption round-trip.
	key := infra.DeriveKey("test", []byte("0123456789abcdef"))
	enc, err := infra.Encrypt(key, []byte("secret-token"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	dec, err := infra.Decrypt(key, enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(dec) != "secret-token" {
		t.Error("credential encryption round-trip failed")
	}

	// Capability graph.
	graph := infra.NewCapabilityGraph()
	graph.AddCapability("deploy_docker", []string{"docker"})
	graph.AddCapability("docker", nil)

	if !graph.CanExecute("deploy_docker", mgr) {
		t.Error("expected deploy_docker to be executable with docker capability available")
	}

	path := graph.FindPath("deploy_docker")
	if len(path) != 2 {
		t.Errorf("expected path length 2, got %d: %v", len(path), path)
	}

	// GetState returns complete snapshot.
	state := mgr.GetState()
	if len(state.Hosts) != 1 {
		t.Errorf("expected 1 host in state, got %d", len(state.Hosts))
	}

	// Credential masking.
	mgr.AddHost(infra.Host{
		ID:   "remote",
		Name: "remote-host",
		Access: infra.AccessMethod{
			Type:        "ssh",
			Credentials: "secret-key-data",
		},
	})
	masked := infra.MaskCredential("secret-key-data")
	if masked != "****" {
		t.Errorf("expected masked credential, got %q", masked)
	}

	// Stale and warning checks.
	if !status.IsStale(0) {
		t.Error("expected stale with 0 TTL")
	}
}
