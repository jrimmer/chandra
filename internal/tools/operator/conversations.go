package operator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jrimmer/chandra/pkg"
)

type listConversationsTool struct {
	db *sql.DB
}

// NewListConversationsTool returns a Tool that lets Chandra inspect her conversation history.
func NewListConversationsTool(db *sql.DB) pkg.Tool {
	return &listConversationsTool{db: db}
}

func (t *listConversationsTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name:        "list_conversations",
		Description: "List recent conversations Chandra has had, with message counts and timestamps. Useful for self-inspection and debugging.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"limit": {
					"type": "integer",
					"description": "Maximum number of conversations to return. Defaults to 10."
				},
				"channel_id": {
					"type": "string",
					"description": "Filter to a specific Discord channel ID. Omit to show all channels."
				}
			}
		}`),
	}
}

func (t *listConversationsTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		Limit     int    `json:"limit"`
		ChannelID string `json:"channel_id"`
	}
	if err := json.Unmarshal(call.Parameters, &args); err != nil {
		return errResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error()), nil
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	// Query sessions grouped — each session is a conversation turn;
	// group by conversation_id to get conversation-level stats.
	var (
		query string
		qargs []any
	)
	if args.ChannelID != "" {
		query = `
			SELECT conversation_id, channel_id,
			       COUNT(*) as turn_count,
			       MIN(started_at) as first_at,
			       MAX(last_active) as last_at
			FROM sessions
			WHERE channel_id = ?
			GROUP BY conversation_id
			ORDER BY last_at DESC
			LIMIT ?`
		qargs = []any{args.ChannelID, args.Limit}
	} else {
		query = `
			SELECT conversation_id, channel_id,
			       COUNT(*) as turn_count,
			       MIN(started_at) as first_at,
			       MAX(last_active) as last_at
			FROM sessions
			GROUP BY conversation_id
			ORDER BY last_at DESC
			LIMIT ?`
		qargs = []any{args.Limit}
	}

	rows, err := t.db.QueryContext(ctx, query, qargs...)
	if err != nil {
		return errResult(call.ID, pkg.ErrInternal, "query failed: "+err.Error()), nil
	}
	defer rows.Close()

	type convSummary struct {
		ConvID    string `json:"conv_id"`
		ChannelID string `json:"channel_id"`
		Turns     int    `json:"turns"`
		FirstAt   string `json:"started_at"`
		LastAt    string `json:"last_active"`
	}

	var convs []convSummary
	for rows.Next() {
		var cs convSummary
		var firstAt, lastAt int64
		if err := rows.Scan(&cs.ConvID, &cs.ChannelID, &cs.Turns, &firstAt, &lastAt); err != nil {
			continue
		}
		cs.FirstAt = time.UnixMilli(firstAt).UTC().Format(time.RFC3339)
		cs.LastAt = time.UnixMilli(lastAt).UTC().Format(time.RFC3339)
		// Truncate conv_id for readability.
		if len(cs.ConvID) > 16 {
			cs.ConvID = cs.ConvID[:16] + "…"
		}
		convs = append(convs, cs)
	}
	if err := rows.Err(); err != nil {
		return errResult(call.ID, pkg.ErrInternal, "row scan error: "+err.Error()), nil
	}
	if len(convs) == 0 {
		return pkg.ToolResult{ID: call.ID, Content: "No conversations found."}, nil
	}

	// Format as readable table.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d conversation(s):\n\n", len(convs)))
	for _, cs := range convs {
		sb.WriteString(fmt.Sprintf("• %s  channel=%s  turns=%d  last=%s\n",
			cs.ConvID, cs.ChannelID, cs.Turns, cs.LastAt))
	}
	return pkg.ToolResult{ID: call.ID, Content: sb.String()}, nil
}

// ─── get_usage_stats tool ────────────────────────────────────────────────────

type getUsageStatsTool struct {
	db *sql.DB
}

// NewGetUsageStatsTool returns a Tool that reports token usage stats.
func NewGetUsageStatsTool(db *sql.DB) pkg.Tool {
	return &getUsageStatsTool{db: db}
}

func (t *getUsageStatsTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name:        "get_usage_stats",
		Description: "Report token usage statistics — daily totals, per-conversation breakdown, and all-time cumulative. Useful for cost awareness.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"conv_id": {
					"type": "string",
					"description": "Optional: filter to a specific conversation ID for per-conversation stats."
				},
				"days": {
					"type": "integer",
					"description": "Number of days of daily history to return. Defaults to 7."
				}
			}
		}`),
	}
}

func (t *getUsageStatsTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		ConvID string `json:"conv_id"`
		Days   int    `json:"days"`
	}
	if err := json.Unmarshal(call.Parameters, &args); err != nil {
		return errResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error()), nil
	}
	if args.Days <= 0 {
		args.Days = 7
	}

	var sb strings.Builder

	// All-time totals.
	var totalPrompt, totalCompletion int64
	_ = t.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0) FROM token_usage`,
	).Scan(&totalPrompt, &totalCompletion)
	sb.WriteString(fmt.Sprintf("All-time: %d prompt + %d completion = %d total tokens\n\n",
		totalPrompt, totalCompletion, totalPrompt+totalCompletion))

	// Today's totals.
	todayStart := time.Now().UTC().Truncate(24 * time.Hour).Unix()
	var todayPrompt, todayCompletion int64
	_ = t.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0)
		 FROM token_usage WHERE created_at >= ?`, todayStart,
	).Scan(&todayPrompt, &todayCompletion)
	sb.WriteString(fmt.Sprintf("Today: %d prompt + %d completion = %d total tokens\n\n",
		todayPrompt, todayCompletion, todayPrompt+todayCompletion))

	// Daily breakdown.
	sb.WriteString(fmt.Sprintf("Daily (last %d days):\n", args.Days))
	cutoff := time.Now().AddDate(0, 0, -args.Days).Unix()
	rows, err := t.db.QueryContext(ctx,
		`SELECT date(created_at, 'unixepoch') as day,
		        SUM(prompt_tokens), SUM(completion_tokens), COUNT(*), model
		 FROM token_usage
		 WHERE created_at >= ?
		 GROUP BY day, model
		 ORDER BY day DESC`, cutoff)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var day, model string
			var p, c int64
			var calls int
			if err2 := rows.Scan(&day, &p, &c, &calls, &model); err2 == nil {
				sb.WriteString(fmt.Sprintf("  %s  %s  %d calls  %d+%d tokens\n", day, model, calls, p, c))
			}
		}
	}

	// Per-conversation if requested.
	if args.ConvID != "" {
		var convPrompt, convCompletion int64
		var convCalls int
		_ = t.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0), COUNT(*)
			 FROM token_usage WHERE conv_id = ?`, args.ConvID,
		).Scan(&convPrompt, &convCompletion, &convCalls)
		sb.WriteString(fmt.Sprintf("\nConversation %s: %d calls, %d prompt + %d completion tokens\n",
			args.ConvID, convCalls, convPrompt, convCompletion))
	}

	return pkg.ToolResult{ID: call.ID, Content: sb.String()}, nil
}
