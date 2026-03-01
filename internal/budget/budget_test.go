package budget_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/budget"
	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/pkg"
)

// staticIntentStore always returns a fixed list of intents.
type staticIntentStore struct {
	intents []budget.Intent
}

func (s *staticIntentStore) Active(_ context.Context) ([]budget.Intent, error) {
	return s.intents, nil
}

// nilIntentStore always returns an empty intent list (simulates no active intents).
type nilIntentStore struct{}

func (n *nilIntentStore) Active(_ context.Context) ([]budget.Intent, error) {
	return nil, nil
}

// makeCandidate is a helper to construct a ContextCandidate.
func makeCandidate(role, content string, priority, importance float32, recency time.Time, tokens int) budget.ContextCandidate {
	return budget.ContextCandidate{
		Role:       role,
		Content:    content,
		Priority:   priority,
		Importance: importance,
		Recency:    recency,
		Tokens:     tokens,
	}
}

func TestBudget_FixedCandidatesAlwaysIncluded(t *testing.T) {
	mgr := budget.New(0.5, 0.3, 0.2, 24, &nilIntentStore{})
	ctx := context.Background()

	now := time.Now()
	fixed := []budget.ContextCandidate{
		makeCandidate("user", "fixed message one", 0.5, 0, now, 50),
		makeCandidate("assistant", "fixed message two", 0.5, 0, now, 50),
	}
	ranked := make([]budget.ContextCandidate, 5)
	for i := range ranked {
		ranked[i] = makeCandidate("user", "ranked content", 0.8, 0, now, 20)
	}

	window, err := mgr.Assemble(ctx, 200, fixed, ranked, nil, 0)
	require.NoError(t, err)

	// Both fixed messages must appear in the output.
	assert.GreaterOrEqual(t, len(window.Messages), 2)
	contents := make(map[string]bool)
	for _, msg := range window.Messages {
		contents[msg.Content] = true
	}
	assert.True(t, contents["fixed message one"], "fixed message one should be present")
	assert.True(t, contents["fixed message two"], "fixed message two should be present")
}

func TestBudget_RankedByScore(t *testing.T) {
	mgr := budget.New(1.0, 0.0, 0.0, 24, &nilIntentStore{})
	ctx := context.Background()

	// With recency and importance weights at zero, ranking is purely by Priority.
	now := time.Now()
	fixed := []budget.ContextCandidate{}
	ranked := []budget.ContextCandidate{
		makeCandidate("user", "high priority", 0.9, 0, now, 30),
		makeCandidate("user", "medium priority", 0.5, 0, now, 30),
		makeCandidate("user", "low priority", 0.1, 0, now, 30),
	}

	// Budget of 70 tokens: fits only 2 candidates (each 30 tokens).
	window, err := mgr.Assemble(ctx, 70, fixed, ranked, nil, 0)
	require.NoError(t, err)

	assert.Equal(t, 1, window.Dropped, "exactly 1 candidate should be dropped")
	assert.Len(t, window.Messages, 2)

	// Verify top 2 are included.
	contents := make(map[string]bool)
	for _, msg := range window.Messages {
		contents[msg.Content] = true
	}
	assert.True(t, contents["high priority"])
	assert.True(t, contents["medium priority"])
	assert.False(t, contents["low priority"])
}

func TestBudget_ScoreFormula(t *testing.T) {
	// With semanticWeight=1.0 and others 0: high similarity wins.
	mgr := budget.New(1.0, 0.0, 0.0, 24, &nilIntentStore{})
	ctx := context.Background()

	now := time.Now()
	fixed := []budget.ContextCandidate{}
	ranked := []budget.ContextCandidate{
		makeCandidate("user", "high similarity", 0.9, 0.5, now.Add(-time.Hour), 20),
		makeCandidate("user", "recent but low similarity", 0.3, 0.5, now, 20),
	}

	// Budget allows both, but check ordering.
	window, err := mgr.Assemble(ctx, 200, fixed, ranked, nil, 0)
	require.NoError(t, err)
	require.Len(t, window.Messages, 2)
	assert.Equal(t, "high similarity", window.Messages[0].Content,
		"highest similarity should appear first when semanticWeight dominates")

	// Now flip: recency dominates.
	mgr2 := budget.New(0.0, 1.0, 0.0, 24, &nilIntentStore{})
	window2, err := mgr2.Assemble(ctx, 200, fixed, ranked, nil, 0)
	require.NoError(t, err)
	require.Len(t, window2.Messages, 2)
	assert.Equal(t, "recent but low similarity", window2.Messages[0].Content,
		"most recent should appear first when recencyWeight dominates")
}

func TestBudget_GracefulDegradation_FixedExceedsBudget(t *testing.T) {
	mgr := budget.New(0.5, 0.3, 0.2, 24, &nilIntentStore{})
	ctx := context.Background()

	now := time.Now()
	fixed := []budget.ContextCandidate{
		makeCandidate("user", "fixed oldest", 0.5, 0, now.Add(-2*time.Hour), 150),
		makeCandidate("user", "fixed newer", 0.5, 0, now.Add(-time.Hour), 100),
		makeCandidate("user", "fixed newest", 0.5, 0, now, 50),
	}

	// Budget of 100 tokens — only the newest fixed candidate fits.
	window, err := mgr.Assemble(ctx, 100, fixed, nil, nil, 0)
	// Must not panic; may return a partial result.
	require.NoError(t, err)
	assert.LessOrEqual(t, window.TotalTokens, 100,
		"TotalTokens must not exceed the budget")
}

func TestBudget_RecencyDecay(t *testing.T) {
	// recencyWeight dominates to ensure recency determines ranking.
	mgr := budget.New(0.0, 1.0, 0.0, 24, &nilIntentStore{})
	ctx := context.Background()

	now := time.Now()
	fixed := []budget.ContextCandidate{}
	ranked := []budget.ContextCandidate{
		makeCandidate("user", "week old memory", 0.5, 0.5, now.Add(-7*24*time.Hour), 20),
		makeCandidate("user", "one hour old memory", 0.5, 0.5, now.Add(-time.Hour), 20),
	}

	window, err := mgr.Assemble(ctx, 200, fixed, ranked, nil, 0)
	require.NoError(t, err)
	require.Len(t, window.Messages, 2)
	assert.Equal(t, "one hour old memory", window.Messages[0].Content,
		"more recent memory should rank higher")
}

func TestBudget_DroppedCount(t *testing.T) {
	mgr := budget.New(1.0, 0.0, 0.0, 24, &nilIntentStore{})
	ctx := context.Background()

	now := time.Now()
	fixed := []budget.ContextCandidate{}
	ranked := make([]budget.ContextCandidate, 5)
	for i := range ranked {
		ranked[i] = makeCandidate("user", "content", 0.5, 0, now, 30)
	}

	// Budget of 70 tokens: 2 candidates fit (each 30 tokens).
	window, err := mgr.Assemble(ctx, 70, fixed, ranked, nil, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, window.Dropped)
	assert.Len(t, window.Messages, 2)
}

func TestBudget_IntentBoost(t *testing.T) {
	intents := []budget.Intent{
		{ID: "i1", Description: "deploy pipeline", Condition: "on_event"},
	}
	mgr := budget.New(1.0, 0.0, 0.0, 24, &staticIntentStore{intents: intents})
	ctx := context.Background()

	now := time.Now()
	fixed := []budget.ContextCandidate{}
	ranked := []budget.ContextCandidate{
		// Same base priority, but this mentions the intent keyword.
		makeCandidate("user", "about the deploy pipeline configuration", 0.5, 0, now, 20),
		// Same priority without the keyword.
		makeCandidate("user", "unrelated conversation topic", 0.5, 0, now, 20),
	}

	// With both candidates at the same base score, the intent boost should
	// push the deploy-pipeline candidate to the top.
	window, err := mgr.Assemble(ctx, 200, fixed, ranked, nil, 0)
	require.NoError(t, err)
	require.Len(t, window.Messages, 2)
	assert.Equal(t, "about the deploy pipeline configuration", window.Messages[0].Content,
		"intent-boosted candidate should rank first")
}

func TestBudget_ToolsRespected(t *testing.T) {
	mgr := budget.New(1.0, 0.0, 0.0, 24, &nilIntentStore{})
	ctx := context.Background()

	now := time.Now()
	fixed := []budget.ContextCandidate{
		makeCandidate("user", "fixed message", 0.5, 0, now, 50),
	}
	tools := []pkg.ToolDef{
		{Name: "test_tool", Description: "does stuff"},
	}

	// toolTokens=80, fixed=50, budget=100 → remaining after tools = 20
	// fixed candidate alone (50 tokens) exceeds remaining (20) → it gets trimmed.
	window, err := mgr.Assemble(ctx, 130, fixed, nil, tools, 80)
	require.NoError(t, err)
	assert.Equal(t, tools, window.Tools)
	// Total tokens must not exceed budget.
	assert.LessOrEqual(t, window.TotalTokens, 130)
}

func TestBudget_ContextWindow_MessageRoles(t *testing.T) {
	mgr := budget.New(0.5, 0.3, 0.2, 24, &nilIntentStore{})
	ctx := context.Background()

	now := time.Now()
	fixed := []budget.ContextCandidate{
		makeCandidate("memory", "remembered fact", 0.5, 0.5, now, 20),
		makeCandidate("identity", "agent persona", 0.5, 0.5, now, 20),
	}

	window, err := mgr.Assemble(ctx, 200, fixed, nil, nil, 0)
	require.NoError(t, err)

	for _, msg := range window.Messages {
		assert.NotEqual(t, "memory", msg.Role,
			"memory candidates must be mapped to a standard role")
		assert.NotEqual(t, "identity", msg.Role,
			"identity candidates must be mapped to a standard role")
		assert.Contains(t, []string{"system", "user", "assistant", "tool"}, msg.Role)
	}

	_ = provider.Message{} // ensure provider import used
}
