// Package worker implements a bounded pool of parallel agent workers.
//
// Workers are the third layer of Chandra's concurrency model:
//
//	Layer 1: Per-conversation goroutines (convQueues in main.go)
//	         Multiple conversations run concurrently; messages within
//	         a conversation are serialized.
//
//	Layer 2: Parallel tool execution (tools.Executor)
//	         Within a single turn, all tool calls execute concurrently
//	         via goroutines. spawn_agent calls in the same turn therefore
//	         launch all workers simultaneously — no extra orchestration.
//
//	Layer 3: Worker pool (this package)
//	         On-demand isolated agent goroutines spawnable mid-turn.
//	         Each worker has its own LLM context window, runs the full
//	         tool reasoning loop, and returns a result to the parent.
//
// Workers are intentionally lightweight: goroutines, not processes or
// containers. Spawning cost is microseconds; runtime cost is LLM API calls.
// Workers are ephemeral — they hold no persistent state and do not write
// to episodic memory.
package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/internal/tools"
	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
)

// ErrPoolFull is returned by Spawn when the pool is at capacity.
var ErrPoolFull = errors.New("worker pool full")

const (
	defaultMaxWorkers      = 3
	defaultInactivityLimit = 5 * time.Minute
	defaultMaxRounds       = 20
	watchdogInterval       = 30 * time.Second
)

// WorkerTask describes a task to execute in a worker.
type WorkerTask struct {
	// Task is the task description given to the worker as its objective.
	Task string

	// Context is optional curated context from the parent conversation:
	// facts the worker needs but wouldn't have from semantic memory alone.
	// The parent LLM decides what to include — it is not the full history.
	Context string

	// InactivitySecs overrides the inactivity timeout (0 = default 5min).
	// The inactivity timeout is reset on every LLM response or tool call.
	// A worker is cancelled only if this duration passes with no activity —
	// long but active workloads are never killed.
	InactivitySecs int
}

// TokenUsage tracks prompt and completion token counts for a worker.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
}

func (u *TokenUsage) add(other TokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
}

// WorkerStatus is the live state of a running worker, used for progress reporting.
type WorkerStatus struct {
	WorkerID string
	Task     string
	Running  bool
}

// WorkerResult is returned by Await for a completed worker.
type WorkerResult struct {
	WorkerID string
	Task     string
	// Status is one of: "done", "cancelled", "error", "timeout"
	Status     string
	Output     string
	Error      string
	Tokens     TokenUsage
	DurationMs int64
}

// Pool manages a bounded set of concurrent worker agents.
// It is safe for concurrent use.
type Pool struct {
	mu         sync.Mutex
	maxWorkers int
	active     map[string]*workerState

	prov     provider.Provider
	exec     tools.Executor
	toolDefs []pkg.ToolDef
}

type workerState struct {
	id       string
	task     WorkerTask
	resultCh chan WorkerResult // buffered(1); written exactly once on completion
	cancel   context.CancelFunc

	actMu        sync.Mutex
	lastActivity time.Time
}

func (w *workerState) resetActivity() {
	w.actMu.Lock()
	w.lastActivity = time.Now()
	w.actMu.Unlock()
}

func (w *workerState) timeSinceActivity() time.Duration {
	w.actMu.Lock()
	defer w.actMu.Unlock()
	return time.Since(w.lastActivity)
}

// NewPool returns a Pool ready to spawn workers.
//
//   - prov:       shared LLM provider (workers share the connection, not context)
//   - exec:       executor built with the worker-safe tool registry (no spawn_agent,
//                 no set_config, no write_skill, no note_context)
//   - toolDefs:   tool definitions advertised to the worker's LLM
//   - maxWorkers: concurrent cap (≤0 defaults to 3)
func NewPool(prov provider.Provider, exec tools.Executor, toolDefs []pkg.ToolDef, maxWorkers int) *Pool {
	if maxWorkers <= 0 {
		maxWorkers = defaultMaxWorkers
	}
	return &Pool{
		maxWorkers: maxWorkers,
		active:     make(map[string]*workerState),
		prov:       prov,
		exec:       exec,
		toolDefs:   toolDefs,
	}
}

// Spawn starts a new worker goroutine and returns its ID immediately.
// The caller should pass workerIDs to Await to collect results.
// Returns ErrPoolFull if the pool is at capacity.
func (p *Pool) Spawn(ctx context.Context, task WorkerTask) (string, error) {
	p.mu.Lock()
	if len(p.active) >= p.maxWorkers {
		count := len(p.active)
		p.mu.Unlock()
		return "", fmt.Errorf("%w: %d/%d workers active; call await_agents first",
			ErrPoolFull, count, p.maxWorkers)
	}

	inactivityLimit := defaultInactivityLimit
	if task.InactivitySecs > 0 {
		inactivityLimit = time.Duration(task.InactivitySecs) * time.Second
	}

	workerCtx, workerCancel := context.WithCancel(ctx)
	id := store.NewID()
	w := &workerState{
		id:           id,
		task:         task,
		resultCh:     make(chan WorkerResult, 1),
		cancel:       workerCancel,
		lastActivity: time.Now(),
	}
	p.active[id] = w
	p.mu.Unlock()

	go p.runWorker(workerCtx, w, inactivityLimit)
	return id, nil
}

// Await blocks until all specified workers complete or ctx is cancelled.
// Results are returned in the same order as workerIDs.
// If ctx is cancelled, remaining workers are cancelled and the partial
// results are returned alongside the context error.
func (p *Pool) Await(ctx context.Context, workerIDs []string) ([]WorkerResult, error) {
	// Snapshot the result channels under the lock so we don't hold the
	// lock while blocking on channel reads.
	p.mu.Lock()
	channels := make([]chan WorkerResult, len(workerIDs))
	for i, id := range workerIDs {
		if w, ok := p.active[id]; ok {
			channels[i] = w.resultCh
		}
	}
	p.mu.Unlock()

	results := make([]WorkerResult, len(workerIDs))
	for i, ch := range channels {
		if ch == nil {
			results[i] = WorkerResult{
				WorkerID: workerIDs[i],
				Status:   "error",
				Error:    "worker not found (unknown ID or already awaited)",
			}
			continue
		}
		select {
		case r := <-ch:
			results[i] = r
		case <-ctx.Done():
			// Parent cancelled — cancel all remaining workers and return.
			for j := i; j < len(workerIDs); j++ {
				p.mu.Lock()
				if w, ok := p.active[workerIDs[j]]; ok {
					w.cancel()
				}
				p.mu.Unlock()
			}
			return results, ctx.Err()
		}
	}
	return results, nil
}

// ActiveCount returns the number of currently running workers.
func (p *Pool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.active)
}

// ActiveStatus returns live status for all running workers.
func (p *Pool) ActiveStatus() []WorkerStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]WorkerStatus, 0, len(p.active))
	for id, w := range p.active {
		out = append(out, WorkerStatus{WorkerID: id, Task: w.task.Task, Running: true})
	}
	return out
}

// runWorker is the goroutine entry point. It runs the LLM loop, manages the
// inactivity watchdog, cleans up on exit, and sends the result.
func (p *Pool) runWorker(ctx context.Context, w *workerState, inactivityLimit time.Duration) {
	start := time.Now()

	// Inactivity watchdog: cancels the worker if no LLM activity for inactivityLimit.
	// This is an inactivity timeout — not wall-clock. A long but active workload
	// (large codebase audit, multi-step research) resets the timer on every LLM
	// response or tool call completion. Only a truly stalled worker is cut.
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if w.timeSinceActivity() > inactivityLimit {
					w.cancel() // signals context.Canceled to the LLM loop
					return
				}
			}
		}
	}()

	result := p.runLoop(ctx, w, inactivityLimit)
	result.DurationMs = time.Since(start).Milliseconds()

	w.cancel()        // idempotent; stops watchdog if still running
	<-watchdogDone   // wait for watchdog to exit before cleanup

	p.mu.Lock()
	delete(p.active, w.id)
	p.mu.Unlock()

	w.resultCh <- result // buffered(1); never blocks
}

// runLoop is the worker's LLM reasoning loop. It mirrors the agent loop in
// cmd/chandrad/main.go but is simpler: no session management, no episodic
// memory writes, no progressive delivery, no context assembly — just tool
// calls until the model returns a final answer.
func (p *Pool) runLoop(ctx context.Context, w *workerState, inactivityLimit time.Duration) WorkerResult {
	sysPrompt := "You are a focused task worker. Complete the following task completely and " +
		"return your results.\n" +
		"Do not ask clarifying questions — execute the task directly using available tools.\n" +
		"When the task is complete, provide a clear summary of what you did and what you found.\n\n" +
		"Task: " + w.task.Task
	if w.task.Context != "" {
		sysPrompt += "\n\nAdditional context from the parent conversation:\n" + w.task.Context
	}

	messages := []provider.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: "Please complete the task."},
	}

	var usage TokenUsage

	for round := 0; round < defaultMaxRounds; round++ {
		// Check for cancellation (from parent ctx, inactivity watchdog, or safety interrupt).
		select {
		case <-ctx.Done():
			status := "cancelled"
			if w.timeSinceActivity() > inactivityLimit {
				status = "timeout"
			}
			return WorkerResult{
				WorkerID: w.id, Task: w.task.Task, Status: status, Tokens: usage,
			}
		default:
		}

		resp, err := p.prov.Complete(ctx, provider.CompletionRequest{
			Messages: messages,
			Tools:    p.toolDefs,
		})
		if err != nil {
			if ctx.Err() != nil {
				return WorkerResult{WorkerID: w.id, Task: w.task.Task, Status: "cancelled", Tokens: usage}
			}
			return WorkerResult{
				WorkerID: w.id, Task: w.task.Task, Status: "error",
				Error: err.Error(), Tokens: usage,
			}
		}

		w.resetActivity()
		usage.add(TokenUsage{
			PromptTokens:     resp.InputTokens,
			CompletionTokens: resp.OutputTokens,
		})

		// No tool calls — the model has finished.
		if len(resp.ToolCalls) == 0 {
			return WorkerResult{
				WorkerID: w.id, Task: w.task.Task, Status: "done",
				Output: resp.Message.Content, Tokens: usage,
			}
		}

		// Execute tool calls concurrently via the worker-safe executor.
		toolResults := p.exec.Execute(ctx, resp.ToolCalls)
		w.resetActivity()

		// Append assistant turn + tool results to the conversation.
		messages = append(messages, provider.Message{
			Role:      "assistant",
			Content:   resp.Message.Content,
			ToolCalls: resp.ToolCalls,
		})
		for _, r := range toolResults {
			content := r.Content
			if r.Error != nil {
				content = "error: " + r.Error.Message
			}
			messages = append(messages, provider.Message{
				Role:       "tool",
				ToolCallID: r.ID,
				Content:    content,
			})
		}
	}

	return WorkerResult{
		WorkerID: w.id, Task: w.task.Task, Status: "error",
		Error:  fmt.Sprintf("exceeded max rounds (%d)", defaultMaxRounds),
		Tokens: usage,
	}
}
