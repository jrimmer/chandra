package pkg

import "testing"

func TestToolCall_PlanFields(t *testing.T) {
	tc := ToolCall{
		ID:        "test-1",
		Name:      "exec",
		SkillName: "docker",
		PlanID:    "plan-abc",
		StepIndex: 3,
	}
	if tc.PlanID != "plan-abc" {
		t.Errorf("expected plan-abc, got %q", tc.PlanID)
	}
	if tc.StepIndex != 3 {
		t.Errorf("expected step 3, got %d", tc.StepIndex)
	}
	if tc.SkillName != "docker" {
		t.Errorf("expected docker, got %q", tc.SkillName)
	}
}
