package planner

import (
	"context"
	"fmt"
	"testing"
)

type mockPlanner struct {
	gaps []Gap
}

func (m *mockPlanner) Decompose(ctx context.Context, goal string) (*ExecutionPlan, error) {
	return &ExecutionPlan{Goal: goal}, nil
}
func (m *mockPlanner) IdentifyGaps(ctx context.Context, plan *ExecutionPlan) ([]Gap, error) {
	return m.gaps, nil
}
func (m *mockPlanner) Replan(ctx context.Context, plan *ExecutionPlan, failedStep int) (*ExecutionPlan, error) {
	return plan, nil
}

type mockGenerator struct {
	generated []string
	failOn    string
}

func (m *mockGenerator) Generate(ctx context.Context, command, description string) error {
	if m.failOn == command {
		return fmt.Errorf("generation failed for %s", command)
	}
	m.generated = append(m.generated, command)
	return nil
}

func TestGapResolver_NoGaps(t *testing.T) {
	resolver := &GapResolver{
		Planner: &mockPlanner{gaps: nil},
	}

	unresolved, err := resolver.ResolveGaps(context.Background(), &ExecutionPlan{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unresolved) != 0 {
		t.Errorf("expected 0 unresolved, got %d", len(unresolved))
	}
}

func TestGapResolver_GeneratesSkill(t *testing.T) {
	gen := &mockGenerator{}
	resolver := &GapResolver{
		Planner: &mockPlanner{
			gaps: []Gap{
				{Type: "skill", Name: "deploy_k8s", Required: true, Resolution: "create"},
			},
		},
		Generator: gen,
		Confirm: func(ctx context.Context, desc string) (bool, error) {
			return true, nil // auto-approve
		},
	}

	unresolved, err := resolver.ResolveGaps(context.Background(), &ExecutionPlan{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unresolved) != 0 {
		t.Errorf("expected 0 unresolved, got %d", len(unresolved))
	}
	if len(gen.generated) != 1 || gen.generated[0] != "deploy_k8s" {
		t.Errorf("expected deploy_k8s generated, got %v", gen.generated)
	}
}

func TestGapResolver_ConfirmationDeclined(t *testing.T) {
	gen := &mockGenerator{}
	resolver := &GapResolver{
		Planner: &mockPlanner{
			gaps: []Gap{
				{Type: "skill", Name: "deploy_k8s", Required: true, Resolution: "create"},
			},
		},
		Generator: gen,
		Confirm: func(ctx context.Context, desc string) (bool, error) {
			return false, nil // decline
		},
	}

	unresolved, err := resolver.ResolveGaps(context.Background(), &ExecutionPlan{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unresolved) != 1 {
		t.Fatalf("expected 1 unresolved, got %d", len(unresolved))
	}
	if unresolved[0].Name != "deploy_k8s" {
		t.Errorf("expected deploy_k8s unresolved, got %q", unresolved[0].Name)
	}
	if len(gen.generated) != 0 {
		t.Errorf("expected no generation, got %v", gen.generated)
	}
}

func TestGapResolver_GenerationFailure(t *testing.T) {
	gen := &mockGenerator{failOn: "deploy_k8s"}
	resolver := &GapResolver{
		Planner: &mockPlanner{
			gaps: []Gap{
				{Type: "skill", Name: "deploy_k8s", Required: true, Resolution: "create"},
			},
		},
		Generator: gen,
		Confirm: func(ctx context.Context, desc string) (bool, error) {
			return true, nil
		},
	}

	unresolved, err := resolver.ResolveGaps(context.Background(), &ExecutionPlan{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unresolved) != 1 {
		t.Fatalf("expected 1 unresolved after generation failure, got %d", len(unresolved))
	}
}

func TestGapResolver_NonSkillGapsPassThrough(t *testing.T) {
	resolver := &GapResolver{
		Planner: &mockPlanner{
			gaps: []Gap{
				{Type: "infrastructure", Name: "proxmox_node", Required: true, Resolution: "configure"},
			},
		},
	}

	unresolved, err := resolver.ResolveGaps(context.Background(), &ExecutionPlan{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unresolved) != 1 {
		t.Fatalf("expected 1 unresolved non-skill gap, got %d", len(unresolved))
	}
	if unresolved[0].Type != "infrastructure" {
		t.Errorf("expected infrastructure gap, got %q", unresolved[0].Type)
	}
}
