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
	"github.com/jrimmer/chandra/internal/skills"
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

// SkillMatcher is the subset of skills.Registry used for trigger matching.
type SkillMatcher interface {
	Match(message string) []skills.Skill
}

// SkillConfig carries skill-related settings for context assembly.
type SkillConfig struct {
	Registry         SkillMatcher
	Priority         float64
	MaxContextTokens int
	MaxMatches       int
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
	Sessions       Manager             // required for RunScheduled; if nil, scheduled turns are dropped
	MaxRounds      int                 // max tool call rounds per turn (default: 5)
	ToolAllowlist  map[string][]string // channelID → allowed tool names (nil = all allowed)
	SkillRegistry  SkillMatcher        // optional: matches skills to messages
	SkillPriority  float64             // default: 0.7
	SkillMaxTokens int                 // default: 2000
	SkillMaxMatch  int                 // default: 3
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
	cfg LoopConfig
}

// NewLoop constructs an AgentLoop with the provided configuration.
// A default MaxRounds of 5 is applied when zero or negative.
func NewLoop(cfg LoopConfig) AgentLoop {
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 5
	}
	return &agentLoop{cfg: cfg}
}

// Run implements AgentLoop.Run: the 9-step think-act-remember cycle.
func (l *agentLoop) Run(ctx context.Context, session *Session, msg channels.InboundMessage) (string, error) {
	// Step 1: Load recent episodes and identity context.
	recentEps, err := l.cfg.Memory.Episodic().Recent(ctx, session.ID, 20)
	if err != nil {
		slog.Warn("agent/loop: failed to load recent episodes", "session_id", session.ID, "error", err)
	}
	// Steps 2-3: Retrieve semantic memories and assemble context window.
	// Prepend identity system prompt as the highest-priority fixed candidate.
	fixed := buildIdentityCandidate(l.cfg.Memory)
	fixed = append(fixed, episodesToCandidates(recentEps)...)

	// Step 4: Apply tool allowlist (before assembly so window carries the right tools).
	availableTools := l.cfg.Registry.All()
	if allowed, ok := l.cfg.ToolAllowlist[session.ChannelID]; ok {
		availableTools = filterTools(availableTools, allowed)
	}

	var skillCfg *SkillConfig
	if l.cfg.SkillRegistry != nil {
		priority := l.cfg.SkillPriority
		if priority == 0 {
			priority = 0.7
		}
		maxTokens := l.cfg.SkillMaxTokens
		if maxTokens == 0 {
			maxTokens = 2000
		}
		maxMatch := l.cfg.SkillMaxMatch
		if maxMatch == 0 {
			maxMatch = 3
		}
		skillCfg = &SkillConfig{
			Registry:         l.cfg.SkillRegistry,
			Priority:         priority,
			MaxContextTokens: maxTokens,
			MaxMatches:       maxMatch,
		}
	}

	window, err := assembleContext(ctx, msg, l.cfg.Memory, l.cfg.Budget, 8000, fixed, l.cfg.Provider, skillCfg)
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

// RunScheduled implements AgentLoop.RunScheduled.
// It converts the ScheduledTurn into a synthetic InboundMessage and drives it
// through the standard Run() path. If Sessions is not configured the turn is
// dropped with a warning.
func (l *agentLoop) RunScheduled(ctx context.Context, turn scheduler.ScheduledTurn) error {
	if l.cfg.Sessions == nil {
		slog.Warn("agent/loop: RunScheduled: no session manager configured; dropping scheduled turn",
			"intent_id", turn.IntentID,
		)
		return nil
	}

	sess, err := l.cfg.Sessions.GetOrCreate(ctx, turn.SessionID, "scheduler", "system")
	if err != nil {
		return fmt.Errorf("agent/loop: RunScheduled: get session: %w", err)
	}

	msg := channels.InboundMessage{
		ConversationID: turn.SessionID,
		UserID:         "system",
		ChannelID:      "scheduler",
		Content:        turn.Prompt,
		Timestamp:      time.Now().UTC(),
		Meta:           map[string]any{"intent_id": turn.IntentID, "scheduled": true},
	}

	_, runErr := l.Run(ctx, sess, msg)
	return runErr
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

// buildIdentityCandidate loads the agent profile and relationship state from
// memory and returns a single high-priority system-prompt ContextCandidate.
// If either load fails (e.g. first run before any profile is set), an empty
// slice is returned so the rest of context assembly proceeds unaffected.
func buildIdentityCandidate(mem memory.Memory) []budget.ContextCandidate {
	agentProfile, agentErr := mem.Identity().Agent()
	if agentErr != nil {
		// Profile not yet configured — skip rather than error.
		return nil
	}

	rel, relErr := mem.Identity().Relationship()

	var sb strings.Builder
	sb.WriteString("You are ")
	if agentProfile.Name != "" {
		sb.WriteString(agentProfile.Name)
	} else {
		sb.WriteString("Chandra")
	}
	sb.WriteString(".")
	if agentProfile.Persona != "" {
		sb.WriteString(" ")
		sb.WriteString(agentProfile.Persona)
	}
	if len(agentProfile.Traits) > 0 {
		sb.WriteString("\nTraits: ")
		sb.WriteString(strings.Join(agentProfile.Traits, ", "))
	}

	if relErr == nil {
		sb.WriteString(fmt.Sprintf("\nRelationship: trust_level=%d, style=%s",
			rel.TrustLevel, rel.CommunicationStyle))
		if len(rel.OngoingContext) > 0 {
			sb.WriteString("\nActive context: ")
			sb.WriteString(strings.Join(rel.OngoingContext, "; "))
		}
	}

	systemPrompt := sb.String()
	return []budget.ContextCandidate{
		{
			Role:     "system",
			Content:  systemPrompt,
			Priority: 1.0,
			Tokens:   estimateTokens(systemPrompt),
		},
	}
}
