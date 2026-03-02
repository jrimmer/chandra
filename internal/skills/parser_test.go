package skills

import "testing"

func TestParseSkillMD_ValidFile(t *testing.T) {
	input := `---
name: github
description: GitHub operations via gh CLI
version: 1.0.0
triggers:
  - github
  - pull request
  - PR
requires:
  bins: ["gh"]
  env: ["GH_TOKEN"]
---
# GitHub Skill

Use gh CLI for GitHub operations.

## Common Commands

List issues: ` + "`gh issue list`"

	skill, err := ParseSkillMD([]byte(input), "/path/to/github/SKILL.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skill.Name != "github" {
		t.Errorf("expected name github, got %q", skill.Name)
	}
	if skill.Description != "GitHub operations via gh CLI" {
		t.Errorf("expected description, got %q", skill.Description)
	}
	if skill.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %q", skill.Version)
	}
	if len(skill.Triggers) != 3 {
		t.Fatalf("expected 3 triggers, got %d", len(skill.Triggers))
	}
	if skill.Triggers[0] != "github" || skill.Triggers[2] != "PR" {
		t.Errorf("unexpected triggers: %v", skill.Triggers)
	}
	if len(skill.Requires.Bins) != 1 || skill.Requires.Bins[0] != "gh" {
		t.Errorf("expected bins [gh], got %v", skill.Requires.Bins)
	}
	if len(skill.Requires.Env) != 1 || skill.Requires.Env[0] != "GH_TOKEN" {
		t.Errorf("expected env [GH_TOKEN], got %v", skill.Requires.Env)
	}
	if skill.Content == "" {
		t.Error("expected non-empty content")
	}
	if skill.Path != "/path/to/github/SKILL.md" {
		t.Errorf("expected path, got %q", skill.Path)
	}
}

func TestParseSkillMD_NoFrontmatter(t *testing.T) {
	input := `# Just markdown, no frontmatter`
	_, err := ParseSkillMD([]byte(input), "/path")
	if err == nil {
		t.Error("expected error for missing frontmatter")
	}
}

func TestParseSkillMD_MissingName(t *testing.T) {
	input := "---\ndescription: test\n---\n# Content"
	_, err := ParseSkillMD([]byte(input), "/path")
	if err == nil {
		t.Error("expected error for missing name")
	}
}
