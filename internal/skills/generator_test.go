package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSkillGenerator_Generate(t *testing.T) {
	tmpDir := t.TempDir()
	reg := NewRegistry()
	_ = reg.Load(context.Background(), tmpDir, nil)

	gen := &SkillGenerator{
		Registry:   reg,
		SkillsDir:  tmpDir,
		Explorer:   &CLIExplorer{},
		PkgManager: &ManualManager{},
	}

	// Generate a skill for "ls" (exists on all systems).
	err := gen.Generate(context.Background(), "ls", "file listing tool")
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	// SKILL.md should exist.
	skillPath := filepath.Join(tmpDir, "ls", "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		t.Fatalf("expected SKILL.md at %s", skillPath)
	}

	// Skill should be registered as pending.
	pending := reg.PendingReview()
	if len(pending) != 1 || pending[0].Name != "ls" {
		t.Errorf("expected ls in pending review, got %v", pending)
	}
}

func TestSkillGenerator_DuplicatePrevented(t *testing.T) {
	tmpDir := t.TempDir()
	reg := NewRegistry()
	_ = reg.Load(context.Background(), tmpDir, nil)

	gen := &SkillGenerator{
		Registry:   reg,
		SkillsDir:  tmpDir,
		Explorer:   &CLIExplorer{},
		PkgManager: &ManualManager{},
	}

	_ = gen.Generate(context.Background(), "ls", "file listing")
	err := gen.Generate(context.Background(), "ls", "file listing again")
	if err == nil {
		t.Error("expected error for duplicate generation")
	}
}

func TestSkillGenerator_GenerationTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	reg := NewRegistry()
	_ = reg.Load(context.Background(), tmpDir, nil)

	gen := &SkillGenerator{
		Registry:          reg,
		SkillsDir:         tmpDir,
		Explorer:          &CLIExplorer{},
		PkgManager:        &ManualManager{},
		GenerationTimeout: 1 * time.Millisecond,
	}

	// With 1ms timeout, generation should fail due to timeout.
	err := gen.Generate(context.Background(), "ls", "file listing")
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestSkillGenerator_MaxPendingReviewEnforced(t *testing.T) {
	tmpDir := t.TempDir()
	reg := NewRegistry()
	_ = reg.Load(context.Background(), tmpDir, nil)
	reg.SetMaxPendingReview(1)

	gen := &SkillGenerator{
		Registry:   reg,
		SkillsDir:  tmpDir,
		Explorer:   &CLIExplorer{},
		PkgManager: &ManualManager{},
	}

	// First generation should succeed.
	err := gen.Generate(context.Background(), "ls", "file listing")
	if err != nil {
		t.Fatalf("first generate failed: %v", err)
	}

	// Second should fail because we're at max pending.
	err = gen.Generate(context.Background(), "cat", "display file")
	if err == nil {
		t.Error("expected error when max pending review reached")
	}
}

func TestSkillGenerator_PostGenerationValidation(t *testing.T) {
	tmpDir := t.TempDir()
	reg := NewRegistry()
	_ = reg.Load(context.Background(), tmpDir, nil)

	gen := &SkillGenerator{
		Registry:   reg,
		SkillsDir:  tmpDir,
		Explorer:   &CLIExplorer{},
		PkgManager: &ManualManager{},
	}

	// Generate and then verify the generated SKILL.md can be re-parsed.
	err := gen.Generate(context.Background(), "ls", "file listing tool")
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	skillPath := filepath.Join(tmpDir, "ls", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("failed to read SKILL.md: %v", err)
	}

	skill, err := ParseSkillMD(data, skillPath)
	if err != nil {
		t.Fatalf("generated SKILL.md failed parsing: %v", err)
	}
	if skill.Name != "ls" {
		t.Errorf("expected name ls, got %q", skill.Name)
	}
}

func TestSkillGenerator_Definition(t *testing.T) {
	gen := &SkillGenerator{
		Registry:   NewRegistry(),
		SkillsDir:  "/tmp",
		Explorer:   &CLIExplorer{},
		PkgManager: &ManualManager{},
	}
	def := gen.Definition()
	if def.Name != "generate_skill" {
		t.Errorf("expected name generate_skill, got %q", def.Name)
	}
}
