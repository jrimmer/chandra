package skills

import (
	"time"

	"github.com/jrimmer/chandra/pkg"
)

// SkillStatus represents the approval status of a generated skill.
type SkillStatus string

const (
	SkillApproved      SkillStatus = "approved"
	SkillPendingReview SkillStatus = "pending_review"
	SkillRejected      SkillStatus = "rejected"
)

// GeneratedMeta holds metadata for auto-generated skills.
type GeneratedMeta struct {
	By         string      `yaml:"by"`
	Date       time.Time   `yaml:"date"`
	Source     string      `yaml:"source"`
	Status     SkillStatus `yaml:"status"`
	CreatedAt  string      `yaml:"created_at"`
	Reviewer   string      `yaml:"-"`
	ReviewedAt time.Time   `yaml:"-"`
}

// CronConfig declares a recurring scheduler job for a skill.
// When a skill with Cron set is loaded, the registry syncs a recurring intent
// so the agent runs on the specified schedule.
type CronConfig struct {
	Interval string `yaml:"interval"` // Go duration + "d"/"w" suffixes, e.g. "30m", "24h", "7d"
	Prompt   string `yaml:"prompt"`   // System prompt injected as the scheduled turn
	Channel  string `yaml:"channel"`  // Delivery channel hint: "default" or explicit channel_id
}

// Skill represents a loaded SKILL.md with parsed metadata and content.
type Skill struct {
	Name          string
	Description   string
	Summary       string            // Short description (< 100 tokens) for context injection
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
	Cron          *CronConfig    // Non-nil if skill declares a recurring cron job
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
