package executor

import (
	"testing"
)

func TestPlanConcurrencyGuard_Acquire(t *testing.T) {
	g := NewPlanConcurrencyGuard(2)

	if !g.Acquire("plan-1") {
		t.Error("expected first acquire to succeed")
	}
	if !g.Acquire("plan-2") {
		t.Error("expected second acquire to succeed")
	}
	if g.Acquire("plan-3") {
		t.Error("expected third acquire to fail (max=2)")
	}
}

func TestPlanConcurrencyGuard_Release(t *testing.T) {
	g := NewPlanConcurrencyGuard(1)

	if !g.Acquire("plan-1") {
		t.Fatal("expected acquire to succeed")
	}
	if g.Acquire("plan-2") {
		t.Error("expected second acquire to fail")
	}

	g.Release("plan-1")

	if !g.Acquire("plan-2") {
		t.Error("expected acquire to succeed after release")
	}
}

func TestPlanConcurrencyGuard_DuplicateAcquire(t *testing.T) {
	g := NewPlanConcurrencyGuard(2)

	if !g.Acquire("plan-1") {
		t.Fatal("expected first acquire to succeed")
	}
	// Duplicate acquire of same plan should succeed (idempotent)
	if !g.Acquire("plan-1") {
		t.Error("expected duplicate acquire of same plan to succeed")
	}
}

func TestPlanConcurrencyGuard_ActivePlans(t *testing.T) {
	g := NewPlanConcurrencyGuard(3)
	g.Acquire("plan-a")
	g.Acquire("plan-b")

	active := g.ActivePlans()
	if len(active) != 2 {
		t.Errorf("expected 2 active plans, got %d", len(active))
	}
}

func TestPlanConcurrencyGuard_ZeroMax(t *testing.T) {
	// Zero max means unlimited
	g := NewPlanConcurrencyGuard(0)
	for i := 0; i < 100; i++ {
		if !g.Acquire("plan-" + string(rune('a'+i))) {
			t.Fatalf("expected unlimited acquire to succeed at %d", i)
		}
	}
}
