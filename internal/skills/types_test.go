package skills

import "testing"

func TestSkillRequirements_Empty(t *testing.T) {
	req := SkillRequirements{}
	if len(req.Bins) != 0 || len(req.Tools) != 0 || len(req.Env) != 0 {
		t.Error("expected empty requirements")
	}
}

func TestUnmetSkill_HasMissing(t *testing.T) {
	u := UnmetSkill{
		Name:    "github",
		Path:    "/path/to/SKILL.md",
		Missing: []string{"bin:gh"},
	}
	if u.Name != "github" {
		t.Errorf("expected name github, got %q", u.Name)
	}
	if len(u.Missing) != 1 || u.Missing[0] != "bin:gh" {
		t.Errorf("expected missing [bin:gh], got %v", u.Missing)
	}
}
