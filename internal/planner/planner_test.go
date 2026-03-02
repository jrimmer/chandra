package planner

import (
	"context"
	"testing"
)

func TestPlanner_Decompose(t *testing.T) {
	p := NewPlanner(nil, nil)
	if p == nil {
		t.Fatal("expected non-nil planner")
	}

	plan, err := p.Decompose(context.Background(), "deploy nginx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Goal != "deploy nginx" {
		t.Errorf("expected goal 'deploy nginx', got %q", plan.Goal)
	}
	if plan.Status != PlanPlanning {
		t.Errorf("expected status %q, got %q", PlanPlanning, plan.Status)
	}
}

func TestPlanner_IdentifyGaps(t *testing.T) {
	p := NewPlanner(nil, nil)
	gaps, err := p.IdentifyGaps(context.Background(), &ExecutionPlan{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gaps != nil {
		t.Errorf("expected nil gaps, got %v", gaps)
	}
}

func TestPlanner_Replan(t *testing.T) {
	p := NewPlanner(nil, nil)
	plan := &ExecutionPlan{Goal: "test"}
	replanned, err := p.Replan(context.Background(), plan, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if replanned == nil {
		t.Fatal("expected non-nil replanned plan")
	}
}

// mockCapResolver implements CapabilityResolver for testing.
type mockCapResolver struct {
	paths map[string][]string
}

func (m *mockCapResolver) FindPath(capability string) []string {
	return m.paths[capability]
}

func (m *mockCapResolver) RequiredCapabilities(capability string) []string {
	return nil
}

func TestPlanner_IdentifyGaps_WithCapabilities(t *testing.T) {
	resolver := &mockCapResolver{
		paths: map[string][]string{
			"deploy_docker": {"deploy_docker", "docker_host", "lxc_container", "proxmox_node"},
		},
	}
	p := NewPlanner(nil, nil, WithCapabilities(resolver))

	plan := &ExecutionPlan{
		Goal: "deploy app",
		Steps: []ExecutionStep{
			{SkillName: "deploy_docker"}, // known capability
			{SkillName: "deploy_k8s"},    // unknown capability
		},
	}

	gaps, err := p.IdentifyGaps(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gaps) != 1 {
		t.Fatalf("expected 1 gap, got %d", len(gaps))
	}
	if gaps[0].Name != "deploy_k8s" {
		t.Errorf("expected gap for deploy_k8s, got %q", gaps[0].Name)
	}
	if gaps[0].Type != "skill" {
		t.Errorf("expected gap type 'skill', got %q", gaps[0].Type)
	}
}
