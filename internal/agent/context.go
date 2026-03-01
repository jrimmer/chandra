package agent

import (
	"context"
	"log/slog"

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
) (budget.ContextWindow, error) {
	// Step 2: Retrieve semantic memories relevant to the incoming message.
	semanticMems, err := mem.Semantic().QueryText(ctx, msg.Content, 5)
	if err != nil {
		slog.Warn("agent/context: failed to query semantic memory", "error", err)
	}

	// Step 3: Convert memories to ranked candidates and assemble context window.
	ranked := memoriesToCandidates(semanticMems)

	window, err := budgetMgr.Assemble(ctx, tokenBudget, fixed, ranked, nil, 0)
	if err != nil {
		return budget.ContextWindow{}, err
	}

	// prov is reserved for future token-counting integration; not used in v1.
	_ = prov

	return window, nil
}
