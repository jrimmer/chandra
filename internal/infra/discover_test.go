package infra

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestManager_HostStatus_InitialState(t *testing.T) {
	mgr := NewManager()
	mgr.AddHost(Host{ID: "h1", Name: "test-host", Address: "127.0.0.1"})

	status := mgr.HostStatus("h1")
	if !status.LastChecked.IsZero() {
		t.Error("expected zero LastChecked before first check")
	}
}

func TestManager_Discover_Localhost(t *testing.T) {
	mgr := NewManager()
	mgr.AddHost(Host{ID: "local", Name: "localhost", Address: "127.0.0.1"})

	err := mgr.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	status := mgr.HostStatus("local")
	if !status.Reachable {
		t.Error("localhost should be reachable")
	}
	if status.LastChecked.IsZero() {
		t.Error("expected non-zero LastChecked after discovery")
	}
}

func TestManager_Discover_PartialFailure(t *testing.T) {
	mgr := NewManager()
	mgr.AddHost(Host{ID: "local", Name: "localhost", Address: "127.0.0.1"})
	// Use TCP address with a port that won't be listening to force a failure.
	mgr.AddHost(Host{ID: "unreachable", Name: "bad-host", Address: "127.0.0.1:1"})

	// Discover should not return error even when one host fails.
	err := mgr.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover should not fail on partial failures: %v", err)
	}

	// Localhost should be reachable.
	status := mgr.HostStatus("local")
	if !status.Reachable {
		t.Error("localhost should be reachable")
	}

	// Unreachable host should be marked as not reachable.
	badStatus := mgr.HostStatus("unreachable")
	if badStatus.Reachable {
		t.Error("unreachable host should not be reachable")
	}
	if badStatus.ErrorCount == 0 {
		t.Error("expected error count > 0 for unreachable host")
	}
}

func TestHostReachability_IsStale(t *testing.T) {
	r := &HostReachability{
		LastSuccess: time.Now().Add(-2 * time.Hour),
	}
	if !r.IsStale(1 * time.Hour) {
		t.Error("expected stale with 1h TTL and 2h since last success")
	}
	if r.IsStale(3 * time.Hour) {
		t.Error("expected not stale with 3h TTL and 2h since last success")
	}
}

func TestHostReachability_IsStale_NeverSucceeded(t *testing.T) {
	r := &HostReachability{}
	if !r.IsStale(1 * time.Hour) {
		t.Error("expected stale when never succeeded")
	}
}

func TestHostReachability_NeedsWarning(t *testing.T) {
	r := &HostReachability{
		LastSuccess: time.Now().Add(-25 * time.Hour),
	}
	if !r.NeedsWarning() {
		t.Error("expected warning when last success > 24h ago")
	}

	r2 := &HostReachability{
		LastSuccess: time.Now().Add(-23 * time.Hour),
	}
	if r2.NeedsWarning() {
		t.Error("expected no warning when last success < 24h ago")
	}
}

func TestHostReachability_NeedsWarning_NeverSucceeded(t *testing.T) {
	r := &HostReachability{}
	if !r.NeedsWarning() {
		t.Error("expected warning when never succeeded")
	}
}

func TestManager_Discover_Backpressure(t *testing.T) {
	mgr := NewManager()
	mgr.MaxConcurrentHosts = 2

	// Add 5 hosts (all localhost so they're reachable).
	for i := 0; i < 5; i++ {
		mgr.AddHost(Host{
			ID:      fmt.Sprintf("h%d", i),
			Name:    fmt.Sprintf("host-%d", i),
			Address: "127.0.0.1",
		})
	}

	err := mgr.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	// With MaxConcurrentHosts=2, only 2 should be scanned.
	scanned := 0
	stale := 0
	for i := 0; i < 5; i++ {
		status := mgr.HostStatus(fmt.Sprintf("h%d", i))
		if !status.LastChecked.IsZero() {
			scanned++
		} else {
			stale++
		}
	}

	if scanned != 2 {
		t.Errorf("expected 2 scanned hosts with MaxConcurrentHosts=2, got %d", scanned)
	}
	if stale != 3 {
		t.Errorf("expected 3 stale hosts, got %d", stale)
	}
}

func TestManager_Discover_StaleHostsPrioritized(t *testing.T) {
	mgr := NewManager()
	mgr.MaxConcurrentHosts = 2

	// Add 4 hosts.
	for i := 0; i < 4; i++ {
		mgr.AddHost(Host{
			ID:      fmt.Sprintf("h%d", i),
			Name:    fmt.Sprintf("host-%d", i),
			Address: "127.0.0.1",
		})
	}

	// First discovery: scans 2, leaves 2 stale.
	_ = mgr.Discover(context.Background())

	// Find which were scanned.
	var scannedFirst []string
	var staleFirst []string
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("h%d", i)
		status := mgr.HostStatus(id)
		if !status.LastChecked.IsZero() {
			scannedFirst = append(scannedFirst, id)
		} else {
			staleFirst = append(staleFirst, id)
		}
	}

	if len(staleFirst) != 2 {
		t.Fatalf("expected 2 stale hosts after first discovery, got %d", len(staleFirst))
	}

	// Second discovery: stale hosts should be prioritized.
	_ = mgr.Discover(context.Background())

	// The previously stale hosts should now be scanned.
	for _, id := range staleFirst {
		status := mgr.HostStatus(id)
		if status.LastChecked.IsZero() {
			t.Errorf("expected previously stale host %s to be scanned, but it wasn't", id)
		}
	}
}
