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
