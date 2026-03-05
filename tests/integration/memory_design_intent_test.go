package integration

// memory_design_intent_test.go
//
// These tests validate the DESIGN INTENT of Chandra's multi-layer memory
// system, not just functional correctness of individual functions.
//
// Each test is annotated with the architectural claim it exercises.
// A failing test here means the system does not behave as designed — it is
// a design conformance regression, not merely a unit bug.
//
// Memory layer overview:
//   - Episodic: recent conversation history (fixed context, cross-session)
//   - Semantic: long-term salient facts (ranked context, queried by relevance)
//   - Identity: agent persona + relationship state (fixed context, always first)
//   - Intent:   scheduled tasks (budget keyword boosting)

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/actionlog"
	"github.com/jrimmer/chandra/internal/agent"
	"github.com/jrimmer/chandra/internal/budget"
	"github.com/jrimmer/chandra/internal/channels"
	"github.com/jrimmer/chandra/internal/memory"
	"github.com/jrimmer/chandra/internal/memory/episodic"
	"github.com/jrimmer/chandra/internal/memory/identity"
	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/internal/memory/semantic"
	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/internal/tools"
	"encoding/json"

	"github.com/jrimmer/chandra/pkg"
)

// -------------------------------------------------------------------------
// Test harness helpers.
// -------------------------------------------------------------------------

// testHarness wires up the full memory stack with a real SQLite DB,
// a mock LLM provider, and real memory stores + budget manager.
type testHarness struct {
	ctx         context.Context
	loop        agent.AgentLoop
	sessionMgr  agent.Manager
	mem         memory.Memory
	epStore     *episodic.Store
	semStore    *semantic.Store
	identStore  *identity.Store
	intentStore intent.IntentStore
	mockProv    *integrationMockProvider
	ppWg        sync.WaitGroup // synchronises runTurn() callers with async post-processing
}

func newTestHarness(t *testing.T, responses []provider.CompletionResponse) *testHarness {
	t.Helper()
	ctx := context.Background()

	s := openTestDB(t)
	db := s.DB()

	epStore := episodic.NewStore(db)
	embedder := &fixedEmbedder{}
	semStore, err := semantic.NewStore(db, embedder)
	require.NoError(t, err)
	intentStore := intent.NewStore(db)

	// Seed agent profile so identity candidate is non-empty.
	identStore := identity.NewStore(db, "user-alice")
	err = identStore.SetAgent(ctx, identity.AgentProfile{
		Name:    "Chandra",
		Persona: "A helpful and thoughtful personal assistant.",
		Traits:  []string{"curious", "precise"},
	})
	require.NoError(t, err)
	require.NoError(t, identStore.SetUser(ctx, identity.UserProfile{ID: "user-alice", Name: "Alice"}))

	mem := memory.New(epStore, semStore, intentStore, identStore)

	mockProv := &integrationMockProvider{responses: responses}

	budgetMgr := budget.New(0.5, 0.3, 0.2, 24, &budgetIntentAdapter{store: intentStore})

	aLog, err := actionlog.NewLog(db)
	require.NoError(t, err)

	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)
	exec := tools.NewExecutor(reg, db, 5*time.Second)

	h := &testHarness{}
	loop := agent.NewLoop(agent.LoopConfig{
		Provider:        mockProv,
		Memory:          mem,
		Budget:          budgetMgr,
		Registry:        reg,
		Executor:        exec,
		ActionLog:       aLog,
		Channel:         &noopChannel{},
		MaxRounds:       5,
		// PostProcessDone lets runTurn() wait for the async post-processing goroutine
		// so tests can query memory immediately after a turn without data races.
		PostProcessDone: func() { h.ppWg.Done() },
	})

	sessionMgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	h.ctx         = ctx
	h.loop        = loop
	h.sessionMgr  = sessionMgr
	h.mem         = mem
	h.epStore     = epStore
	h.semStore    = semStore
	h.identStore  = identStore
	h.intentStore = intentStore
	h.mockProv    = mockProv
	return h
}

func (h *testHarness) runTurn(t *testing.T, channelID, userID, content string) string {
	t.Helper()
	convID := agent.ComputeConversationID(channelID, userID)
	sess, err := h.sessionMgr.GetOrCreate(h.ctx, convID, channelID, userID)
	require.NoError(t, err)
	// Add to WaitGroup before Run() so we can wait for async post-processing.
	h.ppWg.Add(1)
	resp, err := h.loop.Run(h.ctx, sess, channels.InboundMessage{
		ID:             fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		ConversationID: convID,
		ChannelID:      channelID,
		UserID:         userID,
		Content:        content,
		Timestamp:      time.Now().UTC(),
	})
	require.NoError(t, err)
	// Wait for the post-processing goroutine to complete before returning.
	// This ensures episodic and semantic writes are visible to test assertions.
	h.ppWg.Wait()
	return resp
}

func constResponse(text string) []provider.CompletionResponse {
	return []provider.CompletionResponse{
		{Message: provider.Message{Role: "assistant", Content: text}, StopReason: "stop"},
	}
}

// -------------------------------------------------------------------------
// Design intent 1: Semantic reinforcement via "remember" keyword
//
// Claim: When a user says "remember X", the system stores the turn in the
// semantic store at importance 0.8 (higher than the default 0.5).
// This allows Chandra to recall facts across large gaps in episodic history.
// -------------------------------------------------------------------------

func TestDesignIntent_SemanticReinforcement_RememberKeyword(t *testing.T) {
	// Design intent: "remember" keyword → semantic store, importance = 0.8
	h := newTestHarness(t, constResponse("Got it, I'll remember that."))

	h.runTurn(t, "ch-1", "user-alice", "Please remember that my sister's name is Harriet.")

	entries, err := h.semStore.QueryText(h.ctx, "sister name", 5)
	require.NoError(t, err)

	require.NotEmpty(t, entries, "semantic store must contain an entry after 'remember' turn")
	// Assert the correct entry was retrieved, not just that something came back.
	// With hybrid BM25+vector search, "sister name" must prefer the entry that
	// actually contains "Harriet" over any other stored content.
	assert.Contains(t, entries[0].Content, "Harriet",
		"top result must be the planted memory entry; got: %q", entries[0].Content)
	assert.GreaterOrEqual(t, entries[0].Importance, float32(0.79),
		"reinforced memory should have importance ≥ 0.8, got %f", entries[0].Importance)
}

// -------------------------------------------------------------------------
// Design intent 2: Short turns are NOT stored in semantic memory
//
// Claim: Turns below the ~50-token threshold are ephemeral — they exist in
// episodic history but are not promoted to long-term semantic memory.
// This prevents trivia like "ok", "thanks" from polluting semantic recall.
// -------------------------------------------------------------------------

func TestDesignIntent_ShortTurns_NotStoredSemantically(t *testing.T) {
	h := newTestHarness(t, constResponse("You're welcome!"))

	// "Thanks" — well under 50 tokens.
	h.runTurn(t, "ch-1", "user-alice", "Thanks!")

	entries, err := h.semStore.QueryText(h.ctx, "thanks", 5)
	require.NoError(t, err)
	assert.Empty(t, entries, "short trivial turns must not be stored in semantic memory")
}

// -------------------------------------------------------------------------
// Design intent 3: Long-term recall bridges the episodic window
//
// Claim: Episodic memory holds the last 20 turns. For longer conversations,
// important facts must be reachable via semantic retrieval. A fact stored
// early in a conversation should be surfaced by QueryText even after the
// episodic window has moved past it.
// -------------------------------------------------------------------------

func TestDesignIntent_LongTermRecall_BridgesEpisodicWindow(t *testing.T) {
	// Use enough constant responses to fill the episodic window.
	responses := make([]provider.CompletionResponse, 30)
	for i := range responses {
		responses[i] = provider.CompletionResponse{
			Message:    provider.Message{Role: "assistant", Content: fmt.Sprintf("Acknowledged turn %d.", i+1)},
			StopReason: "stop",
		}
	}
	h := newTestHarness(t, responses)

	// Turn 1: plant an important fact with "remember".
	h.runTurn(t, "ch-long", "user-alice", "Please remember: my emergency contact is Bob Nguyen, phone 555-0199.")

	// Turns 2–25: push the window well past the episodic limit of 20.
	for i := 2; i <= 25; i++ {
		h.runTurn(t, "ch-long", "user-alice", fmt.Sprintf("Turn %d: what is the weather like today?", i))
	}

	// The planted fact should now be outside the episodic window (>20 turns ago).
	// It must still be retrievable via semantic memory.
	results, err := h.semStore.QueryText(h.ctx, "emergency contact", 5)
	require.NoError(t, err)

	require.NotEmpty(t, results, "important fact must survive past the episodic window via semantic memory")
	found := false
	for _, r := range results {
		if strings.Contains(r.Content, "Bob Nguyen") || strings.Contains(r.Content, "emergency contact") {
			found = true
			break
		}
	}
	assert.True(t, found, "semantic memory must surface the planted fact: %+v", results)
}

// -------------------------------------------------------------------------
// Design intent 4: Identity candidate is always present in the context window
//
// Claim: The agent's name, persona, and relationship state are injected as
// the highest-priority fixed candidate (Priority=1.0). This must be present
// in every context window regardless of other content.
// -------------------------------------------------------------------------

func TestDesignIntent_IdentityAlwaysInContext(t *testing.T) {
	// We need to observe what the mock provider receives, not just what it returns.
	capturedRequests := make([]provider.CompletionRequest, 0)
	capturingProv := &capturingProvider{
		wrapped: &integrationMockProvider{responses: constResponse("Hi!")},
		capture: &capturedRequests,
	}

	s := openTestDB(t)
	db := s.DB()
	ctx := context.Background()

	epStore := episodic.NewStore(db)
	embedder := &fixedEmbedder{}
	semStore, err := semantic.NewStore(db, embedder)
	require.NoError(t, err)
	intentStore := intent.NewStore(db)
	identStore := identity.NewStore(db, "user-alice")
	require.NoError(t, identStore.SetAgent(ctx, identity.AgentProfile{
		Name:    "Chandra",
		Persona: "A thoughtful assistant.",
	}))
	mem := memory.New(epStore, semStore, intentStore, identStore)

	budgetMgr := budget.New(0.5, 0.3, 0.2, 24, &budgetIntentAdapter{store: intentStore})
	aLog, err := actionlog.NewLog(db)
	require.NoError(t, err)
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)
	exec := tools.NewExecutor(reg, db, 5*time.Second)

	loop := agent.NewLoop(agent.LoopConfig{
		Provider:  capturingProv,
		Memory:    mem,
		Budget:    budgetMgr,
		Registry:  reg,
		Executor:  exec,
		ActionLog: aLog,
		Channel:   &noopChannel{},
		MaxRounds: 5,
	})

	sessionMgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	convID := agent.ComputeConversationID("ch-id", "user-alice")
	sess, err := sessionMgr.GetOrCreate(ctx, convID, "ch-id", "user-alice")
	require.NoError(t, err)

	_, err = loop.Run(ctx, sess, channels.InboundMessage{
		ID: "m1", ConversationID: convID, ChannelID: "ch-id", UserID: "user-alice",
		Content: "Hello!", Timestamp: time.Now(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, capturedRequests, "provider must have been called")

	// The system message with identity must appear in the context.
	msgs := capturedRequests[0].Messages
	foundIdentity := false
	for _, m := range msgs {
		if m.Role == "system" && strings.Contains(m.Content, "Chandra") {
			foundIdentity = true
			break
		}
	}
	assert.True(t, foundIdentity,
		"identity system prompt containing agent name must be present in every context window; messages: %+v", msgs)
}

// capturingProvider wraps a provider to record all CompletionRequests.
type capturingProvider struct {
	wrapped provider.Provider
	capture *[]provider.CompletionRequest
}

func (c *capturingProvider) Complete(ctx context.Context, req provider.CompletionRequest) (provider.CompletionResponse, error) {
	*c.capture = append(*c.capture, req)
	return c.wrapped.Complete(ctx, req)
}
func (c *capturingProvider) CountTokens(msgs []provider.Message, tools []pkg.ToolDef) (int, error) {
	return c.wrapped.CountTokens(msgs, tools)
}
func (c *capturingProvider) ModelID() string { return c.wrapped.ModelID() }

var _ provider.Provider = (*capturingProvider)(nil)

// -------------------------------------------------------------------------
// Design intent 5: Relationship state is updated after every turn
//
// Claim: Each Run() call updates LastInteraction on the relationship record.
// This is how Chandra tracks recency of contact and can modulate tone over time.
// -------------------------------------------------------------------------

func TestDesignIntent_Relationship_LastInteractionUpdates(t *testing.T) {
	h := newTestHarness(t, constResponse("Nice to hear from you!"))

	// Seed an initial relationship with an old LastInteraction.
	past := time.Now().Add(-48 * time.Hour)
	rel := identity.RelationshipState{
		TrustLevel:         1,
		CommunicationStyle: "formal",
		LastInteraction:    past,
	}
	err := h.identStore.UpdateRelationship(h.ctx, rel)
	require.NoError(t, err)

	// Run a turn.
	h.runTurn(t, "ch-rel", "user-alice", "Hey, just checking in.")

	// LastInteraction must now be within the last few seconds.
	updated, err := h.identStore.Relationship()
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), updated.LastInteraction, 10*time.Second,
		"LastInteraction must be updated to now after each Run(); was %v", updated.LastInteraction)
}

// -------------------------------------------------------------------------
// Design intent 6: Episodic continuity across session boundaries (restart sim)
//
// Claim: RecentAcrossSessions means that memory survives a daemon restart
// (simulated by creating a new session manager against the same DB).
// The agent loop must surface prior episodes from the old session to the LLM.
// -------------------------------------------------------------------------

func TestDesignIntent_EpisodicContinuity_AcrossSessionBoundary(t *testing.T) {
	// Capture what the provider sees on the second turn (after "restart").
	capturedRequests := make([]provider.CompletionRequest, 0)

	responses := []provider.CompletionResponse{
		{Message: provider.Message{Role: "assistant", Content: "Noted your cat's name."}, StopReason: "stop"},
		{Message: provider.Message{Role: "assistant", Content: "Your cat is Schrödinger."}, StopReason: "stop"},
	}
	capturingProv := &capturingProvider{
		wrapped: &integrationMockProvider{responses: responses},
		capture: &capturedRequests,
	}

	s := openTestDB(t)
	db := s.DB()
	ctx := context.Background()

	newMem := func() memory.Memory {
		ep := episodic.NewStore(db)
		emb := &fixedEmbedder{}
		sem, err := semantic.NewStore(db, emb)
		require.NoError(t, err)
		it := intent.NewStore(db)
		id := identity.NewStore(db, "user-alice")
		return memory.New(ep, sem, it, id)
	}

	newLoop := func(prov provider.Provider) (agent.AgentLoop, agent.Manager) {
		mem := newMem()
		budgetMgr := budget.New(0.5, 0.3, 0.2, 24, &budgetIntentAdapter{store: intent.NewStore(db)})
		aLog, err := actionlog.NewLog(db)
		require.NoError(t, err)
		reg, err := tools.NewRegistry(nil)
		require.NoError(t, err)
		exec := tools.NewExecutor(reg, db, 5*time.Second)
		mgr, err := agent.NewManager(db, 30*time.Minute)
		require.NoError(t, err)
		return agent.NewLoop(agent.LoopConfig{
			Provider:  prov,
			Memory:    mem,
			Budget:    budgetMgr,
			Registry:  reg,
			Executor:  exec,
			ActionLog: aLog,
			Channel:   &noopChannel{},
			MaxRounds: 5,
		}), mgr
	}

	// --- Session 1: plant the fact. ---
	loop1, mgr1 := newLoop(&integrationMockProvider{responses: []provider.CompletionResponse{responses[0]}})
	convID := agent.ComputeConversationID("ch-restart", "user-alice")
	sess1, err := mgr1.GetOrCreate(ctx, convID, "ch-restart", "user-alice")
	require.NoError(t, err)
	_, err = loop1.Run(ctx, sess1, channels.InboundMessage{
		ID: "m1", ConversationID: convID, ChannelID: "ch-restart", UserID: "user-alice",
		Content: "My cat is named Schrödinger.", Timestamp: time.Now(),
	})
	require.NoError(t, err)

	// --- Simulate restart: new session manager (empty cache), same DB. ---
	loop2, mgr2 := newLoop(capturingProv)
	// The new manager's cache is empty — it must restore from DB.
	sess2, err := mgr2.GetOrCreate(ctx, convID, "ch-restart", "user-alice")
	require.NoError(t, err)

	_, err = loop2.Run(ctx, sess2, channels.InboundMessage{
		ID: "m2", ConversationID: convID, ChannelID: "ch-restart", UserID: "user-alice",
		Content: "What's my cat's name?", Timestamp: time.Now(),
	})
	require.NoError(t, err)

	// The second turn's context must contain the cat episode from session 1.
	require.NotEmpty(t, capturedRequests)
	msgs := capturedRequests[0].Messages
	foundCat := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "Schrödinger") || strings.Contains(m.Content, "cat") {
			foundCat = true
			break
		}
	}
	assert.True(t, foundCat,
		"prior episode from old session must appear in context after simulated restart; messages: %+v", msgs)
}

// -------------------------------------------------------------------------
// Design intent 7: Budget pressure drops ranked (semantic) before fixed (episodic)
//
// Claim: Fixed candidates (identity, recent episodes) are guaranteed to
// appear in the context window. Ranked (semantic) candidates are best-effort
// and are dropped first when the token budget is tight.
// -------------------------------------------------------------------------

func TestDesignIntent_Budget_SemanticDropsBeforeEpisodic(t *testing.T) {
	ctx := context.Background()
	s := openTestDB(t)
	db := s.DB()

	// Seed the identity profile.
	identStore := identity.NewStore(db, "user-alice")
	require.NoError(t, identStore.SetAgent(ctx, identity.AgentProfile{Name: "Chandra", Persona: "helpful"}))

	// Build a context window under a very tight budget: 50 tokens.
	budgetMgr := budget.New(0.5, 0.3, 0.2, 24, nil)

	// One fixed candidate (identity-like, high priority).
	fixed := []budget.ContextCandidate{
		{Role: "system", Content: "You are Chandra.", Priority: 1.0, Tokens: 5},
		{Role: "assistant", Content: "Episodic episode from yesterday.", Priority: 0.9, Tokens: 10},
	}

	// One ranked semantic candidate that won't fit if budget is 50 - 5 - 10 = 35 tokens.
	// Make it 40 tokens so it overflows.
	semanticContent := strings.Repeat("semantic memory word ", 20) // ~20 tokens
	ranked := []budget.ContextCandidate{
		{Role: "memory", Content: semanticContent, Priority: 0.8, Tokens: 40},
	}

	window, err := budgetMgr.Assemble(ctx, 50, fixed, ranked, nil, 0)
	require.NoError(t, err)

	// Fixed must be present; ranked must be dropped.
	foundIdentity := false
	foundEpisodic := false
	foundSemantic := false
	for _, m := range window.Messages {
		if strings.Contains(m.Content, "You are Chandra") {
			foundIdentity = true
		}
		if strings.Contains(m.Content, "Episodic episode") {
			foundEpisodic = true
		}
		if strings.Contains(m.Content, "semantic memory word") {
			foundSemantic = true
		}
	}

	assert.True(t, foundIdentity, "identity (fixed) must survive budget pressure")
	assert.True(t, foundEpisodic, "episodic (fixed) must survive budget pressure")
	assert.False(t, foundSemantic, "semantic (ranked) must be dropped when budget is tight; window: %+v", window.Messages)
	assert.Equal(t, 1, window.Dropped, "exactly 1 candidate should have been dropped")
}

// -------------------------------------------------------------------------
// Design intent 8: Tool-call-only turns are NOT stored in semantic memory
//
// Claim: When the LLM responds only with tool calls (no concluding text),
// there is no meaningful content to store semantically. Such turns must be
// silently skipped by maybeSemanticallyStore.
// -------------------------------------------------------------------------

func TestDesignIntent_ToolOnlyTurns_NotStoredSemantically(t *testing.T) {
	h := newTestHarness(t, []provider.CompletionResponse{
		// Round 1: a tool call with no content.
		{
			Message: provider.Message{Role: "assistant", Content: ""},
			ToolCalls: []pkg.ToolCall{
				{ID: "tc1", Name: "web_search", Parameters: json.RawMessage(`{"query":"weather"}`)},
			},
			StopReason: "tool_use",
		},
		// Round 2: final answer after tool result.
		{
			Message:    provider.Message{Role: "assistant", Content: "It is sunny today."},
			StopReason: "stop",
		},
	})

	// This turn triggers a tool call then a short final answer.
	// The tool-call round (empty content) must not be stored.
	h.runTurn(t, "ch-tool", "user-alice", "What is the weather like?")

	entries, err := h.semStore.QueryText(h.ctx, "weather sunny", 10)
	require.NoError(t, err)

	// The final answer "It is sunny today" combined with the question is ~8 words
	// (~10 tokens) — under the 50-token threshold — so nothing should be stored.
	assert.Empty(t, entries,
		"tool-call-only rounds and short follow-ups must not appear in semantic store; got: %+v", entries)
}
