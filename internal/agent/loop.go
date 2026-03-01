// Package agent implements the core agent reasoning loop.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jrimmer/chandra/internal/actionlog"
	"github.com/jrimmer/chandra/internal/budget"
	"github.com/jrimmer/chandra/internal/channels"
	"github.com/jrimmer/chandra/internal/memory"
	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/internal/scheduler"
	"github.com/jrimmer/chandra/internal/tools"
	"github.com/jrimmer/chandra/pkg"
)

// ContextBudget is the subset of budget.Manager used by the agent loop.
// It is defined here so callers do not need to import the budget package.
type ContextBudget interface {
	Assemble(
		ctx context.Context,
		tokenBudget int,
		fixed []budget.ContextCandidate,
		ranked []budget.ContextCandidate,
		toolDefs []pkg.ToolDef,
		toolTokens int,
	) (budget.ContextWindow, error)
}

// LoopConfig carries all dependencies for the AgentLoop.
type LoopConfig struct {
	Provider      provider.Provider
	Memory        memory.Memory
	Budget        ContextBudget
	Registry      tools.Registry
	Executor      tools.Executor
	ActionLog     actionlog.ActionLog
	Channel       channels.Channel
	MaxRounds     int               // max tool call rounds per turn (default: 5)
	MaxQueueDepth int               // max pending scheduled turns before shedding (default: 20)
	ToolAllowlist map[string][]string // channelID → allowed tool names (nil = all allowed)
}

// AgentLoop is the central reasoning cycle for the Chandra agent.
type AgentLoop interface {
	// Run processes one inbound message through the think-act-remember cycle.
	Run(ctx context.Context, session *Session, msg channels.InboundMessage) (string, error)

	// RunScheduled processes a proactive turn injected by the Scheduler.
	// Returns nil immediately (with a WARN log) when the internal queue is full.
	RunScheduled(ctx context.Context, turn scheduler.ScheduledTurn) error
}

// Compile-time assertion.
var _ AgentLoop = (*agentLoop)(nil)

// agentLoop implements AgentLoop.
type agentLoop struct {
	cfg   LoopConfig
	queue chan scheduler.ScheduledTurn
}

// NewLoop constructs an AgentLoop with the provided configuration. Defaults are
// applied for MaxRounds (5) and MaxQueueDepth (20) when zero or negative.
func NewLoop(cfg LoopConfig) AgentLoop {
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 5
	}
	queueDepth := cfg.MaxQueueDepth
	if queueDepth <= 0 {
		queueDepth = 20
	}
	return &agentLoop{
		cfg:   cfg,
		queue: make(chan scheduler.ScheduledTurn, queueDepth),
	}
}

// Run implements AgentLoop.Run: the 9-step think-act-remember cycle.
func (l *agentLoop) Run(ctx context.Context, session *Session, msg channels.InboundMessage) (string, error) {
	// Step 1: Load recent episodes and identity context.
	recentEps, err := l.cfg.Memory.Episodic().Recent(ctx, session.ID, 20)
	if err != nil {
		slog.Warn("agent/loop: failed to load recent episodes", "session_id", session.ID, "error", err)
	}
	// Steps 2-3: Retrieve semantic memories and assemble context window.
	fixed := episodesToCandidates(recentEps)

	// Step 4: Apply tool allowlist (before assembly so window carries the right tools).
	availableTools := l.cfg.Registry.All()
	if allowed, ok := l.cfg.ToolAllowlist[session.ChannelID]; ok {
		availableTools = filterTools(availableTools, allowed)
	}

	window, err := assembleContext(ctx, msg, l.cfg.Memory, l.cfg.Budget, 8000, fixed, l.cfg.Provider)
	if err != nil {
		slog.Warn("agent/loop: budget assembly failed", "error", err)
		window = budget.ContextWindow{Tools: availableTools}
	}
	// Merge available tools into the window; assembleContext passes nil tools
	// so the caller owns the tool list (post allowlist filtering).
	if window.Tools == nil {
		window.Tools = availableTools
	}

	// Build initial messages: assembled context + current user message.
	// Copy the slice to avoid sharing the backing array with window.Messages.
	messages := append([]provider.Message(nil), window.Messages...)
	messages = append(messages, provider.Message{Role: "user", Content: msg.Content})

	// Step 5 & 6: Call provider, enter tool-call loop.
	// llmResponse holds the actual LLM-generated text (empty if only tool calls occurred).
	// finalResponse is llmResponse or the graceful fallback — always non-empty after this block.
	var llmResponse string
	var finalResponse string
	chainTrace := []string{}

	for round := 0; round < l.cfg.MaxRounds; round++ {
		resp, err := l.cfg.Provider.Complete(ctx, provider.CompletionRequest{
			Messages: messages,
			Tools:    window.Tools,
		})
		if err != nil {
			_ = l.cfg.ActionLog.Record(ctx, actionlog.ActionEntry{
				Type:      actionlog.ActionError,
				SessionID: session.ID,
				Summary:   err.Error(),
			})
			return "", fmt.Errorf("agent/loop: provider.Complete: %w", err)
		}

		// No tool calls — we have the final answer.
		if len(resp.ToolCalls) == 0 {
			llmResponse = resp.Message.Content
			finalResponse = llmResponse
			break
		}

		// Filter tool calls: prompt injection defense + allowlist.
		safeToolCalls := l.filterSafeToolCalls(resp.ToolCalls, msg.Content, session.ChannelID)

		// Append assistant message with tool calls to history BEFORE tool results.
		// The API requires assistant message (with ToolCalls) to precede the tool result messages.
		assistantMsg := resp.Message
		assistantMsg.ToolCalls = resp.ToolCalls
		messages = append(messages, assistantMsg)

		// Execute safe tool calls in parallel and append results.
		if len(safeToolCalls) > 0 {
			results := l.cfg.Executor.Execute(ctx, safeToolCalls)
			for i, result := range results {
				toolName := safeToolCalls[i].Name
				chainTrace = append(chainTrace, toolName)

				// Record tool call to ActionLog.
				_ = l.cfg.ActionLog.Record(ctx, actionlog.ActionEntry{
					Type:      actionlog.ActionToolCall,
					SessionID: session.ID,
					ToolName:  toolName,
					Summary:   "tool call: " + toolName,
					Details: map[string]any{
						"tool": toolName,
						"id":   safeToolCalls[i].ID,
					},
				})

				// Append tool result to message history.
				messages = append(messages, provider.Message{
					Role:       "tool",
					Content:    result.Content,
					ToolCallID: result.ID,
				})
			}
		}
	}

	// If we exhausted MaxRounds without a final text response, return graceful message.
	// llmResponse remains empty to signal a tool-call-only turn for semantic storage.
	if finalResponse == "" {
		slog.Warn("agent/loop: max tool rounds exceeded",
			"session_id", session.ID,
			"chain", strings.Join(chainTrace, " -> "),
		)
		finalResponse = "I wasn't able to complete that, please try again."
	}

	// Step 7: Append final exchange to EpisodicStore.
	now := time.Now().UTC()
	userEp := pkg.Episode{
		SessionID: session.ID,
		Role:      "user",
		Content:   msg.Content,
		Timestamp: now,
		Tags:      []string{},
	}
	assistantEp := pkg.Episode{
		SessionID: session.ID,
		Role:      "assistant",
		Content:   finalResponse,
		Timestamp: now,
		Tags:      []string{},
	}
	if err := l.cfg.Memory.Episodic().Append(ctx, userEp); err != nil {
		slog.Warn("agent/loop: failed to append user episode", "error", err)
	}
	if err := l.cfg.Memory.Episodic().Append(ctx, assistantEp); err != nil {
		slog.Warn("agent/loop: failed to append assistant episode", "error", err)
	}

	// Step 8: Conditional semantic storage.
	// Pass llmResponse (the actual LLM text) so tool-call-only turns (empty llmResponse) are skipped.
	l.maybeSemanticallyStore(ctx, msg.Content, llmResponse, session.ID)

	// G17: Update LastInteraction on the relationship state after each turn.
	if rel, relErr := l.cfg.Memory.Identity().Relationship(); relErr == nil {
		rel.LastInteraction = time.Now()
		if updateErr := l.cfg.Memory.Identity().UpdateRelationship(ctx, rel); updateErr != nil {
			slog.Warn("agent/loop: failed to update relationship last_interaction", "error", updateErr)
		}
	}

	// Step 9: Record outbound message to ActionLog.
	_ = l.cfg.ActionLog.Record(ctx, actionlog.ActionEntry{
		Type:      actionlog.ActionMessageSent,
		SessionID: session.ID,
		Summary:   "message sent in session " + session.ID,
		Details: map[string]any{
			"channel_id": session.ChannelID,
			"session_id": session.ID,
		},
	})

	return finalResponse, nil
}

// RunScheduled implements AgentLoop.RunScheduled with backpressure shedding.
func (l *agentLoop) RunScheduled(_ context.Context, turn scheduler.ScheduledTurn) error {
	if len(l.queue) >= cap(l.queue) {
		slog.Warn("agent/loop: queue full, dropping scheduled turn",
			"intent_id", turn.IntentID,
			"session_id", turn.SessionID,
		)
		return nil
	}
	// In v1 we directly drop if the channel is already at capacity.
	// For a zero-capacity queue, cap(l.queue)==0 so the check above always drops.
	select {
	case l.queue <- turn:
	default:
		slog.Warn("agent/loop: queue full (non-blocking send dropped), dropping scheduled turn",
			"intent_id", turn.IntentID,
		)
	}
	return nil
}

// filterSafeToolCalls removes tool calls that fail the prompt-injection check
// or are not in the per-channel allowlist.
func (l *agentLoop) filterSafeToolCalls(calls []pkg.ToolCall, userContent, channelID string) []pkg.ToolCall {
	lowerContent := strings.ToLower(userContent)

	// Build allowlist set for this channel, if configured.
	var allowSet map[string]struct{}
	if allowed, ok := l.cfg.ToolAllowlist[channelID]; ok {
		allowSet = make(map[string]struct{}, len(allowed))
		for _, name := range allowed {
			allowSet[name] = struct{}{}
		}
	}

	safe := make([]pkg.ToolCall, 0, len(calls))
	for _, call := range calls {
		// Allowlist check.
		if allowSet != nil {
			if _, allowed := allowSet[call.Name]; !allowed {
				slog.Warn("agent/loop: tool call rejected by allowlist",
					"tool", call.Name,
					"channel_id", channelID,
				)
				continue
			}
		}

		// Prompt injection defense: reject if tool name appears verbatim in user input.
		if strings.Contains(lowerContent, strings.ToLower(call.Name)) {
			slog.Warn("agent/loop: prompt injection suspected: tool call name found verbatim in user input",
				"tool", call.Name,
			)
			continue
		}

		safe = append(safe, call)
	}
	return safe
}

// maybeSemanticallyStore conditionally stores the turn in semantic memory.
// assistantContent must be the actual LLM-generated text (not the graceful fallback) — an
// empty string indicates a pure tool-call-only turn that produced no concluding text.
// The reinforcement check happens BEFORE the length filter.
func (l *agentLoop) maybeSemanticallyStore(ctx context.Context, userContent, assistantContent, sessionID string) {
	combined := userContent + " " + assistantContent
	lower := strings.ToLower(combined)

	// Check for explicit reinforcement keywords.
	hasReinforcement := strings.Contains(lower, "remember") ||
		strings.Contains(lower, "remember:") ||
		strings.Contains(lower, "important")

	if hasReinforcement {
		entry := pkg.MemoryEntry{
			Content:    combined,
			Source:     "conversation",
			Timestamp:  time.Now().UTC(),
			Importance: 0.8,
		}
		if err := l.cfg.Memory.Semantic().Store(ctx, entry); err != nil {
			slog.Warn("agent/loop: failed to store reinforcement memory", "session_id", sessionID, "error", err)
		}
		return
	}

	// Skip pure tool-call-only turns: no concluding LLM text response was produced.
	if strings.TrimSpace(assistantContent) == "" {
		return
	}

	// Length filter: estimate tokens as word_count * 4/3.
	wordCount := len(strings.Fields(combined))
	estimatedTokens := wordCount * 4 / 3
	if estimatedTokens < 50 {
		return // Too short — skip.
	}

	entry := pkg.MemoryEntry{
		Content:    combined,
		Source:     "conversation",
		Timestamp:  time.Now().UTC(),
		Importance: 0.5,
	}
	if err := l.cfg.Memory.Semantic().Store(ctx, entry); err != nil {
		slog.Warn("agent/loop: failed to store semantic memory", "session_id", sessionID, "error", err)
	}
}

// episodesToCandidates converts a slice of episodes to budget context candidates.
func episodesToCandidates(eps []pkg.Episode) []budget.ContextCandidate {
	out := make([]budget.ContextCandidate, len(eps))
	for i, ep := range eps {
		out[i] = budget.ContextCandidate{
			Role:    ep.Role,
			Content: ep.Content,
			Recency: ep.Timestamp,
			Tokens:  estimateTokens(ep.Content),
		}
	}
	return out
}

// memoriesToCandidates converts a slice of memory entries to budget context candidates.
func memoriesToCandidates(entries []pkg.MemoryEntry) []budget.ContextCandidate {
	out := make([]budget.ContextCandidate, len(entries))
	for i, e := range entries {
		out[i] = budget.ContextCandidate{
			Role:       "memory",
			Content:    e.Content,
			Priority:   e.Score,
			Importance: e.Importance,
			Recency:    e.Timestamp,
			Tokens:     estimateTokens(e.Content),
		}
	}
	return out
}

// filterTools returns only the tools whose names are in the allowed set.
func filterTools(all []pkg.ToolDef, allowed []string) []pkg.ToolDef {
	allowSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowSet[name] = struct{}{}
	}
	filtered := make([]pkg.ToolDef, 0, len(allowed))
	for _, def := range all {
		if _, ok := allowSet[def.Name]; ok {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

// estimateTokens estimates the token count for a string using the heuristic:
// word_count * 4/3.
func estimateTokens(s string) int {
	return len(strings.Fields(s)) * 4 / 3
}
