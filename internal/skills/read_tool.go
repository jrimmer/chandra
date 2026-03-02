package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jrimmer/chandra/pkg"
)

// Compile-time check that ReadSkillTool satisfies pkg.Tool.
var _ pkg.Tool = (*ReadSkillTool)(nil)

// ReadSkillTool is a built-in tool that fetches full skill content on demand.
type ReadSkillTool struct {
	registry *Registry
}

func NewReadSkillTool(registry *Registry) *ReadSkillTool {
	return &ReadSkillTool{registry: registry}
}

func (r *ReadSkillTool) Definition() pkg.ToolDef {
	params, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill_name": map[string]any{"type": "string", "description": "Name of the skill"},
		},
		"required": []string{"skill_name"},
	})
	return pkg.ToolDef{
		Name:        "read_skill",
		Description: "Get full documentation for a skill by name",
		Parameters:  params,
		Tier:        pkg.TierBuiltin,
	}
}

// Execute implements pkg.Tool.
func (r *ReadSkillTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var params map[string]any
	if err := json.Unmarshal(call.Parameters, &params); err != nil {
		return pkg.ToolResult{
			ID:    call.ID,
			Error: &pkg.ToolError{Kind: pkg.ErrBadInput, Message: err.Error()},
		}, nil
	}

	name, _ := params["skill_name"].(string)
	if name == "" {
		return pkg.ToolResult{
			ID:    call.ID,
			Error: &pkg.ToolError{Kind: pkg.ErrBadInput, Message: "skill_name is required"},
		}, nil
	}

	skill, ok := r.registry.Get(name)
	if !ok {
		return pkg.ToolResult{
			ID:    call.ID,
			Error: &pkg.ToolError{Kind: pkg.ErrNotFound, Message: fmt.Sprintf("skill not found: %s", name)},
		}, nil
	}

	return pkg.ToolResult{
		ID:      call.ID,
		Content: skill.Content,
	}, nil
}
