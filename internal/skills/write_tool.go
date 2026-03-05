package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jrimmer/chandra/pkg"
)

// Compile-time check that WriteSkillTool satisfies pkg.Tool.
var _ pkg.Tool = (*WriteSkillTool)(nil)

// writeSkillNameRe validates skill names: lowercase alphanumeric + hyphens.
var writeSkillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)

// WriteSkillTool allows the LLM to draft a new SKILL.md and save it for review.
// The skill is written as pending_review; an admin runs `chandra skill approve`
// to make it live.
type WriteSkillTool struct {
	registry  *Registry
	skillsDir string
}

// NewWriteSkillTool returns a pkg.Tool that writes SKILL.md files.
func NewWriteSkillTool(registry *Registry, skillsDir string) *WriteSkillTool {
	return &WriteSkillTool{registry: registry, skillsDir: skillsDir}
}

func (w *WriteSkillTool) Definition() pkg.ToolDef {
	params, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name (lowercase, hyphens OK, e.g. flightradar24). Used as the directory name.",
			},
			"content": map[string]any{
				"type": "string",
				"description": "Full SKILL.md content including YAML frontmatter delimited by ---. " +
					"Must include name, description, version, triggers, and a markdown body with usage instructions.",
			},
		},
		"required": []string{"name", "content"},
	})
	return pkg.ToolDef{
		Name: "write_skill",
		Description: "Draft a new skill by writing a SKILL.md file. The skill is saved as pending_review " +
			"and must be approved with 'chandra skill approve <name>' before it becomes active. " +
			"Use this when the user asks you to create, build, or add a new skill.",
		Parameters:   params,
		Tier:         pkg.TierIsolated,
		Capabilities: []pkg.Capability{pkg.CapFileWrite},
	}
}

// Execute implements pkg.Tool.
func (w *WriteSkillTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var params struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(call.Parameters, &params); err != nil {
		return badInput(call.ID, err.Error()), nil
	}

	name := strings.TrimSpace(params.Name)
	content := strings.TrimSpace(params.Content)

	if name == "" {
		return badInput(call.ID, "name is required"), nil
	}
	if !writeSkillNameRe.MatchString(name) {
		return badInput(call.ID,
			fmt.Sprintf("invalid skill name %q: must be lowercase alphanumeric with optional hyphens, 2–64 chars", name)), nil
	}
	if content == "" {
		return badInput(call.ID, "content is required"), nil
	}

	// Validate the content can be parsed as a skill.
	skill, err := ParseSkillMD([]byte(content), name+"/SKILL.md")
	if err != nil {
		return badInput(call.ID, fmt.Sprintf("SKILL.md parse error: %v", err)), nil
	}
	if skill.Name != name {
		return badInput(call.ID,
			fmt.Sprintf("frontmatter name %q does not match requested name %q", skill.Name, name)), nil
	}

	// Check if skill already exists.
	if _, exists := w.registry.Get(name); exists {
		return badInput(call.ID,
			fmt.Sprintf("skill %q already exists; edit manually then run: chandra skill reload", name)), nil
	}

	// Write to disk.
	skillDir := filepath.Join(w.skillsDir, name)
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		return internal(call.ID, fmt.Sprintf("create skill directory: %v", err)), nil
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(content), 0600); err != nil {
		return internal(call.ID, fmt.Sprintf("write SKILL.md: %v", err)), nil
	}

	// Tag as pending_review.
	skill.Generated = &GeneratedMeta{
		By:        "chandra-llm",
		Date:      time.Now(),
		Source:    "conversational generation",
		Status:    SkillPendingReview,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	skill.Path = skillPath

	if err := w.registry.Register(skill); err != nil {
		return internal(call.ID, fmt.Sprintf("register skill: %v", err)), nil
	}

	return pkg.ToolResult{
		ID: call.ID,
		Content: fmt.Sprintf(
			"Skill %q written to %s and registered as pending_review.\n"+
				"Run: chandra skill approve %s\n"+
				"Then: chandra skill reload",
			name, skillPath, name,
		),
	}, nil
}

func badInput(id, msg string) pkg.ToolResult {
	return pkg.ToolResult{ID: id, Error: &pkg.ToolError{Kind: pkg.ErrBadInput, Message: msg}}
}

func internal(id, msg string) pkg.ToolResult {
	return pkg.ToolResult{ID: id, Error: &pkg.ToolError{Kind: pkg.ErrInternal, Message: msg}}
}
