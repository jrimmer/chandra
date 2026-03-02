package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/budget"
	"github.com/jrimmer/chandra/internal/channels"
	"github.com/jrimmer/chandra/internal/skills"
	"github.com/jrimmer/chandra/pkg"
)

type stubSkillRegistry struct {
	matched []skills.Skill
}

func (s *stubSkillRegistry) Match(message string) []skills.Skill { return s.matched }

// capturingBudget records the ranked candidates passed to Assemble.
type capturingBudget struct {
	captured *[]budget.ContextCandidate
}

func (c *capturingBudget) Assemble(
	_ context.Context,
	_ int,
	_ []budget.ContextCandidate,
	ranked []budget.ContextCandidate,
	_ []pkg.ToolDef,
	_ int,
) (budget.ContextWindow, error) {
	*c.captured = append(*c.captured, ranked...)
	return budget.ContextWindow{}, nil
}

func TestAssembleContext_WithSkills(t *testing.T) {
	skill := skills.Skill{
		Name:    "github",
		Content: "# GitHub\nUse gh CLI.",
	}
	reg := &stubSkillRegistry{matched: []skills.Skill{skill}}

	msg := channels.InboundMessage{Content: "create a pull request"}

	// Use a mock budget manager that captures candidates.
	var capturedRanked []budget.ContextCandidate
	mockBudget := &capturingBudget{captured: &capturedRanked}

	skillCfg := SkillConfig{
		Registry:         reg,
		Priority:         0.7,
		MaxContextTokens: 2000,
		MaxMatches:       3,
	}

	_, _ = assembleContext(context.Background(), msg, nil, mockBudget, 8000, nil, nil, &skillCfg)

	// Verify skill was added as a ranked candidate.
	found := false
	for _, c := range capturedRanked {
		if c.Role == "skill" {
			found = true
			if c.Priority != 0.7 {
				t.Errorf("expected skill priority 0.7, got %f", c.Priority)
			}
		}
	}
	if !found {
		t.Error("expected skill candidate in ranked list")
	}
}

func TestAssembleContext_WithoutSkills(t *testing.T) {
	msg := channels.InboundMessage{Content: "hello"}

	var capturedRanked []budget.ContextCandidate
	mockBudget := &capturingBudget{captured: &capturedRanked}

	// nil skillCfg — no skills.
	_, _ = assembleContext(context.Background(), msg, nil, mockBudget, 8000, nil, nil, nil)

	// Verify no skill candidates.
	for _, c := range capturedRanked {
		if c.Role == "skill" {
			t.Error("unexpected skill candidate when no skill config")
		}
	}
}

func TestAssembleContext_SkillSummaryInjection(t *testing.T) {
	skill := skills.Skill{
		Name:    "github",
		Summary: "GitHub operations via gh CLI.",
		Content: "# Full GitHub docs...",
	}
	reg := &stubSkillRegistry{matched: []skills.Skill{skill}}

	msg := channels.InboundMessage{Content: "create a pull request"}

	var capturedRanked []budget.ContextCandidate
	mockBudget := &capturingBudget{captured: &capturedRanked}

	skillCfg := SkillConfig{
		Registry:         reg,
		Priority:         0.7,
		MaxContextTokens: 2000,
		MaxMatches:       3,
	}

	_, _ = assembleContext(context.Background(), msg, nil, mockBudget, 8000, nil, nil, &skillCfg)

	// Verify injected content contains Summary, not full Content.
	found := false
	for _, c := range capturedRanked {
		if c.Role == "skill" {
			found = true
			if !strings.Contains(c.Content, "GitHub operations via gh CLI.") {
				t.Errorf("expected summary in content, got %q", c.Content)
			}
			if strings.Contains(c.Content, "# Full GitHub docs...") {
				t.Error("full content should not be injected when summary exists")
			}
			if !strings.Contains(c.Content, `read_skill("github")`) {
				t.Error("expected read_skill hint in content")
			}
		}
	}
	if !found {
		t.Error("expected skill candidate in ranked list")
	}
}

func TestAssembleContext_SkillNoSummaryFallback(t *testing.T) {
	skill := skills.Skill{
		Name:    "docker",
		Content: "# Full Docker docs...",
	}
	reg := &stubSkillRegistry{matched: []skills.Skill{skill}}

	msg := channels.InboundMessage{Content: "start a container"}

	var capturedRanked []budget.ContextCandidate
	mockBudget := &capturingBudget{captured: &capturedRanked}

	skillCfg := SkillConfig{
		Registry:         reg,
		Priority:         0.7,
		MaxContextTokens: 2000,
		MaxMatches:       3,
	}

	_, _ = assembleContext(context.Background(), msg, nil, mockBudget, 8000, nil, nil, &skillCfg)

	// Verify full content is injected when no summary exists.
	for _, c := range capturedRanked {
		if c.Role == "skill" {
			if c.Content != "# Full Docker docs..." {
				t.Errorf("expected full content, got %q", c.Content)
			}
		}
	}
}

// Ensure time import is used.
var _ = time.Now
