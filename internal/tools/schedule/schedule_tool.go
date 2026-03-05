// Package scheduletool provides the schedule_reminder built-in tool.
// It allows the LLM to create one-shot or recurring reminders that fire at a
// specified time and deliver a message back to the originating channel and user.
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
		Description: "Schedule a one-shot or recurring reminder to be delivered to the user. Use when the user asks to be reminded of something later, or to repeat a check/update on a regular interval.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": {
					"type": "string",
					"description": "The reminder message to deliver to the user."
				},
				"due_at": {
					"type": "string",
					"description": "ISO 8601 UTC timestamp when the reminder should first fire, e.g. \"2026-03-05T04:20:00Z\"."
				},
				"interval": {
					"type": "string",
					"description": "Optional. Recurrence interval as a Go duration string: \"1h\", \"30m\", \"24h\", \"7d\". If provided, the reminder repeats on this schedule after each fire. Omit for one-shot reminders."
				}
			},
			"required": ["message", "due_at"]
		}`),
	}
}

func (t *scheduleReminderTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		Message  string `json:"message"`
		DueAt    string `json:"due_at"`
		Interval string `json:"interval"`
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

	// Parse optional recurrence interval.
	var recurrenceInterval time.Duration
	if args.Interval != "" {
		d, parseErr := parseInterval(args.Interval)
		if parseErr != nil {
			return pkg.ToolResult{ID: call.ID, Content: fmt.Sprintf("error: invalid interval %q — use Go duration format like \"1h\", \"30m\", \"24h\", \"168h\" (7 days): %v", args.Interval, parseErr)}, nil
		}
		if d < time.Minute {
			return pkg.ToolResult{ID: call.ID, Content: "error: interval must be at least 1 minute"}, nil
		}
		recurrenceInterval = d
	}

	channelID, _ := ctx.Value(KeyChannelID).(string)
	userID, _ := ctx.Value(KeyUserID).(string)
	if channelID == "" || userID == "" {
		return pkg.ToolResult{ID: call.ID, Content: "error: delivery context not available (internal error)"}, nil
	}

	description := fmt.Sprintf("Reminder: %s", args.Message)
	if recurrenceInterval > 0 {
		description = fmt.Sprintf("Recurring reminder every %s: %s", args.Interval, args.Message)
	}
	in := intent.Intent{
		Description:        description,
		Condition:          "time",
		Action:             args.Message,
		ChannelID:          channelID,
		UserID:             userID,
		NextCheck:          dueAt,
		RecurrenceInterval: recurrenceInterval,
	}
	if err := t.store.Create(ctx, in); err != nil {
		return pkg.ToolResult{}, fmt.Errorf("schedule_reminder: create intent: %w", err)
	}

	confirmMsg := fmt.Sprintf("Reminder scheduled for %s UTC: \"%s\"", dueAt.UTC().Format("2006-01-02 15:04:05"), args.Message)
	if recurrenceInterval > 0 {
		confirmMsg += fmt.Sprintf(" (repeats every %s)", args.Interval)
	}
	return pkg.ToolResult{
		ID:      call.ID,
		Content: confirmMsg,
	}, nil
}
// parseInterval parses a human-friendly duration string, extending Go's
// standard time.ParseDuration with "d" (day) and "w" (week) suffixes.
func parseInterval(s string) (time.Duration, error) {
	// Replace day/week suffixes before standard parsing.
	repl := s
	for len(repl) > 0 {
		if repl[len(repl)-1] == 'd' {
			// Treat trailing 'd' as 24h.
			repl = repl[:len(repl)-1] + "h"
			// Multiply: parse "Nh" and scale by 24.
			d, err := time.ParseDuration(repl)
			if err != nil {
				return 0, err
			}
			return d * 24, nil
		}
		if repl[len(repl)-1] == 'w' {
			// Treat trailing 'w' as 168h.
			repl = repl[:len(repl)-1] + "h"
			d, err := time.ParseDuration(repl)
			if err != nil {
				return 0, err
			}
			return d * 168, nil
		}
		break
	}
	return time.ParseDuration(s)
}
