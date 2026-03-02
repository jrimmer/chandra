package planner

import (
	"context"
	"fmt"
	"log/slog"
)

// SkillGenerator is the interface for generating skills from gaps.
type SkillGenerator interface {
	Generate(ctx context.Context, command, description string) error
}

// ConfirmFunc asks the user for confirmation before generating a skill.
// Returns true if confirmed, false if rejected.
type ConfirmFunc func(ctx context.Context, description string) (bool, error)

// GapResolver bridges Planner.IdentifyGaps to SkillGenerator.Generate.
// When the planner finds a missing skill gap, the resolver triggers
// generation with Tier 4 confirmation before proceeding.
type GapResolver struct {
	Planner   PlannerInterface
	Generator SkillGenerator
	Confirm   ConfirmFunc
}

// ResolveGaps identifies capability gaps in a plan and triggers skill
// generation for each missing skill. Returns the list of unresolved gaps
// (those where generation was declined or failed).
func (r *GapResolver) ResolveGaps(ctx context.Context, plan *ExecutionPlan) ([]Gap, error) {
	gaps, err := r.Planner.IdentifyGaps(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("identify gaps: %w", err)
	}

	if len(gaps) == 0 {
		return nil, nil
	}

	var unresolved []Gap
	for _, gap := range gaps {
		if gap.Type != "skill" {
			unresolved = append(unresolved, gap)
			continue
		}

		// Tier 4: require confirmation before generating.
		if r.Confirm != nil {
			desc := fmt.Sprintf("Generate skill %q to resolve capability gap (resolution: %s)", gap.Name, gap.Resolution)
			confirmed, confirmErr := r.Confirm(ctx, desc)
			if confirmErr != nil {
				slog.Warn("gap resolver: confirmation failed", "skill", gap.Name, "err", confirmErr)
				unresolved = append(unresolved, gap)
				continue
			}
			if !confirmed {
				slog.Info("gap resolver: generation declined", "skill", gap.Name)
				unresolved = append(unresolved, gap)
				continue
			}
		}

		// Generate the missing skill.
		if r.Generator != nil {
			genErr := r.Generator.Generate(ctx, gap.Name, fmt.Sprintf("Auto-generated to resolve capability gap: %s", gap.Name))
			if genErr != nil {
				slog.Warn("gap resolver: generation failed", "skill", gap.Name, "err", genErr)
				unresolved = append(unresolved, gap)
				continue
			}
			slog.Info("gap resolver: skill generated", "skill", gap.Name)
		} else {
			unresolved = append(unresolved, gap)
		}
	}

	return unresolved, nil
}
