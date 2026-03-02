package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jrimmer/chandra/internal/skills"
)

func TestIntegration_SkillGeneration(t *testing.T) {
	tmpDir := t.TempDir()
	reg := skills.NewRegistry()
	_ = reg.Load(context.Background(), tmpDir, nil)

	gen := &skills.SkillGenerator{
		Registry:   reg,
		SkillsDir:  tmpDir,
		Explorer:   &skills.CLIExplorer{},
		PkgManager: &skills.ManualManager{},
	}

	// Generate skill for "ls".
	err := gen.Generate(context.Background(), "ls", "list files")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Should be pending.
	pending := reg.PendingReview()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}

	// Should NOT match until approved (triggers are "ls-cli", "ls command" for short commands).
	matches := reg.Match("use the ls-cli")
	if len(matches) != 0 {
		t.Error("pending skill should not match")
	}

	// Approve.
	_ = reg.Approve("ls", "tester")

	// Now should match.
	matches = reg.Match("use the ls-cli")
	if len(matches) == 0 {
		t.Error("expected match after approval")
	}

	// SKILL.md file should exist on disk.
	skillPath := filepath.Join(tmpDir, "ls", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("SKILL.md not found: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty SKILL.md")
	}
}

func TestIntegration_SkillRejection(t *testing.T) {
	tmpDir := t.TempDir()
	reg := skills.NewRegistry()
	_ = reg.Load(context.Background(), tmpDir, nil)

	gen := &skills.SkillGenerator{
		Registry:   reg,
		SkillsDir:  tmpDir,
		Explorer:   &skills.CLIExplorer{},
		PkgManager: &skills.ManualManager{},
	}

	_ = gen.Generate(context.Background(), "cat", "display file contents")

	// Reject the skill.
	_ = reg.Reject("cat", "tester")

	// Should not match.
	matches := reg.Match("cat a file")
	if len(matches) != 0 {
		t.Error("rejected skill should not match")
	}

	// Pending list should be empty.
	if len(reg.PendingReview()) != 0 {
		t.Error("expected no pending after rejection")
	}
}

func TestIntegration_ReadSkillTool(t *testing.T) {
	tmpDir := t.TempDir()
	reg := skills.NewRegistry()
	_ = reg.Load(context.Background(), tmpDir, nil)

	gen := &skills.SkillGenerator{
		Registry:   reg,
		SkillsDir:  tmpDir,
		Explorer:   &skills.CLIExplorer{},
		PkgManager: &skills.ManualManager{},
	}

	_ = gen.Generate(context.Background(), "ls", "list files")
	_ = reg.Approve("ls", "tester")

	tool := skills.NewReadSkillTool(reg)
	def := tool.Definition()
	if def.Name != "read_skill" {
		t.Errorf("expected read_skill, got %q", def.Name)
	}
}
