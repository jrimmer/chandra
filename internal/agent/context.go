package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/jrimmer/chandra/internal/budget"
	"github.com/jrimmer/chandra/internal/channels"
	"github.com/jrimmer/chandra/internal/memory"
	"github.com/jrimmer/chandra/internal/provider"
)

// assembleContext retrieves relevant memories and builds a ContextWindow
// for the given session and message.
//
// Steps:
//  1. Query semantic store for top-5 memories matching the message content
//  2. Convert memories to ContextCandidates
//  3. Call CBM.Assemble with the fixed candidates (identity, recent episodes)
//     and the ranked semantic candidates
func assembleContext(
	ctx context.Context,
	msg channels.InboundMessage,
	mem memory.Memory,
	budgetMgr ContextBudget,
	tokenBudget int,
	fixed []budget.ContextCandidate,
	prov provider.Provider,
	skillCfg *SkillConfig,
) (budget.ContextWindow, error) {
	// Step 2: Retrieve semantic memories relevant to the incoming message.
	var ranked []budget.ContextCandidate
	if mem != nil {
		semanticMems, err := mem.Semantic().QueryText(ctx, msg.Content, 5)
		if err != nil {
			slog.Warn("agent/context: failed to query semantic memory", "error", err)
		}

		// Step 3: Convert memories to ranked candidates.
		ranked = memoriesToCandidates(semanticMems)
	}

	// Inject matched skills as ranked candidates.
	if skillCfg != nil && skillCfg.Registry != nil {
		matched := skillCfg.Registry.Match(msg.Content)

		maxMatch := skillCfg.MaxMatches
		if maxMatch > 0 && len(matched) > maxMatch {
			matched = matched[:maxMatch]
		}

		tokenBudgetForSkills := skillCfg.MaxContextTokens
		usedTokens := 0
		for _, sk := range matched {
			// Rough estimate: 1 token ~= 4 chars.
			tokens := len(sk.Content) / 4
			if tokenBudgetForSkills > 0 && usedTokens+tokens > tokenBudgetForSkills {
				break
			}
			ranked = append(ranked, budget.ContextCandidate{
				Role:     "skill",
				Content:  sk.Content,
				Priority: float32(skillCfg.Priority),
				Recency:  time.Now(),
				Tokens:   tokens,
			})
			usedTokens += tokens
		}
	}

	window, err := budgetMgr.Assemble(ctx, tokenBudget, fixed, ranked, nil, 0)
	if err != nil {
		return budget.ContextWindow{}, err
	}

	// prov is reserved for future token-counting integration; not used in v1.
	_ = prov

	return window, nil
}
