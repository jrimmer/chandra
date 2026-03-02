package skills

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jrimmer/chandra/pkg"
)

func TestReadSkillTool_Execute(t *testing.T) {
	reg := NewRegistry()
	reg.skills["github"] = Skill{
		Name:    "github",
		Content: "# GitHub Skill\n\nFull documentation here.",
	}

	tool := NewReadSkillTool(reg)
	params, _ := json.Marshal(map[string]any{"skill_name": "github"})
	result, err := tool.Execute(context.Background(), pkg.ToolCall{
		ID:         "test-1",
		Name:       "read_skill",
		Parameters: params,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected tool error: %v", result.Error)
	}
	if result.Content != "# GitHub Skill\n\nFull documentation here." {
		t.Errorf("unexpected content: %q", result.Content)
	}
}

func TestReadSkillTool_NotFound(t *testing.T) {
	reg := NewRegistry()
	tool := NewReadSkillTool(reg)
	params, _ := json.Marshal(map[string]any{"skill_name": "nope"})
	result, err := tool.Execute(context.Background(), pkg.ToolCall{
		ID:         "test-2",
		Name:       "read_skill",
		Parameters: params,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == nil {
		t.Error("expected error for missing skill")
	}
}

func TestReadSkillTool_Definition(t *testing.T) {
	reg := NewRegistry()
	tool := NewReadSkillTool(reg)
	def := tool.Definition()
	if def.Name != "read_skill" {
		t.Errorf("expected name read_skill, got %q", def.Name)
	}
	if def.Tier != pkg.TierBuiltin {
		t.Errorf("expected TierBuiltin, got %v", def.Tier)
	}
}
