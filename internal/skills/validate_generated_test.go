package skills

import (
	"testing"

	"github.com/jrimmer/chandra/pkg"
)

func TestValidateGeneratedSkill_Valid(t *testing.T) {
	s := &Skill{
		Name:     "docker",
		Triggers: []string{"docker", "container"},
		Generated: &GeneratedMeta{Status: SkillPendingReview},
	}
	if err := ValidateGeneratedSkill(s); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestValidateGeneratedSkill_TooManyTriggers(t *testing.T) {
	s := &Skill{
		Name:     "overkill",
		Triggers: make([]string, 11),
		Generated: &GeneratedMeta{Status: SkillPendingReview},
	}
	for i := range s.Triggers {
		s.Triggers[i] = "trigger" + string(rune('a'+i))
	}
	if err := ValidateGeneratedSkill(s); err == nil {
		t.Error("expected error for too many triggers")
	}
}

func TestValidateGeneratedSkill_ShortTrigger(t *testing.T) {
	s := &Skill{
		Name:     "bad",
		Triggers: []string{"go"},
		Generated: &GeneratedMeta{Status: SkillPendingReview},
	}
	if err := ValidateGeneratedSkill(s); err == nil {
		t.Error("expected error for short trigger")
	}
}

func TestValidateGeneratedSkill_WildcardTrigger(t *testing.T) {
	s := &Skill{
		Name:     "wild",
		Triggers: []string{"docker*"},
		Generated: &GeneratedMeta{Status: SkillPendingReview},
	}
	if err := ValidateGeneratedSkill(s); err == nil {
		t.Error("expected error for wildcard trigger")
	}
}

func TestValidateGeneratedSkill_NotGenerated(t *testing.T) {
	s := &Skill{Name: "manual"}
	if err := ValidateGeneratedSkill(s); err != nil {
		t.Errorf("non-generated skill should pass: %v", err)
	}
}

func TestValidateGeneratedSkill_HasTools(t *testing.T) {
	s := &Skill{
		Name:      "sneaky",
		Generated: &GeneratedMeta{Status: SkillPendingReview},
		Tools:     []pkg.ToolDef{{Name: "evil"}},
	}
	if err := ValidateGeneratedSkill(s); err == nil {
		t.Error("expected error for generated skill with tools")
	}
}
