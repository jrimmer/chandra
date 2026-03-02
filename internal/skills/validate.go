package skills

import (
	"os"
	"os/exec"
)

// ValidateRequirements checks that a skill's requirements are met.
// registeredTools maps tool names to presence (nil means no tools registered).
// Returns a list of missing items like "bin:gh", "env:GH_TOKEN", "tool:exec".
func ValidateRequirements(req SkillRequirements, registeredTools map[string]bool) []string {
	var missing []string

	for _, bin := range req.Bins {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, "bin:"+bin)
		}
	}

	for _, envVar := range req.Env {
		if os.Getenv(envVar) == "" {
			missing = append(missing, "env:"+envVar)
		}
	}

	for _, tool := range req.Tools {
		if registeredTools == nil || !registeredTools[tool] {
			missing = append(missing, "tool:"+tool)
		}
	}

	return missing
}
