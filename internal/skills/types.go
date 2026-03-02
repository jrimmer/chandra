package skills

import "github.com/jrimmer/chandra/pkg"

// SkillStatus represents the approval status of a generated skill.
type SkillStatus string

const (
	SkillApproved      SkillStatus = "approved"
	SkillPendingReview SkillStatus = "pending_review"
	SkillRejected      SkillStatus = "rejected"
)

// GeneratedMeta holds metadata for auto-generated skills.
type GeneratedMeta struct {
	Status    SkillStatus `yaml:"status"`
	Source    string      `yaml:"source"`
	CreatedAt string     `yaml:"created_at"`
}

// Skill represents a loaded SKILL.md with parsed metadata and content.
type Skill struct {
	Name          string
	Description   string
	Version       string
	Triggers      []string
	Requires      SkillRequirements
	Config        map[string]any
	Content       string         // Full markdown body (after frontmatter)
	Path          string         // Filesystem path to SKILL.md
	DependsOn     []string       // Other skills required (e.g., ["docker"] for kubernetes)
	Tools         []pkg.ToolDef  // Go tools defined by this skill (only for built-in skills)
	RequiresShell bool           // Whether this skill needs shell access
	Generated     *GeneratedMeta // Non-nil for auto-generated skills
}

// SkillRequirements declares what a skill needs to function.
type SkillRequirements struct {
	Bins  []string `yaml:"bins"`
	Tools []string `yaml:"tools"`
	Env   []string `yaml:"env"`
}

// UnmetSkill records a skill that could not load due to missing requirements.
type UnmetSkill struct {
	Name    string
	Path    string
	Missing []string // e.g. "bin:gh", "env:GH_TOKEN"
}
