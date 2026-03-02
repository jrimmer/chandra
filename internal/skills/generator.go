package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrimmer/chandra/pkg"
)

// SkillGenerator discovers CLI tools and creates pending SKILL.md files.
type SkillGenerator struct {
	Registry          *Registry
	SkillsDir         string
	Explorer          *CLIExplorer
	PkgManager        PackageManager
	ProgressFunc      func(message string) // optional progress callback
	GenerationTimeout time.Duration        // max time for generation (0 = no limit)
}

// Definition returns the tool definition for the skill generator.
func (g *SkillGenerator) Definition() pkg.ToolDef {
	params, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":     map[string]any{"type": "string", "description": "CLI command to generate a skill for"},
			"description": map[string]any{"type": "string", "description": "Brief description of the tool"},
		},
		"required": []string{"command"},
	})
	return pkg.ToolDef{
		Name:         "generate_skill",
		Description:  "Discover a CLI tool's capabilities and generate a SKILL.md for it",
		Parameters:   params,
		Tier:         pkg.TierIsolated,
		Capabilities: []pkg.Capability{pkg.CapProcessExec, pkg.CapFileWrite, pkg.CapNetworkOut},
	}
}

func (g *SkillGenerator) progress(msg string) {
	if g.ProgressFunc != nil {
		g.ProgressFunc(msg)
	}
}

// Generate creates a SKILL.md for the given command and registers it as pending review.
func (g *SkillGenerator) Generate(ctx context.Context, command, description string) error {
	// Enforce GenerationTimeout.
	if g.GenerationTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.GenerationTimeout)
		defer cancel()
	}

	// Check MaxPendingReview before starting.
	if g.Registry.MaxPendingReviewReached() {
		return fmt.Errorf("too many skills pending review; approve or reject pending skills first")
	}

	// Check if skill already exists.
	if _, ok := g.Registry.Get(command); ok {
		return fmt.Errorf("skill %q already exists", command)
	}

	// Acquire generation lock.
	acquired, release := g.Registry.AcquireGenerationLock(command)
	if !acquired {
		return fmt.Errorf("skill %q is already being generated", command)
	}
	defer release()

	g.progress(fmt.Sprintf("Exploring %s capabilities...", command))

	// Check for context timeout.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("generation timed out: %w", err)
	}

	// Explore CLI capabilities.
	caps, err := g.Explorer.Explore(ctx, command)
	if err != nil {
		return fmt.Errorf("explore %s: %w", command, err)
	}

	// Check for context timeout after exploration.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("generation timed out: %w", err)
	}

	g.progress(fmt.Sprintf("Building SKILL.md for %s...", command))

	// Build SKILL.md content.
	content := g.buildSkillMD(command, description, caps)

	// Sanitize.
	flags := SanitizeContent(content)

	// Write to disk.
	skillDir := filepath.Join(g.SkillsDir, command)
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		return err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(content), 0600); err != nil {
		return err
	}

	// Post-generation test: verify the SKILL.md can be re-parsed.
	skill, err := ParseSkillMD([]byte(content), skillPath)
	if err != nil {
		return fmt.Errorf("generated skill failed parsing: %w", err)
	}

	skill.Generated = &GeneratedMeta{
		By:     "chandra",
		Date:   time.Now(),
		Source: fmt.Sprintf("%s --help exploration", command),
		Status: SkillPendingReview,
	}

	if err := ValidateGeneratedSkill(&skill); err != nil {
		return fmt.Errorf("generated skill validation failed: %w", err)
	}

	// Note sanitization flags.
	if len(flags) > 0 {
		skill.Generated.Source += fmt.Sprintf(" [sanitization flags: %v]", flags)
	}

	g.progress(fmt.Sprintf("Skill %s generated, pending review.", command))

	return g.Registry.Register(skill)
}

func (g *SkillGenerator) buildSkillMD(command, description string, caps *CLICapabilities) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %s\n", command))
	b.WriteString(fmt.Sprintf("description: %s\n", description))
	b.WriteString("version: 1.0.0\n")
	// Triggers must be at least 4 chars. If the command name is shorter,
	// also add the description as a trigger.
	b.WriteString("triggers:\n")
	if len(command) >= 4 {
		b.WriteString(fmt.Sprintf("  - %s\n", command))
	} else {
		// Use "command-cli" to ensure the trigger meets minimum length.
		b.WriteString(fmt.Sprintf("  - %s-cli\n", command))
		b.WriteString(fmt.Sprintf("  - \"%s command\"\n", command))
	}
	b.WriteString(fmt.Sprintf("requires:\n  bins: [\"%s\"]\n", command))
	b.WriteString("---\n\n")
	b.WriteString(fmt.Sprintf("# %s\n\n", command))
	if description != "" {
		b.WriteString(description + "\n\n")
	}
	if caps.Version != "" {
		b.WriteString(fmt.Sprintf("Version: %s\n\n", caps.Version))
	}
	if caps.HelpOutput != "" {
		b.WriteString("## Usage\n\n```\n")
		help := caps.HelpOutput
		if len(help) > 2000 {
			help = help[:2000] + "\n... (truncated)"
		}
		b.WriteString(help)
		b.WriteString("\n```\n")
	}
	if len(caps.Subcommands) > 0 {
		b.WriteString("\n## Subcommands\n\n")
		for _, sub := range caps.Subcommands {
			b.WriteString(fmt.Sprintf("- `%s`\n", sub))
		}
	}
	return b.String()
}
