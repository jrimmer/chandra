// Package integration contains end-to-end integration tests that wire up real
// components (SQLite, memory stores, action log) with lightweight mock
// implementations of the LLM provider and embedding provider.
package integration

import (
	"context"
	"testing"
	"time"

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
	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
)

// -------------------------------------------------------------------------
// Mock provider: returns preset responses.
// -------------------------------------------------------------------------

type integrationMockProvider struct {
	responses []provider.CompletionResponse
	idx       int
}

func (m *integrationMockProvider) Complete(_ context.Context, _ provider.CompletionRequest) (provider.CompletionResponse, error) {
	if m.idx >= len(m.responses) {
		return m.responses[len(m.responses)-1], nil
	}
	r := m.responses[m.idx]
	m.idx++
	return r, nil
}

func (m *integrationMockProvider) CountTokens(_ []provider.Message, _ []pkg.ToolDef) (int, error) {
	return 10, nil
}

func (m *integrationMockProvider) ModelID() string { return "mock-integration" }

var _ provider.Provider = (*integrationMockProvider)(nil)

// -------------------------------------------------------------------------
// Mock embedding provider: returns a fixed non-zero 1536-dim vector.
// -------------------------------------------------------------------------

const embDim = 1536

type fixedEmbedder struct{}

func (f *fixedEmbedder) Embed(_ context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	embs := make([][]float32, len(req.Texts))
	for i := range embs {
		vec := make([]float32, embDim)
		for j := range vec {
			vec[j] = 0.1 // fixed non-zero value
		}
		embs[i] = vec
	}
	return provider.EmbeddingResponse{
		Embeddings: embs,
		Model:      "mock-embedder",
		Dimensions: embDim,
	}, nil
}

func (f *fixedEmbedder) Dimensions() int { return embDim }

var _ provider.EmbeddingProvider = (*fixedEmbedder)(nil)

// -------------------------------------------------------------------------
// Minimal mock channel and executor.
// -------------------------------------------------------------------------

type noopChannel struct{}

func (c *noopChannel) ID() string                                                              { return "noop" }
func (c *noopChannel) Listen(_ context.Context, _ chan<- channels.InboundMessage) error       { return nil }
func (c *noopChannel) Send(_ context.Context, _ channels.OutboundMessage) error               { return nil }
func (c *noopChannel) React(_ context.Context, _, _ string) error                             { return nil }
func (c *noopChannel) SendCheckpoint(_ context.Context, _, _ string) error                    { return nil }

var _ channels.Channel = (*noopChannel)(nil)

type noopExecutor struct{}

func (e *noopExecutor) Execute(_ context.Context, calls []pkg.ToolCall) []pkg.ToolResult {
	res := make([]pkg.ToolResult, len(calls))
	for i, c := range calls {
		res[i] = pkg.ToolResult{ID: c.ID, Content: "ok"}
	}
	return res
}

var _ tools.Executor = (*noopExecutor)(nil)

type noopRegistry struct{}

func (r *noopRegistry) Register(_ pkg.Tool) error                                          { return nil }
func (r *noopRegistry) Get(_ string) (pkg.Tool, bool)                                      { return nil, false }
func (r *noopRegistry) All() []pkg.ToolDef                                                 { return nil }
func (r *noopRegistry) EnforceCapabilities(_ pkg.ToolCall) error                           { return nil }
func (r *noopRegistry) RequiresConfirmation(_ pkg.ToolCall) (bool, tools.ConfirmationRule) {
	return false, tools.ConfirmationRule{}
}
func (r *noopRegistry) AddTierOverride(_ string, _ pkg.ToolTier) {}

var _ tools.Registry = (*noopRegistry)(nil)

// -------------------------------------------------------------------------
// Intent store adapter: wraps intent.IntentStore to satisfy budget.IntentStore.
// -------------------------------------------------------------------------

type budgetIntentAdapter struct {
	store intent.IntentStore
}

func (a *budgetIntentAdapter) Active(ctx context.Context) ([]budget.Intent, error) {
	items, err := a.store.Active(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]budget.Intent, len(items))
	for i, it := range items {
		out[i] = budget.Intent{
			ID:          it.ID,
			Description: it.Description,
			Condition:   it.Condition,
		}
	}
	return out, nil
}

// -------------------------------------------------------------------------
// openTestDB opens a temporary on-disk SQLite DB with migrations applied.
// Using a named temp file rather than :memory: because the WAL-mode journal
// check in NewDB does not work with in-memory URIs.
// -------------------------------------------------------------------------

func openTestDB(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.NewDB(dir + "/test.db")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// -------------------------------------------------------------------------
// TestIntegration_FullAgentLoop
// -------------------------------------------------------------------------

func TestIntegration_FullAgentLoop(t *testing.T) {
	ctx := context.Background()

	// 1. Real SQLite DB with migrations.
	s := openTestDB(t)
	db := s.DB()

	// 2. Boot all four memory stores.
	epStore := episodic.NewStore(db)
	embedder := &fixedEmbedder{}
	semStore, err := semantic.NewStore(db, embedder)
	if err != nil {
		t.Fatalf("semantic.NewStore: %v", err)
	}
	intentStore := intent.NewStore(db)
	identityStore := identity.NewStore(db, "user-integration")
	mem := memory.New(epStore, semStore, intentStore, identityStore)

	// 3. Mock provider: returns "Hello from Chandra!" with no tool calls.
	mockProv := &integrationMockProvider{
		responses: []provider.CompletionResponse{
			{
				Message:    provider.Message{Role: "assistant", Content: "Hello from Chandra!"},
				StopReason: "stop",
			},
		},
	}

	// 4. Budget manager (real).
	budgetMgr := budget.New(0.5, 0.3, 0.2, 24, &budgetIntentAdapter{store: intentStore})

	// 5. Action log (real).
	aLog, err := actionlog.NewLog(db)
	if err != nil {
		t.Fatalf("actionlog.NewLog: %v", err)
	}

	// 6. Tool registry and executor (real registry, no tools registered).
	reg, err := tools.NewRegistry(nil)
	if err != nil {
		t.Fatalf("tools.NewRegistry: %v", err)
	}
	exec := tools.NewExecutor(reg, db, 5*time.Second)

	// 7. Assemble agent loop config.
	cfg := agent.LoopConfig{
		Provider:  mockProv,
		Memory:    mem,
		Budget:    budgetMgr,
		Registry:  reg,
		Executor:  exec,
		ActionLog: aLog,
		Channel:   &noopChannel{},
		MaxRounds: 5,
	}
	loop := agent.NewLoop(cfg)

	// 8. Create a session using the session manager so it's persisted in DB
	// (the episodes table has a FK to sessions.id).
	sessionMgr, err := agent.NewManager(db, 30*time.Minute)
	if err != nil {
		t.Fatalf("agent.NewManager: %v", err)
	}
	convID := agent.ComputeConversationID("integ-chan-001", "user-integration")
	session, err := sessionMgr.GetOrCreate(ctx, convID, "integ-chan-001", "user-integration")
	if err != nil {
		t.Fatalf("sessionMgr.GetOrCreate: %v", err)
	}

	inbound := channels.InboundMessage{
		ID:             "integ-msg-001",
		ConversationID: session.ConversationID,
		ChannelID:      session.ChannelID,
		UserID:         session.UserID,
		Content:        "Hello, Chandra!",
	}

	// 9. Run the agent loop.
	resp, err := loop.Run(ctx, session, inbound)
	if err != nil {
		t.Fatalf("loop.Run: %v", err)
	}

	// 10. Assert response.
	if resp != "Hello from Chandra!" {
		t.Errorf("expected response %q, got %q", "Hello from Chandra!", resp)
	}

	// 11. Assert episodic store has 2 entries (user + assistant).
	eps, err := epStore.Recent(ctx, session.ID, 10)
	if err != nil {
		t.Fatalf("epStore.Recent: %v", err)
	}
	if len(eps) != 2 {
		t.Errorf("expected 2 episodic entries, got %d", len(eps))
	}

	// 12. Assert action log has at least 1 entry (message_sent).
	actions, err := aLog.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("aLog.Recent: %v", err)
	}
	if len(actions) < 1 {
		t.Errorf("expected at least 1 action log entry, got %d", len(actions))
	}
	foundMessageSent := false
	for _, a := range actions {
		if a.Type == actionlog.ActionMessageSent {
			foundMessageSent = true
			break
		}
	}
	if !foundMessageSent {
		t.Errorf("expected action_log to contain a message_sent entry, entries: %+v", actions)
	}
}
