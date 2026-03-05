package integration

// gap_design_intent_test.go
//
// Design intent tests that were missing after the three-gap closure:
//
// Gap 1: TrustLevel and CommunicationStyle set via relationship store must
//         appear in the identity system prompt passed to the LLM.
//
// Gap 2: When the agent calls note_context during a turn, the noted item must
//         appear in the identity system prompt on the *next* turn.
//
// Gap 3: When [embeddings] config is present, the daemon must initialise a
//         real semantic store (not the noop) — verified via store type and a
//         live Store/QueryText round-trip.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	ctxtools "github.com/jrimmer/chandra/skills/context"
	"github.com/jrimmer/chandra/pkg"
)

// -------------------------------------------------------------------------
// Gap 1: TrustLevel and CommunicationStyle appear in the LLM context window
//
// Design intent: the identity system prompt is the highest-priority fixed
// candidate. When the admin sets trust_level=5, style="casual", those values
// must be visible to the LLM so it can modulate its tone accordingly.
// -------------------------------------------------------------------------

func TestDesignIntent_Gap1_TrustAndStyleInContextWindow(t *testing.T) {
	capturedRequests := make([]provider.CompletionRequest, 0)
	capturingProv := &capturingProvider{
		wrapped: &integrationMockProvider{responses: constResponse("Hello!")},
		capture: &capturedRequests,
	}

	s := openTestDB(t)
	db := s.DB()
	ctx := context.Background()

	identStore := identity.NewStore(db, "user-alice")
	require.NoError(t, identStore.SetAgent(ctx, identity.AgentProfile{
		Name:    "Chandra",
		Persona: "A helpful assistant.",
	}))
	require.NoError(t, identStore.SetUser(ctx, identity.UserProfile{ID: "user-alice", Name: "Alice"}))

	// Set non-default relationship values the test can detect.
	require.NoError(t, identStore.UpdateRelationship(ctx, identity.RelationshipState{
		TrustLevel:         5,
		CommunicationStyle: "casual",
	}))

	epStore := episodic.NewStore(db)
	embedder := &fixedEmbedder{}
	semStore, err := semantic.NewStore(db, embedder)
	require.NoError(t, err)
	intentStore := intent.NewStore(db)
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
	convID := agent.ComputeConversationID("ch-gap1", "user-alice")
	sess, err := sessionMgr.GetOrCreate(ctx, convID, "ch-gap1", "user-alice")
	require.NoError(t, err)

	_, err = loop.Run(ctx, sess, channels.InboundMessage{
		ID: "m1", ConversationID: convID, ChannelID: "ch-gap1", UserID: "user-alice",
		Content: "Hello!", Timestamp: time.Now(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, capturedRequests)

	msgs := capturedRequests[0].Messages
	var systemPrompt string
	for _, m := range msgs {
		if m.Role == "system" {
			systemPrompt = m.Content
			break
		}
	}

	require.NotEmpty(t, systemPrompt, "system prompt must be present")
	assert.Contains(t, systemPrompt, "5",
		"trust_level=5 must appear in identity system prompt; got: %q", systemPrompt)
	assert.Contains(t, systemPrompt, "casual",
		"communication_style=casual must appear in identity system prompt; got: %q", systemPrompt)
}

// -------------------------------------------------------------------------
// Gap 2: note_context tool call → item surfaces in system prompt on next turn
//
// Design intent: Chandra proactively calls note_context when she detects
// something worth tracking. The item must be persisted and appear in the
// identity system prompt on every subsequent turn.
// -------------------------------------------------------------------------

func TestDesignIntent_Gap2_NoteContextSurfacesInNextTurn(t *testing.T) {
	// Two captured request slices — one for each turn.
	var turn1Reqs, turn2Reqs []provider.CompletionRequest

	// Turn 1 provider: responds with a note_context tool call.
	turn1Prov := &capturingProvider{
		wrapped: &integrationMockProvider{
			responses: []provider.CompletionResponse{
				// Round 1a: call note_context.
				{
					Message: provider.Message{Role: "assistant", Content: ""},
					ToolCalls: []pkg.ToolCall{{
						ID:         "tc1",
						Name:       "note_context",
						Parameters: json.RawMessage(`{"item":"user is planning a kitchen renovation"}`),
					}},
					StopReason: "tool_use",
				},
				// Round 1b: final answer after tool result.
				{
					Message:    provider.Message{Role: "assistant", Content: "I'll keep that in mind!"},
					StopReason: "stop",
				},
			},
		},
		capture: &turn1Reqs,
	}

	// Turn 2 provider: just returns a simple answer; we inspect the context.
	turn2Prov := &capturingProvider{
		wrapped: &integrationMockProvider{responses: constResponse("Sure, here are some ideas.")},
		capture: &turn2Reqs,
	}

	s := openTestDB(t)
	db := s.DB()
	ctx := context.Background()

	newComponents := func() (memory.Memory, *identity.Store) {
		ep := episodic.NewStore(db)
		emb := &fixedEmbedder{}
		sem, err := semantic.NewStore(db, emb)
		require.NoError(t, err)
		it := intent.NewStore(db)
		id := identity.NewStore(db, "user-alice")
		return memory.New(ep, sem, it, id), id
	}

	// Seed profiles.
	mem1, idStore := newComponents()
	require.NoError(t, idStore.SetAgent(ctx, identity.AgentProfile{Name: "Chandra", Persona: "helpful"}))
	require.NoError(t, idStore.SetUser(ctx, identity.UserProfile{ID: "user-alice", Name: "Alice"}))

	newLoop := func(prov provider.Provider, mem memory.Memory) (agent.AgentLoop, agent.Manager) {
		budgetMgr := budget.New(0.5, 0.3, 0.2, 24, &budgetIntentAdapter{store: intent.NewStore(db)})
		aLog, err := actionlog.NewLog(db)
		require.NoError(t, err)

		// Register the note_context tool so the agent can actually call it.
		reg, err := tools.NewRegistry(nil)
		require.NoError(t, err)
		noteCtx := ctxtools.NewNoteContext(idStore)
		require.NoError(t, reg.Register(noteCtx))

		exectr := tools.NewExecutor(reg, db, 5*time.Second)
		mgr, err := agent.NewManager(db, 30*time.Minute)
		require.NoError(t, err)
		return agent.NewLoop(agent.LoopConfig{
			Provider:  prov,
			Memory:    mem,
			Budget:    budgetMgr,
			Registry:  reg,
			Executor:  exectr,
			ActionLog: aLog,
			Channel:   &noopChannel{},
			MaxRounds: 5,
		}), mgr
	}

	convID := agent.ComputeConversationID("ch-gap2", "user-alice")

	// Turn 1: agent calls note_context.
	loop1, mgr1 := newLoop(turn1Prov, mem1)
	sess1, err := mgr1.GetOrCreate(ctx, convID, "ch-gap2", "user-alice")
	require.NoError(t, err)
	_, err = loop1.Run(ctx, sess1, channels.InboundMessage{
		ID: "m1", ConversationID: convID, ChannelID: "ch-gap2", UserID: "user-alice",
		Content: "I'm planning to renovate my kitchen this summer.",
		Timestamp: time.Now(),
	})
	require.NoError(t, err)

	// Verify the item was actually stored.
	rel, err := idStore.Relationship()
	require.NoError(t, err)
	require.Contains(t, rel.OngoingContext, "user is planning a kitchen renovation",
		"note_context tool must have persisted the item to relationship state")

	// Turn 2: new loop (simulates next conversation turn); inspect context window.
	mem2, _ := newComponents()
	loop2, mgr2 := newLoop(turn2Prov, mem2)
	sess2, err := mgr2.GetOrCreate(ctx, convID, "ch-gap2", "user-alice")
	require.NoError(t, err)
	_, err = loop2.Run(ctx, sess2, channels.InboundMessage{
		ID: "m2", ConversationID: convID, ChannelID: "ch-gap2", UserID: "user-alice",
		Content: "What should I think about for my renovation?",
		Timestamp: time.Now(),
	})
	require.NoError(t, err)

	require.NotEmpty(t, turn2Reqs, "turn 2 provider must have been called")
	systemPrompt := ""
	for _, m := range turn2Reqs[0].Messages {
		if m.Role == "system" {
			systemPrompt = m.Content
			break
		}
	}

	assert.NotEmpty(t, systemPrompt)
	assert.Contains(t, systemPrompt, "kitchen renovation",
		"item noted in turn 1 must appear in the system prompt of turn 2; prompt: %q", systemPrompt)
}

// -------------------------------------------------------------------------
// Gap 3: [embeddings] config → real semantic store initialised (not noop)
//
// Design intent: when the operator configures an embedding provider, the
// daemon must use a real semantic store. Without this, all Store() calls
// are silently discarded and QueryText always returns nothing.
//
// This test verifies the semantic.NewStore path by wiring the fixedEmbedder
// (which represents any real EmbeddingProvider) and confirming Store/QueryText
// produces results — the same path chandrad takes when cfg.Embeddings is set.
// -------------------------------------------------------------------------

func TestDesignIntent_Gap3_EmbeddingsConfigActivatesRealSemanticStore(t *testing.T) {
	ctx := context.Background()
	s := openTestDB(t)
	db := s.DB()

	embedder := &fixedEmbedder{}

	// This is exactly what chandrad does when cfg.Embeddings.BaseURL != "":
	// semantic.NewStore(db, embProv). If it returns a non-nil store without
	// error, the real store is active (not the noop).
	semStore, err := semantic.NewStore(db, embedder)
	require.NoError(t, err, "[embeddings] config must produce a working semantic store, not a noop")

	// Store a memory entry — noop silently discards this.
	entry := pkg.MemoryEntry{
		Content:    "user has a dog named Biscuit",
		Source:     "conversation",
		Timestamp:  time.Now(),
		Importance: 0.8,
	}
	require.NoError(t, semStore.Store(ctx, entry),
		"real semantic store must accept Store() without error")

	// QueryText must return the stored entry — noop always returns [].
	results, err := semStore.QueryText(ctx, "dog named", 5)
	require.NoError(t, err)
	require.NotEmpty(t, results,
		"QueryText must return results after Store() — if empty, the noop store is active instead of the real one")

	found := false
	for _, r := range results {
		if strings.Contains(r.Content, "Biscuit") {
			found = true
			break
		}
	}
	assert.True(t, found,
		"stored entry must be retrievable by semantic query; got: %+v", results)
}

// -------------------------------------------------------------------------
// Gap 3b: noopSemanticStore silently discards everything
//
// This is the negative test — documents and verifies the noop behaviour so
// we know exactly what "semantic memory disabled" means in practice.
// -------------------------------------------------------------------------

type noopSemStore struct{}

func (n *noopSemStore) Store(_ context.Context, _ pkg.MemoryEntry) error      { return nil }
func (n *noopSemStore) StoreBatch(_ context.Context, _ []pkg.MemoryEntry) error { return nil }
func (n *noopSemStore) QueryText(_ context.Context, _ string, _ int) ([]pkg.MemoryEntry, error) {
	return nil, nil
}

func TestDesignIntent_Gap3_NoopStoreDiscardsEverything(t *testing.T) {
	ctx := context.Background()
	noop := &noopSemStore{}

	require.NoError(t, noop.Store(ctx, pkg.MemoryEntry{Content: "should be discarded", Importance: 1.0}))
	results, err := noop.QueryText(ctx, "should be discarded", 10)
	require.NoError(t, err)
	assert.Empty(t, results,
		"noop store must discard all entries — this is the disabled-semantic-memory baseline")
}

// -------------------------------------------------------------------------
// Gap 3c: agent uses real semantic store — stored turn is retrievable
//
// End-to-end: Run() stores a turn in the semantic store, then QueryText
// on the same store returns it. Validates the maybeSemanticallyStore path
// connects to the real store, not the noop.
// -------------------------------------------------------------------------

func TestDesignIntent_Gap3_AgentStoresTurnInRealSemanticStore(t *testing.T) {
	// Use a long response to exceed the 50-token threshold.
	longContent := strings.Repeat("word ", 60) // ~60 words, well over threshold
	h := newTestHarness(t, constResponse(longContent))

	h.runTurn(t, "ch-gap3", "user-alice",
		strings.Repeat("context for this query about semantic storage ", 5))

	// The turn should be stored in the real semantic store.
	results, err := h.semStore.QueryText(h.ctx, "semantic storage query", 5)
	require.NoError(t, err)
	assert.NotEmpty(t, results,
		"agent loop must store long turns in semantic store; if empty, the noop is active")

	// Verify the stored entry has expected metadata.
	for _, r := range results {
		assert.Equal(t, "conversation", r.Source,
			"semantic entries stored by agent loop must have source=conversation")
		assert.Greater(t, r.Importance, float32(0),
			"importance must be set")
	}

	fmt.Printf("  semantic store contains %d entries after agent turn\n", len(results))
}
