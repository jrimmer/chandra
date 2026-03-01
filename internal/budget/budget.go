// Package budget implements the Context Budget Manager (CBM), which selects and
// ranks LLM context candidates to fit within a token budget.
package budget

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/pkg"
)

// ContextCandidate is a ranked unit of context that the CBM can include in a
// context window.
type ContextCandidate struct {
	Role       string    // "system" | "user" | "assistant" | "memory" | "identity"
	Content    string
	Priority   float32   // semantic similarity score, 0.0–1.0
	Importance float32   // memory importance, 0.0–1.0 (0 = use Priority as-is)
	Recency    time.Time // more recent = higher priority
	Tokens     int       // pre-counted
}

// ContextWindow is the output of the CBM assembly step.
type ContextWindow struct {
	Messages    []provider.Message
	Tools       []pkg.ToolDef
	TotalTokens int
	Dropped     int // candidates dropped due to budget exhaustion
}

// Intent is a single active agent intent as returned by IntentStore.
type Intent struct {
	ID          string
	Description string
	Condition   string
}

// IntentStore is the subset of the intent store interface the CBM needs.
type IntentStore interface {
	Active(ctx context.Context) ([]Intent, error)
}

// Manager is the Context Budget Manager. Configure it with New().
type Manager struct {
	semanticWeight    float32
	recencyWeight     float32
	importanceWeight  float32
	recencyDecayHours float32
	intentStore       IntentStore
}

// New creates a Manager with the given scoring weights and intent store.
//
//   - semanticWeight: weight for the semantic similarity (Priority) component.
//   - recencyWeight: weight for the recency decay component.
//   - importanceWeight: weight for the importance component.
//   - recencyDecayHours: hours over which recency decays from 1.0 to 0.0.
//   - intentStore: source of active intents (may be nil — treated as empty).
func New(
	semanticWeight, recencyWeight, importanceWeight, recencyDecayHours float32,
	intentStore IntentStore,
) *Manager {
	return &Manager{
		semanticWeight:    semanticWeight,
		recencyWeight:     recencyWeight,
		importanceWeight:  importanceWeight,
		recencyDecayHours: recencyDecayHours,
		intentStore:       intentStore,
	}
}

// Assemble builds a ContextWindow from fixed and ranked candidates within the
// given token budget.
//
//   - budget: maximum total token count (tools + messages).
//   - fixed: candidates always included (identity, recent messages). Oldest are
//     dropped first if they exceed the budget.
//   - ranked: candidates scored and greedily included until budget is exhausted.
//   - tools: tool definitions to include verbatim.
//   - toolTokens: pre-counted tokens consumed by the tool definitions.
func (m *Manager) Assemble(
	ctx context.Context,
	budget int,
	fixed []ContextCandidate,
	ranked []ContextCandidate,
	tools []pkg.ToolDef,
	toolTokens int,
) (ContextWindow, error) {
	// Step 1: reserve tokens for tools.
	remaining := budget - toolTokens
	if remaining < 0 {
		remaining = 0
	}

	// Step 2 & 3: add fixed candidates, dropping oldest if necessary.
	fixedMsgs, fixedTokens := fitFixed(fixed, remaining)
	remaining -= fixedTokens

	// Step 4: load active intents for keyword boosting.
	var intents []Intent
	if m.intentStore != nil {
		var err error
		intents, err = m.intentStore.Active(ctx)
		if err != nil {
			// Non-fatal: log and continue without intent boosts.
			slog.Warn("budget: failed to load active intents", "error", fmt.Sprintf("%v", err))
		}
	}

	// Step 5 & 6: score and sort ranked candidates.
	// Pre-compute lowercased intent keywords to avoid repeated allocations
	// inside the per-candidate scoring loop.
	intentKeywords := make([]string, len(intents))
	for i, intent := range intents {
		intentKeywords[i] = strings.ToLower(intent.Description)
	}

	now := time.Now()
	scored := make([]scoredCandidate, len(ranked))
	for i, c := range ranked {
		scored[i] = scoredCandidate{
			candidate: c,
			score:     m.computeScore(c, now, intentKeywords),
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		// Ties: more recent first.
		return scored[i].candidate.Recency.After(scored[j].candidate.Recency)
	})

	// Step 7 & 8: greedily include ranked candidates.
	var rankedMsgs []provider.Message
	dropped := 0
	for _, sc := range scored {
		if remaining >= sc.candidate.Tokens {
			rankedMsgs = append(rankedMsgs, candidateToMessage(sc.candidate))
			remaining -= sc.candidate.Tokens
		} else {
			dropped++
		}
	}

	// Step 9: assemble final window.
	allMsgs := append(fixedMsgs, rankedMsgs...) //nolint:gocritic
	totalTokens := budget - remaining

	return ContextWindow{
		Messages:    allMsgs,
		Tools:       tools,
		TotalTokens: totalTokens,
		Dropped:     dropped,
	}, nil
}

// scoredCandidate pairs a ContextCandidate with its computed ranking score.
type scoredCandidate struct {
	candidate ContextCandidate
	score     float32
}

// computeScore computes the weighted ranking score for a candidate.
// intentKeywords is a pre-lowercased slice of intent description strings.
func (m *Manager) computeScore(c ContextCandidate, now time.Time, intentKeywords []string) float32 {
	// Intent boost: if any active intent's description appears in content.
	priority := c.Priority
	lowerContent := strings.ToLower(c.Content)
	for _, kw := range intentKeywords {
		if strings.Contains(lowerContent, kw) {
			priority += 0.1
			if priority > 1.0 {
				priority = 1.0
			}
			break // one boost per candidate
		}
	}

	// Recency score: linear decay from 1.0 to 0.0 over decayHours.
	var recencyScore float32
	if m.recencyDecayHours <= 0 {
		recencyScore = 0
	} else {
		hoursSince := float32(now.Sub(c.Recency).Hours())
		rs := float32(1.0) - (hoursSince / m.recencyDecayHours)
		if rs < 0 {
			rs = 0
		}
		recencyScore = rs
	}

	// Importance: if zero use priority as the stand-in.
	importance := c.Importance
	if importance == 0 {
		importance = c.Priority
	}

	return m.semanticWeight*priority +
		m.recencyWeight*recencyScore +
		m.importanceWeight*importance
}

// fitFixed converts fixed candidates to messages, dropping the oldest ones
// until they fit within the remaining budget.
func fitFixed(fixed []ContextCandidate, remaining int) ([]provider.Message, int) {
	if len(fixed) == 0 {
		return nil, 0
	}

	// Work on a copy so we can shrink it without mutating the caller's slice.
	candidates := make([]ContextCandidate, len(fixed))
	copy(candidates, fixed)

	// Sum tokens.
	total := 0
	for _, c := range candidates {
		total += c.Tokens
	}

	// Drop oldest (lowest Recency) until under budget.
	originalLen := len(candidates)
	for total > remaining && len(candidates) > 0 {
		// Find the index of the oldest candidate.
		oldest := 0
		for i := 1; i < len(candidates); i++ {
			if candidates[i].Recency.Before(candidates[oldest].Recency) {
				oldest = i
			}
		}
		total -= candidates[oldest].Tokens
		candidates = append(candidates[:oldest], candidates[oldest+1:]...)
	}
	if len(candidates) < originalLen {
		slog.Warn("budget: dropped fixed candidates to fit budget",
			"dropped", originalLen-len(candidates),
		)
	}

	msgs := make([]provider.Message, len(candidates))
	for i, c := range candidates {
		msgs[i] = candidateToMessage(c)
	}
	return msgs, total
}

// candidateToMessage converts a ContextCandidate to a provider.Message,
// mapping "memory" to "user" and "identity" to "system".
func candidateToMessage(c ContextCandidate) provider.Message {
	role := c.Role
	switch role {
	case "identity":
		role = "system"
	case "memory":
		role = "user"
	}
	return provider.Message{Role: role, Content: c.Content}
}
