package workertool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jrimmer/chandra/internal/agent/worker"
	"github.com/jrimmer/chandra/pkg"
)

type awaitTool struct {
	pool *worker.Pool
}

// NewAwaitAgentsTool returns a tool that blocks until all specified workers complete.
func NewAwaitAgentsTool(pool *worker.Pool) pkg.Tool { return &awaitTool{pool: pool} }

func (t *awaitTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name: "await_agents",
		Description: "Wait for one or more spawned workers to complete and collect their results. " +
			"Blocks until all specified workers finish. " +
			"NOTE: call this in a separate turn from spawn_agent — you need the worker_ids " +
			"returned by spawn_agent before you can await them. " +
			"Returns results for each worker including status, output, and token usage.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"worker_ids": {
					"type": "array",
					"items": {"type": "string"},
					"description": "List of worker_id values returned by spawn_agent."
				}
			},
			"required": ["worker_ids"]
		}`),
	}
}

func (t *awaitTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		WorkerIDs []string `json:"worker_ids"`
	}
	if err := json.Unmarshal(call.Parameters, &args); err != nil {
		return errResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error()), nil
	}
	if len(args.WorkerIDs) == 0 {
		return errResult(call.ID, pkg.ErrBadInput, "worker_ids must not be empty"), nil
	}

	results, err := t.pool.Await(ctx, args.WorkerIDs)
	if err != nil {
		// Partial results may still be useful; include them.
		return pkg.ToolResult{
			ID:      call.ID,
			Content: formatResults(results) + fmt.Sprintf("\n\n[await interrupted: %v]", err),
		}, nil
	}

	return pkg.ToolResult{
		ID:      call.ID,
		Content: formatResults(results),
	}, nil
}

func formatResults(results []worker.WorkerResult) string {
	var sb strings.Builder
	var totalPrompt, totalCompletion int64

	for _, r := range results {
		totalPrompt += int64(r.Tokens.PromptTokens)
		totalCompletion += int64(r.Tokens.CompletionTokens)
	}

	sb.WriteString(fmt.Sprintf("Workers completed: %d\n", len(results)))
	sb.WriteString(fmt.Sprintf("Total tokens: %d prompt + %d completion = %d\n\n",
		totalPrompt, totalCompletion, totalPrompt+totalCompletion))

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("── Worker %d (%s) ──\n", i+1, r.WorkerID))
		sb.WriteString(fmt.Sprintf("Task:     %s\n", r.Task))
		sb.WriteString(fmt.Sprintf("Status:   %s\n", r.Status))
		sb.WriteString(fmt.Sprintf("Duration: %dms\n", r.DurationMs))
		sb.WriteString(fmt.Sprintf("Tokens:   %d prompt + %d completion\n",
			r.Tokens.PromptTokens, r.Tokens.CompletionTokens))
		if r.Error != "" {
			sb.WriteString(fmt.Sprintf("Error:    %s\n", r.Error))
		}
		if r.Output != "" {
			sb.WriteString("Output:\n")
			sb.WriteString(r.Output)
		}
		sb.WriteString("\n\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}
