// Package workertool provides the spawn_agent and await_agents tools.
package workertool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jrimmer/chandra/internal/agent/worker"
	"github.com/jrimmer/chandra/pkg"
)

type spawnTool struct {
	pool *worker.Pool
}

// NewSpawnAgentTool returns a tool that spawns a parallel worker agent.
// The pool must be the same Pool passed to NewAwaitAgentsTool.
func NewSpawnAgentTool(pool *worker.Pool) pkg.Tool { return &spawnTool{pool: pool} }

func (t *spawnTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name: "spawn_agent",
		Description: "Spawn a parallel worker agent to execute an independent subtask concurrently. " +
			"Returns a worker_id immediately; use await_agents to collect results. " +
			"Call spawn_agent multiple times in the same turn to parallelise independent tasks — " +
			"all workers launch simultaneously. " +
			"Workers have access to exec, read_file, write_file, web_search, and get_current_time. " +
			"Workers cannot spawn further agents, modify config, or write to memory. " +
			"Pool limit: 3 concurrent workers.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"task": {
					"type": "string",
					"description": "Clear description of the task the worker should complete. Be specific: include target host, file paths, or any concrete details the worker needs."
				},
				"context": {
					"type": "string",
					"description": "Optional: curated context from the parent conversation that the worker needs. Do not paste full history — include only directly relevant facts (e.g. SSH key format, expected output format, credentials location)."
				},
				"inactivity_secs": {
					"type": "integer",
					"description": "Inactivity timeout in seconds (default 300 = 5 minutes). The timer resets on every LLM response or tool call. Only stalled workers are cancelled."
				}
			},
			"required": ["task"]
		}`),
	}
}

func (t *spawnTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		Task           string `json:"task"`
		Context        string `json:"context"`
		InactivitySecs int    `json:"inactivity_secs"`
	}
	if err := json.Unmarshal(call.Parameters, &args); err != nil {
		return errResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error()), nil
	}
	if args.Task == "" {
		return errResult(call.ID, pkg.ErrBadInput, "task is required"), nil
	}

	id, err := t.pool.Spawn(ctx, worker.WorkerTask{
		Task:           args.Task,
		Context:        args.Context,
		InactivitySecs: args.InactivitySecs,
	})
	if err != nil {
		return errResult(call.ID, pkg.ErrInternal, fmt.Sprintf("spawn failed: %v", err)), nil
	}

	return pkg.ToolResult{
		ID:      call.ID,
		Content: fmt.Sprintf(`{"worker_id": %q, "status": "running", "task": %q}`, id, args.Task),
	}, nil
}

func errResult(id string, kind pkg.ToolErrorKind, msg string) pkg.ToolResult {
	return pkg.ToolResult{ID: id, Error: &pkg.ToolError{Kind: kind, Message: msg}}
}
