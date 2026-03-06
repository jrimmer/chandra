// Package agent implements the core agent reasoning loop.
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	MaxRounds           int                 // max tool call rounds per turn (default: 5)
	PostProcessTimeout  time.Duration       // timeout for background post-processing goroutine (default: 30s)
	// PostProcessDone is called by the post-processing goroutine when it completes.
	// Set in tests to synchronise assertions against async writes; leave nil in production.
	PostProcessDone func()
	PersonaFile    string              // optional: path to persona markdown file (overrides DB identity)
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
	RunScheduled(ctx context.Context, turn scheduler.ScheduledTurn) (string, error)

	// DrainPostProcess waits for all in-flight background post-processing
	// goroutines to complete, up to the given timeout. Call during graceful
	// shutdown to ensure episodic and semantic memory writes finish before exit.
	DrainPostProcess(timeout time.Duration)
}

// Compile-time assertion.
var _ AgentLoop = (*agentLoop)(nil)

// agentLoop implements AgentLoop.
type agentLoop struct {
	cfg  LoopConfig
	ppWg sync.WaitGroup // tracks in-flight post-processing goroutines
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
	// Use RecentAcrossSessions so episodic memory survives daemon restarts
	// and session boundary transitions (session IDs change on each restart
	// but channel+user identifies the conversation continuously).
	recentEps, err := l.cfg.Memory.Episodic().RecentAcrossSessions(ctx, session.ChannelID, session.UserID, 20)
	if err != nil {
		slog.Warn("agent/loop: failed to load recent episodes", "session_id", session.ID, "error", err)
	}
	// Steps 2-3: Retrieve semantic memories and assemble context window.
	// Prepend identity system prompt as the highest-priority fixed candidate.
	fixed := buildIdentityCandidate(l.cfg.Memory, l.cfg.PersonaFile)
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
		chain := strings.Join(chainTrace, " → ")
		slog.Warn("agent/loop: max tool rounds exceeded",
			"session_id", session.ID,
			"chain", chain,
		)
		if chain != "" {
			finalResponse = fmt.Sprintf("I ran out of steps working on that (ran: %s). Try breaking the request into smaller pieces, or ask me to focus on one thing at a time.", chain)
		} else {
			finalResponse = "I wasn't able to get started on that. Could you rephrase or give me a more specific task?"
		}
	}

	// Steps 7-9 run in a background goroutine so Run() returns finalResponse
	// immediately after the LLM call, reducing user-visible latency by ~100-150ms.
	// (The second Ollama embed call and DB writes no longer block the reply.)
	// The goroutine uses its own context so cancellation of the request ctx does
	// not abort in-flight memory writes.
	//
	// Safety: all writes are idempotent/append-only; WAL mode prevents DB corruption
	// on partial writes. At worst a crash loses one episode — acceptable for memory.
	ppTimeout := l.cfg.PostProcessTimeout
	if ppTimeout <= 0 {
		ppTimeout = 30 * time.Second
	}
	// Snapshot all values before returning — avoids data races with caller.
	ppUserContent := msg.Content
	ppLLMResponse := llmResponse
	ppFinalResp   := finalResponse
	ppSessionID   := session.ID
	ppChannelID   := session.ChannelID
	ppUserID      := session.UserID
	ppNow         := time.Now().UTC()

	l.ppWg.Add(1)
	go func() {
		defer l.ppWg.Done()
		ppCtx, cancel := context.WithTimeout(context.Background(), ppTimeout)
		defer cancel()
		if done := l.cfg.PostProcessDone; done != nil {
			defer done()
		}

		// Step 7: Append exchange to EpisodicStore.
		userEp := pkg.Episode{SessionID: ppSessionID, Role: "user", Content: ppUserContent, Timestamp: ppNow, Tags: []string{}}
		assistantEp := pkg.Episode{SessionID: ppSessionID, Role: "assistant", Content: ppFinalResp, Timestamp: ppNow, Tags: []string{}}
		if err := l.cfg.Memory.Episodic().Append(ppCtx, userEp); err != nil {
			slog.Warn("agent/loop: post-process: failed to append user episode", "error", err)
		}
		if err := l.cfg.Memory.Episodic().Append(ppCtx, assistantEp); err != nil {
			slog.Warn("agent/loop: post-process: failed to append assistant episode", "error", err)
		}

		// Step 8: Conditional semantic storage.
		l.maybeSemanticallyStore(ppCtx, ppUserContent, ppLLMResponse, ppSessionID, ppUserID)

		// Step 9a: Update relationship LastInteraction.
		if rel, relErr := l.cfg.Memory.Identity().Relationship(); relErr == nil {
			rel.LastInteraction = time.Now()
			if updateErr := l.cfg.Memory.Identity().UpdateRelationship(ppCtx, rel); updateErr != nil {
				slog.Warn("agent/loop: post-process: failed to update relationship", "error", updateErr)
			}
		}

		// Step 9b: Record outbound message to ActionLog.
		_ = l.cfg.ActionLog.Record(ppCtx, actionlog.ActionEntry{
			Type:      actionlog.ActionMessageSent,
			SessionID: ppSessionID,
			Summary:   "message sent in session " + ppSessionID,
			Details:   map[string]any{"channel_id": ppChannelID, "session_id": ppSessionID},
		})
	}()

	return finalResponse, nil
}

// RunScheduled implements AgentLoop.RunScheduled.
// It converts the ScheduledTurn into a synthetic InboundMessage and drives it
// through the standard Run() path. If Sessions is not configured the turn is
// dropped with a warning.
func (l *agentLoop) RunScheduled(ctx context.Context, turn scheduler.ScheduledTurn) (string, error) {
	if l.cfg.Sessions == nil {
		slog.Warn("agent/loop: RunScheduled: no session manager configured; dropping scheduled turn",
			"intent_id", turn.IntentID,
		)
		return "", nil
	}

	// Use the delivery target channel/user so the session is tied to the right
	// conversation and episodic context. Fall back to generic scheduler session.
	channelID := turn.ChannelID
	userID := turn.UserID
	convID := turn.SessionID
	if channelID == "" {
		channelID = "scheduler"
	}
	if userID == "" {
		userID = "system"
	}
	if channelID != "scheduler" && userID != "system" {
		// Derive a stable conversation ID from channel+user (same as Discord dispatch).
		convID = computeConvID(channelID, userID)
	}

	sess, err := l.cfg.Sessions.GetOrCreate(ctx, convID, channelID, userID)
	if err != nil {
		return "", fmt.Errorf("agent/loop: RunScheduled: get session: %w", err)
	}

	// Wrap the scheduled prompt so the LLM delivers it as a reminder rather
	// than treating it as a new user-initiated conversation. The instruction
	// is kept terse to avoid inflating the context window.
	prompt := "[SCHEDULED REMINDER] Deliver this reminder to the user now. " +
		"Be brief and natural — one or two sentences max. Do not add caveats " +
		"or discuss conversation history. Reminder: " + turn.Prompt
	msg := channels.InboundMessage{
		ConversationID: convID,
		UserID:         userID,
		ChannelID:      channelID,
		Content:        prompt,
		Timestamp:      time.Now().UTC(),
		Meta:           map[string]any{"intent_id": turn.IntentID, "scheduled": true},
	}

	return l.Run(ctx, sess, msg)
}


// computeConvID returns a stable conversation ID for a channel+user pair,
// matching the formula used by the Discord channel adapter.
func computeConvID(channelID, userID string) string {
	sum := sha256.Sum256([]byte(channelID + ":" + userID))
	return hex.EncodeToString(sum[:])[:16]
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
func (l *agentLoop) maybeSemanticallyStore(ctx context.Context, userContent, assistantContent, sessionID, userID string) {
	combined := userContent + " " + assistantContent
	lower := strings.ToLower(combined)

	// Check for explicit reinforcement keywords.
	hasReinforcement := strings.Contains(lower, "remember") ||
		strings.Contains(lower, "remember:") ||
		strings.Contains(lower, "important")

	if hasReinforcement {
		entry := pkg.MemoryEntry{
			UserID:     userID,
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
		UserID:     userID,
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

// DrainPostProcess waits for all in-flight background post-processing goroutines
// to complete, up to timeout. Used during graceful shutdown to ensure episodic
// and semantic memory writes finish before the process exits.
func (l *agentLoop) DrainPostProcess(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		l.ppWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("agent/loop: post-processing drained")
	case <-time.After(timeout):
		slog.Warn("agent/loop: post-processing drain timed out", "timeout", timeout)
	}
}

// buildIdentityCandidate loads the agent profile and relationship state from
// memory and returns a single high-priority system-prompt ContextCandidate.
// If either load fails (e.g. first run before any profile is set), an empty
// slice is returned so the rest of context assembly proceeds unaffected.
func buildIdentityCandidate(mem memory.Memory, personaFile string) []budget.ContextCandidate {
	// If a persona_file is configured, it becomes the entire system prompt.
	// This allows rich multi-paragraph personas without DB size concerns.
	if personaFile != "" {
		path := personaFile
		if strings.HasPrefix(path, "~/") {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, path[2:])
		}
		if raw, err := os.ReadFile(path); err == nil {
			systemPrompt := strings.TrimSpace(string(raw))
			return []budget.ContextCandidate{{
				Role:     "system",
				Content:  systemPrompt,
				Priority: 1.0,
				Tokens:   estimateTokens(systemPrompt),
			}}
		} else {
			slog.Warn("agent/loop: persona_file read failed, falling back to DB identity",
				"path", path, "err", err)
		}
	}

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
