package executor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnforceStepOutputLimit_UnderLimit(t *testing.T) {
	data := json.RawMessage(`{"result":"ok"}`)
	out, err := EnforceStepOutputLimit(data, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(data) {
		t.Errorf("expected unchanged data, got %q", out)
	}
}

func TestEnforceStepOutputLimit_OverLimit(t *testing.T) {
	data := json.RawMessage(`"` + strings.Repeat("x", 200) + `"`)
	_, err := EnforceStepOutputLimit(data, 100)
	if err == nil {
		t.Error("expected error for oversized output")
	}
}

func TestEnforceStepOutputLimit_NilData(t *testing.T) {
	out, err := EnforceStepOutputLimit(nil, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil output, got %q", out)
	}
}

func TestEnforcePlanStateLimit_UnderLimit(t *testing.T) {
	state := json.RawMessage(`{"step_0":"done"}`)
	out, err := EnforcePlanStateLimit(state, 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(state) {
		t.Errorf("expected unchanged state, got %q", out)
	}
}

func TestEnforcePlanStateLimit_OverLimit(t *testing.T) {
	state := json.RawMessage(`"` + strings.Repeat("y", 500) + `"`)
	_, err := EnforcePlanStateLimit(state, 100)
	if err == nil {
		t.Error("expected error for oversized plan state")
	}
}

func TestDefaultStateLimits(t *testing.T) {
	if DefaultMaxStepOutputBytes <= 0 {
		t.Error("expected positive default step output limit")
	}
	if DefaultMaxPlanStateBytes <= 0 {
		t.Error("expected positive default plan state limit")
	}
}
