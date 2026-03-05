// Package scheduletool provides the schedule_reminder built-in tool.
// It allows the LLM to create a one-shot intent that fires at a specified time
// and delivers a reminder message back to the originating channel and user.
package scheduletool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/pkg"
)

// contextKey is the package-private key type for context values.
type contextKey int

const (
	// KeyChannelID is the context key for the current channel ID.
	KeyChannelID contextKey = iota
	// KeyUserID is the context key for the current user ID.
	KeyUserID
)

// WithDelivery returns a new context carrying the channel and user for tool delivery.
func WithDelivery(ctx context.Context, channelID, userID string) context.Context {
	ctx = context.WithValue(ctx, KeyChannelID, channelID)
	ctx = context.WithValue(ctx, KeyUserID, userID)
	return ctx
}

type scheduleReminderTool struct {
	store intent.IntentStore
}

// NewScheduleReminderTool returns a pkg.Tool that creates one-shot reminders.
func NewScheduleReminderTool(store intent.IntentStore) pkg.Tool {
	return &scheduleReminderTool{store: store}
}

func (t *scheduleReminderTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name:        "schedule_reminder",
		Description: "Schedule a one-shot reminder to be delivered to the user at a specific time. Use when the user asks to be reminded of something later, or to follow up on a task at a future time.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": {
					"type": "string",
					"description": "The reminder message to deliver to the user."
				},
				"due_at": {
					"type": "string",
					"description": "ISO 8601 UTC timestamp when the reminder should fire, e.g. \"2026-03-05T04:20:00Z\". Compute this from the user's request relative to the current time."
				}
			},
			"required": ["message", "due_at"]
		}`),
	}
}

func (t *scheduleReminderTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		Message string `json:"message"`
		DueAt   string `json:"due_at"`
	}
	if err := json.Unmarshal(call.Parameters, &args); err != nil {
		return pkg.ToolResult{ID: call.ID, Content: "error: invalid params: " + err.Error()}, nil
	}
	if args.Message == "" {
		return pkg.ToolResult{ID: call.ID, Content: "error: message is required"}, nil
	}
	if args.DueAt == "" {
		return pkg.ToolResult{ID: call.ID, Content: "error: due_at is required"}, nil
	}

	dueAt, err := time.Parse(time.RFC3339, args.DueAt)
	if err != nil {
		return pkg.ToolResult{ID: call.ID, Content: fmt.Sprintf("error: could not parse due_at %q — use ISO 8601 UTC format like \"2026-03-05T04:20:00Z\": %v", args.DueAt, err)}, nil
	}
	if dueAt.Before(time.Now()) {
		return pkg.ToolResult{ID: call.ID, Content: "error: due_at is in the past"}, nil
	}

	channelID, _ := ctx.Value(KeyChannelID).(string)
	userID, _ := ctx.Value(KeyUserID).(string)
	if channelID == "" || userID == "" {
		return pkg.ToolResult{ID: call.ID, Content: "error: delivery context not available (internal error)"}, nil
	}

	in := intent.Intent{
		Description: fmt.Sprintf("Reminder: %s", args.Message),
		Condition:   "time",
		Action:      args.Message,
		ChannelID:   channelID,
		UserID:      userID,
		NextCheck:   dueAt,
	}
	if err := t.store.Create(ctx, in); err != nil {
		return pkg.ToolResult{}, fmt.Errorf("schedule_reminder: create intent: %w", err)
	}

	return pkg.ToolResult{
		ID:      call.ID,
		Content: fmt.Sprintf("Reminder scheduled for %s UTC: \"%s\"", dueAt.UTC().Format("2006-01-02 15:04:05"), args.Message),
	}, nil
}
