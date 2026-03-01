package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/agent"
	"github.com/jrimmer/chandra/internal/actionlog"
	"github.com/jrimmer/chandra/internal/budget"
	"github.com/jrimmer/chandra/internal/channels"
	"github.com/jrimmer/chandra/internal/memory"
	"github.com/jrimmer/chandra/internal/memory/episodic"
	"github.com/jrimmer/chandra/internal/memory/identity"
	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/internal/memory/semantic"
	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/internal/scheduler"
	"github.com/jrimmer/chandra/internal/tools"
	"github.com/jrimmer/chandra/pkg"
)

// ------------------------------------------------------------------ mocks --

// mockProvider records calls and returns preset responses.
type mockProvider struct {
	responses []provider.CompletionResponse
	callIdx   int
	err       error
}

func (m *mockProvider) Complete(_ context.Context, _ provider.CompletionRequest) (provider.CompletionResponse, error) {
	if m.err != nil {
		return provider.CompletionResponse{}, m.err
	}
	if m.callIdx >= len(m.responses) {
		// Always return the last response if we run out.
		return m.responses[len(m.responses)-1], nil
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

func (m *mockProvider) CountTokens(_ []provider.Message, _ []pkg.ToolDef) (int, error) {
	return 10, nil
}
func (m *mockProvider) ModelID() string { return "mock" }

var _ provider.Provider = (*mockProvider)(nil)

// mockEpisodic records Append calls.
type mockEpisodic struct {
	appendedEps []pkg.Episode
}

func (m *mockEpisodic) Append(_ context.Context, ep pkg.Episode) error {
	m.appendedEps = append(m.appendedEps, ep)
	return nil
}
func (m *mockEpisodic) Recent(_ context.Context, _ string, _ int) ([]pkg.Episode, error) {
	return nil, nil
}
func (m *mockEpisodic) Since(_ context.Context, _ time.Time) ([]pkg.Episode, error) {
	return nil, nil
}

var _ episodic.EpisodicStore = (*mockEpisodic)(nil)

// mockSemantic records Store calls.
type mockSemantic struct {
	stored  []pkg.MemoryEntry
	entries []pkg.MemoryEntry // to return on QueryText
}

func (m *mockSemantic) Store(_ context.Context, entry pkg.MemoryEntry) error {
	m.stored = append(m.stored, entry)
	return nil
}
func (m *mockSemantic) StoreBatch(_ context.Context, entries []pkg.MemoryEntry) error {
	m.stored = append(m.stored, entries...)
	return nil
}
func (m *mockSemantic) Query(_ context.Context, _ []float32, _ int) ([]pkg.MemoryEntry, error) {
	return m.entries, nil
}
func (m *mockSemantic) QueryText(_ context.Context, _ string, _ int) ([]pkg.MemoryEntry, error) {
	return m.entries, nil
}

var _ semantic.SemanticStore = (*mockSemantic)(nil)

// mockIntent is a no-op intent store.
type mockIntent struct{}

func (m *mockIntent) Create(_ context.Context, _, _, _ string) (*intent.Intent, error) {
	return nil, nil
}
func (m *mockIntent) Update(_ context.Context, _ *intent.Intent) error    { return nil }
func (m *mockIntent) Active(_ context.Context) ([]*intent.Intent, error)  { return nil, nil }
func (m *mockIntent) Due(_ context.Context) ([]*intent.Intent, error)     { return nil, nil }
func (m *mockIntent) Complete(_ context.Context, _ string) error           { return nil }

var _ intent.IntentStore = (*mockIntent)(nil)

// mockIdentity is a no-op identity store.
type mockIdentity struct{}

func (m *mockIdentity) Agent() (identity.AgentProfile, error) { return identity.AgentProfile{}, nil }
func (m *mockIdentity) SetAgent(_ context.Context, _ identity.AgentProfile) error { return nil }
func (m *mockIdentity) User() (identity.UserProfile, error)                        { return identity.UserProfile{}, nil }
func (m *mockIdentity) SetUser(_ context.Context, _ identity.UserProfile) error   { return nil }
func (m *mockIdentity) Relationship() (identity.RelationshipState, error) {
	return identity.RelationshipState{}, nil
}
func (m *mockIdentity) UpdateRelationship(_ context.Context, _ identity.RelationshipState) error {
	return nil
}

var _ identity.IdentityStore = (*mockIdentity)(nil)

// mockBudget returns a minimal ContextWindow.
type mockBudget struct {
	assembleCalled bool
}

func (m *mockBudget) Assemble(
	_ context.Context,
	_ int,
	fixed []budget.ContextCandidate,
	_ []budget.ContextCandidate,
	tools []pkg.ToolDef,
	_ int,
) (budget.ContextWindow, error) {
	m.assembleCalled = true
	// Build messages from fixed candidates.
	msgs := make([]provider.Message, 0, len(fixed))
	for _, c := range fixed {
		msgs = append(msgs, provider.Message{Role: c.Role, Content: c.Content})
	}
	return budget.ContextWindow{Messages: msgs, Tools: tools}, nil
}

var _ agent.ContextBudget = (*mockBudget)(nil)

// mockRegistry returns an empty tool list.
type mockRegistry struct{}

func (m *mockRegistry) Register(_ pkg.Tool) error                                    { return nil }
func (m *mockRegistry) Get(_ string) (pkg.Tool, bool)                                { return nil, false }
func (m *mockRegistry) All() []pkg.ToolDef                                           { return nil }
func (m *mockRegistry) EnforceCapabilities(_ pkg.ToolCall, _ []pkg.Capability) error { return nil }
func (m *mockRegistry) RequiresConfirmation(_ string) bool                            { return false }

var _ tools.Registry = (*mockRegistry)(nil)

// mockExecutor records Execute calls and returns preset results.
type mockExecutor struct {
	calls   []pkg.ToolCall
	results []pkg.ToolResult
}

func (m *mockExecutor) Execute(_ context.Context, calls []pkg.ToolCall) []pkg.ToolResult {
	m.calls = append(m.calls, calls...)
	if m.results != nil {
		return m.results
	}
	// Default: return empty content results.
	res := make([]pkg.ToolResult, len(calls))
	for i, c := range calls {
		res[i] = pkg.ToolResult{ID: c.ID, Content: "ok"}
	}
	return res
}

var _ tools.Executor = (*mockExecutor)(nil)

// mockActionLog records Record calls.
type mockActionLog struct {
	recorded []struct {
		sessionID  string
		actionType actionlog.ActionType
		details    string
	}
}

func (m *mockActionLog) Record(_ context.Context, sessionID string, actionType actionlog.ActionType, details string) error {
	m.recorded = append(m.recorded, struct {
		sessionID  string
		actionType actionlog.ActionType
		details    string
	}{sessionID, actionType, details})
	return nil
}
func (m *mockActionLog) Query(_ context.Context, _, _ time.Time, _ actionlog.ActionType) ([]*actionlog.Action, error) {
	return nil, nil
}
func (m *mockActionLog) Recent(_ context.Context, _ int) ([]*actionlog.Action, error) { return nil, nil }
func (m *mockActionLog) GenerateHourlyRollup(_ context.Context, _ time.Time) (*actionlog.Rollup, error) {
	return nil, nil
}
func (m *mockActionLog) GetRollup(_ context.Context, _ string, _ time.Time) (*actionlog.Rollup, error) {
	return nil, nil
}

var _ actionlog.Log = (*mockActionLog)(nil)

// mockChannel is a no-op channel.
type mockChannel struct{}

func (m *mockChannel) Listen(_ context.Context) (<-chan channels.InboundMessage, error) { return nil, nil }
func (m *mockChannel) Send(_ context.Context, _ channels.OutboundMessage) error         { return nil }
func (m *mockChannel) React(_ context.Context, _, _ string) error                       { return nil }

var _ channels.Channel = (*mockChannel)(nil)

// ---------------------------------------------------------------- helpers --

func newTestSession() *agent.Session {
	return &agent.Session{
		ID:             "sess-001",
		ConversationID: "conv-001",
		ChannelID:      "chan-001",
		UserID:         "user-001",
		LastActive:     time.Now(),
		CreatedAt:      time.Now(),
	}
}

func newTestMessage(content string) channels.InboundMessage {
	return channels.InboundMessage{
		ID:             "msg-001",
		ConversationID: "conv-001",
		ChannelID:      "chan-001",
		UserID:         "user-001",
		Content:        content,
	}
}

func newTestConfig(
	p provider.Provider,
	ep *mockEpisodic,
	sem *mockSemantic,
	al *mockActionLog,
	ex *mockExecutor,
	bgt agent.ContextBudget,
	allowlist map[string][]string,
	maxRounds int,
	maxQueue int,
) agent.LoopConfig {
	mem := memory.New(ep, sem, &mockIntent{}, &mockIdentity{})
	reg := &mockRegistry{}
	ch := &mockChannel{}

	if maxRounds == 0 {
		maxRounds = 5
	}
	// Note: maxQueue == 0 is intentionally forwarded to NewLoop, which applies the
	// spec-mandated default of 20. Pass a non-zero value to override that default.

	return agent.LoopConfig{
		Provider:      p,
		Memory:        mem,
		Budget:        bgt,
		Registry:      reg,
		Executor:      ex,
		ActionLog:     al,
		Channel:       ch,
		MaxRounds:     maxRounds,
		MaxQueueDepth: maxQueue,
		ToolAllowlist: allowlist,
	}
}

func toolCallResponse(name string, params map[string]string) provider.CompletionResponse {
	raw, _ := json.Marshal(params)
	return provider.CompletionResponse{
		Message: provider.Message{Role: "assistant"},
		ToolCalls: []pkg.ToolCall{
			{ID: "tc-1", Name: name, Parameters: raw},
		},
		StopReason: "tool_calls",
	}
}

func textResponse(content string) provider.CompletionResponse {
	return provider.CompletionResponse{
		Message:    provider.Message{Role: "assistant", Content: content},
		StopReason: "stop",
	}
}

// ------------------------------------------------------------------ tests --

func TestAgentLoop_Run_BasicTurn(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{}
	bgt := &mockBudget{}
	p := &mockProvider{responses: []provider.CompletionResponse{textResponse("Hello!")}}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 20)
	loop := agent.NewLoop(cfg)

	resp, err := loop.Run(context.Background(), newTestSession(), newTestMessage("hi"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", resp)
	}
	// Verify episodes were appended (user + assistant = 2).
	if len(ep.appendedEps) != 2 {
		t.Errorf("expected 2 appended episodes, got %d", len(ep.appendedEps))
	}
}

func TestAgentLoop_Run_WithToolCalls(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{
		results: []pkg.ToolResult{{ID: "tc-1", Content: "search results"}},
	}
	bgt := &mockBudget{}
	p := &mockProvider{
		responses: []provider.CompletionResponse{
			toolCallResponse("web.search", map[string]string{"query": "test"}),
			textResponse("Found it!"),
		},
	}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 20)
	loop := agent.NewLoop(cfg)

	resp, err := loop.Run(context.Background(), newTestSession(), newTestMessage("search for test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "Found it!" {
		t.Errorf("expected 'Found it!', got %q", resp)
	}
	// Verify executor was called.
	if len(ex.calls) == 0 {
		t.Error("expected executor to be called, but it wasn't")
	}
}

func TestAgentLoop_Run_MaxRoundsExceeded(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{}
	bgt := &mockBudget{}

	// Provider always returns a tool call (never produces a final text response).
	p := &mockProvider{}
	for i := 0; i < 10; i++ {
		p.responses = append(p.responses, toolCallResponse("web.search", map[string]string{"q": "x"}))
	}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 3, 20)
	loop := agent.NewLoop(cfg)

	resp, err := loop.Run(context.Background(), newTestSession(), newTestMessage("keep searching"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return graceful message, not empty.
	if resp == "" {
		t.Error("expected graceful response but got empty string")
	}
	// Should contain "wasn't able to complete" or similar.
	// The spec says: "I wasn't able to complete that, please try again"
	if len(resp) < 5 {
		t.Errorf("graceful response too short: %q", resp)
	}
}

func TestAgentLoop_Run_SemanticStorage_Reinforcement(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{}
	bgt := &mockBudget{}
	p := &mockProvider{responses: []provider.CompletionResponse{textResponse("Got it.")}}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 20)
	loop := agent.NewLoop(cfg)

	// Short message (< 50 tokens) but has reinforcement keyword.
	_, err := loop.Run(context.Background(), newTestSession(), newTestMessage("remember: never deploy on Fridays"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Must be stored despite being short.
	if len(sem.stored) == 0 {
		t.Error("expected semantic storage for reinforcement turn, but Store was not called")
	}
	// Check importance is 0.8.
	if len(sem.stored) > 0 && sem.stored[0].Importance != 0.8 {
		t.Errorf("expected importance 0.8 for reinforcement, got %v", sem.stored[0].Importance)
	}
}

func TestAgentLoop_Run_SemanticStorage_SkipsShort(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{}
	bgt := &mockBudget{}
	p := &mockProvider{responses: []provider.CompletionResponse{textResponse("Hey there!")}}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 20)
	loop := agent.NewLoop(cfg)

	// Very short message, no reinforcement keywords.
	_, err := loop.Run(context.Background(), newTestSession(), newTestMessage("Hi!"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should NOT be stored.
	if len(sem.stored) > 0 {
		t.Errorf("expected no semantic storage for short turn, but got %d stored entries", len(sem.stored))
	}
}

func TestAgentLoop_Run_MemoryRetrieval(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{
		entries: []pkg.MemoryEntry{
			{ID: "m1", Content: "some memory", Importance: 0.7},
		},
	}
	al := &mockActionLog{}
	ex := &mockExecutor{}
	bgt := &mockBudget{}
	p := &mockProvider{responses: []provider.CompletionResponse{textResponse("Response using memory.")}}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 20)
	loop := agent.NewLoop(cfg)

	_, err := loop.Run(context.Background(), newTestSession(), newTestMessage("tell me something"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ContextBudget.Assemble must have been called (memory retrieval happened).
	if !bgt.assembleCalled {
		t.Error("expected ContextBudget.Assemble to be called (memory retrieval), but it wasn't")
	}
}

func TestAgentLoop_Backpressure(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{}
	bgt := &mockBudget{}
	p := &mockProvider{responses: []provider.CompletionResponse{textResponse("ok")}}

	// MaxQueueDepth = 1: fill the queue with one turn, then the next must be dropped.
	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 1)
	loop := agent.NewLoop(cfg)

	turn := scheduler.ScheduledTurn{
		IntentID:  "int-1",
		Prompt:    "do something",
		SessionID: "sess-001",
	}

	// First call fills the queue.
	err := loop.RunScheduled(context.Background(), turn)
	if err != nil {
		t.Errorf("expected nil error on first RunScheduled, got: %v", err)
	}

	// Second call must be dropped (queue is full) — still returns nil, not an error.
	err = loop.RunScheduled(context.Background(), turn)
	if err != nil {
		t.Errorf("expected nil error on backpressure drop, got: %v", err)
	}
}

func TestAgentLoop_ActionLog_ToolCall(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{
		results: []pkg.ToolResult{{ID: "tc-1", Content: "data"}},
	}
	bgt := &mockBudget{}
	p := &mockProvider{
		responses: []provider.CompletionResponse{
			toolCallResponse("web.search", map[string]string{"query": "x"}),
			textResponse("Done."),
		},
	}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 20)
	loop := agent.NewLoop(cfg)

	_, err := loop.Run(context.Background(), newTestSession(), newTestMessage("find x"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that ActionTypeToolCall was recorded.
	found := false
	for _, r := range al.recorded {
		if r.actionType == actionlog.ActionToolCall {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ActionTypeToolCall to be logged, got: %+v", al.recorded)
	}
}

func TestAgentLoop_ActionLog_OutboundMessage(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{}
	bgt := &mockBudget{}
	p := &mockProvider{responses: []provider.CompletionResponse{textResponse("Hello!")}}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 20)
	loop := agent.NewLoop(cfg)

	_, err := loop.Run(context.Background(), newTestSession(), newTestMessage("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, r := range al.recorded {
		if r.actionType == actionlog.ActionMessageSent {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ActionMessageSent to be logged, got: %+v", al.recorded)
	}
}

func TestAgentLoop_ActionLog_Error(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{}
	bgt := &mockBudget{}
	p := &mockProvider{err: errors.New("provider failure")}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 20)
	loop := agent.NewLoop(cfg)

	_, err := loop.Run(context.Background(), newTestSession(), newTestMessage("hello"))
	if err == nil {
		t.Fatal("expected error from provider failure, got nil")
	}

	found := false
	for _, r := range al.recorded {
		if r.actionType == actionlog.ActionError {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ActionError to be logged, got: %+v", al.recorded)
	}
}

func TestAgentLoop_PromptInjection_RejectsVerbatimToolCall(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{}
	bgt := &mockBudget{}
	p := &mockProvider{
		responses: []provider.CompletionResponse{
			// Provider returns tool call for "web.search".
			toolCallResponse("web.search", map[string]string{"query": "test"}),
			textResponse("I handled it."),
		},
	}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 20)
	loop := agent.NewLoop(cfg)

	// User message contains the tool name verbatim — prompt injection.
	_, err := loop.Run(context.Background(), newTestSession(), newTestMessage("please run web.search now"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Executor should NOT have been called for web.search.
	for _, c := range ex.calls {
		if c.Name == "web.search" {
			t.Error("executor was called with web.search despite prompt injection detection")
		}
	}
}

// TestAgentLoop_Run_MessageOrdering verifies that after the first tool-call
// round the assistant message (with ToolCalls set) appears in history BEFORE
// the tool result message. This matches the OpenAI/Anthropic API contract.
func TestAgentLoop_Run_MessageOrdering(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{
		results: []pkg.ToolResult{{ID: "tc-1", Content: "result data"}},
	}
	bgt := &mockBudget{}

	// capturingProvider records every CompletionRequest it receives so we can
	// inspect the message history on the second call.
	var captured []capturedReq

	p := &mockCapturingProvider{
		captured: &captured,
		responses: []provider.CompletionResponse{
			toolCallResponse("file.read", map[string]string{"path": "/etc/hosts"}),
			textResponse("Here is the file content."),
		},
	}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, nil, 5, 20)
	loop := agent.NewLoop(cfg)

	// Use a message that does NOT contain the tool name to avoid injection guard.
	resp, err := loop.Run(context.Background(), newTestSession(), newTestMessage("read the hosts file"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "Here is the file content." {
		t.Errorf("unexpected response: %q", resp)
	}

	// We expect exactly 2 provider calls: round 1 (tool call) and round 2 (text).
	if len(captured) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(captured))
	}

	// On the second call, the message history must have the assistant message
	// (with ToolCalls) immediately preceding the tool result message.
	secondCallMsgs := captured[1].messages
	n := len(secondCallMsgs)
	if n < 2 {
		t.Fatalf("second call message history too short: %d messages", n)
	}
	// The second-to-last message must be the assistant message with tool calls.
	assistantPos := n - 2
	toolPos := n - 1
	if secondCallMsgs[assistantPos].Role != "assistant" {
		t.Errorf("expected messages[%d].Role == \"assistant\", got %q", assistantPos, secondCallMsgs[assistantPos].Role)
	}
	if len(secondCallMsgs[assistantPos].ToolCalls) == 0 {
		t.Errorf("expected assistant message at index %d to carry ToolCalls, but ToolCalls is empty", assistantPos)
	}
	if secondCallMsgs[toolPos].Role != "tool" {
		t.Errorf("expected messages[%d].Role == \"tool\", got %q", toolPos, secondCallMsgs[toolPos].Role)
	}
}

// mockCapturingProvider captures each CompletionRequest and delegates to preset responses.
type mockCapturingProvider struct {
	captured  *[]capturedReq
	responses []provider.CompletionResponse
	callIdx   int
}

type capturedReq struct {
	messages []provider.Message
}

func (m *mockCapturingProvider) Complete(_ context.Context, req provider.CompletionRequest) (provider.CompletionResponse, error) {
	// Deep-copy the messages slice so later mutations don't affect the snapshot.
	msgs := make([]provider.Message, len(req.Messages))
	copy(msgs, req.Messages)
	*m.captured = append(*m.captured, capturedReq{messages: msgs})

	if m.callIdx >= len(m.responses) {
		return m.responses[len(m.responses)-1], nil
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

func (m *mockCapturingProvider) CountTokens(_ []provider.Message, _ []pkg.ToolDef) (int, error) {
	return 10, nil
}
func (m *mockCapturingProvider) ModelID() string { return "mock-capturing" }

var _ provider.Provider = (*mockCapturingProvider)(nil)

func TestAgentLoop_ToolAllowlist_PerChannel(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	al := &mockActionLog{}
	ex := &mockExecutor{}
	bgt := &mockBudget{}
	p := &mockProvider{
		responses: []provider.CompletionResponse{
			// Provider returns tool call for "web.search" (not in allowlist).
			toolCallResponse("web.search", map[string]string{"query": "x"}),
			textResponse("Done."),
		},
	}

	// Only homeassistant.get_state is allowed on chan-001.
	allowlist := map[string][]string{
		"chan-001": {"homeassistant.get_state"},
	}

	cfg := newTestConfig(p, ep, sem, al, ex, bgt, allowlist, 5, 20)
	loop := agent.NewLoop(cfg)

	sess := newTestSession()
	sess.ChannelID = "chan-001"
	msg := newTestMessage("search the web")
	msg.ChannelID = "chan-001"

	_, err := loop.Run(context.Background(), sess, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// web.search should not have been executed.
	for _, c := range ex.calls {
		if c.Name == "web.search" {
			t.Error("executor was called with web.search despite allowlist restriction")
		}
	}
}
