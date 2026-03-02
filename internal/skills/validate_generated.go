package skills

import (
	"errors"
	"fmt"
	"strings"
)

// ValidateGeneratedSkill checks that a generated skill meets safety constraints.
// Non-generated skills (Generated == nil) always pass.
func ValidateGeneratedSkill(skill *Skill) error {
	if skill.Generated == nil {
		return nil
	}

	if len(skill.Tools) > 0 {
		return errors.New("generated skills cannot include Go tools")
	}

	if len(skill.Triggers) > 10 {
		return fmt.Errorf("too many triggers (%d > 10): would match too broadly", len(skill.Triggers))
	}

	for _, trigger := range skill.Triggers {
		if len(trigger) < 4 {
			return fmt.Errorf("trigger too short (min 4 chars): %q", trigger)
		}
		if strings.Contains(trigger, "*") {
			return errors.New("trigger contains wildcard: " + trigger)
		}
	}

	return nil
}
