package skills

import "testing"

func TestValidateRequirements_AllMet(t *testing.T) {
	// "ls" exists on every system
	req := SkillRequirements{Bins: []string{"ls"}}
	missing := ValidateRequirements(req, nil)
	if len(missing) != 0 {
		t.Errorf("expected no missing, got %v", missing)
	}
}

func TestValidateRequirements_MissingBin(t *testing.T) {
	req := SkillRequirements{Bins: []string{"nonexistent_binary_xyz"}}
	missing := ValidateRequirements(req, nil)
	if len(missing) != 1 || missing[0] != "bin:nonexistent_binary_xyz" {
		t.Errorf("expected [bin:nonexistent_binary_xyz], got %v", missing)
	}
}

func TestValidateRequirements_MissingEnv(t *testing.T) {
	req := SkillRequirements{Env: []string{"CHANDRA_TEST_NONEXISTENT"}}
	missing := ValidateRequirements(req, nil)
	if len(missing) != 1 || missing[0] != "env:CHANDRA_TEST_NONEXISTENT" {
		t.Errorf("expected [env:CHANDRA_TEST_NONEXISTENT], got %v", missing)
	}
}

func TestValidateRequirements_MissingTool(t *testing.T) {
	req := SkillRequirements{Tools: []string{"web.search"}}
	// No registered tools
	missing := ValidateRequirements(req, nil)
	if len(missing) != 1 || missing[0] != "tool:web.search" {
		t.Errorf("expected [tool:web.search], got %v", missing)
	}
}

func TestValidateRequirements_ToolPresent(t *testing.T) {
	req := SkillRequirements{Tools: []string{"web.search"}}
	registered := map[string]bool{"web.search": true}
	missing := ValidateRequirements(req, registered)
	if len(missing) != 0 {
		t.Errorf("expected no missing, got %v", missing)
	}
}
