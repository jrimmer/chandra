package scheduletool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/pkg"
)

type listRemindersTool struct {
	store intent.IntentStore
}

// NewListRemindersTool returns a pkg.Tool that lists the agent's active scheduled intents.
func NewListRemindersTool(store intent.IntentStore) pkg.Tool {
	return &listRemindersTool{store: store}
}

func (t *listRemindersTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name:        "list_reminders",
		Description: "List all active scheduled reminders and recurring jobs. Use this to answer questions about what is currently scheduled, whether a heartbeat or recurring check is configured, and when the next fire time is.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {},
			"required": []
		}`),
	}
}

type reminderEntry struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	// Condition distinguishes regular reminders from skill crons and other system intents.
	Condition string `json:"condition,omitempty"`
	// NextFireUTC is when this intent will next fire.
	NextFireUTC string `json:"next_fire_utc"`
	// RecurringEvery is set for recurring intents, e.g. "30m0s".
	RecurringEvery string `json:"recurring_every,omitempty"`
	Action         string `json:"action,omitempty"`
	ChannelID      string `json:"channel_id,omitempty"`
}

func (t *listRemindersTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	active, err := t.store.Active(ctx)
	if err != nil {
		return pkg.ToolResult{ID: call.ID, Error: &pkg.ToolError{
			Kind: pkg.ErrInternal, Message: fmt.Sprintf("list_reminders: %v", err),
		}}, nil
	}

	if len(active) == 0 {
		return pkg.ToolResult{ID: call.ID, Content: `{"reminders":[],"count":0}`}, nil
	}

	entries := make([]reminderEntry, 0, len(active))
	for _, in := range active {
		e := reminderEntry{
			ID:          in.ID,
			Description: in.Description,
			Condition:   in.Condition,
			NextFireUTC: in.NextCheck.UTC().Format(time.RFC3339),
			Action:      in.Action,
			ChannelID:   in.ChannelID,
		}
		if in.RecurrenceInterval > 0 {
			e.RecurringEvery = in.RecurrenceInterval.String()
		}
		entries = append(entries, e)
	}

	out, err := json.Marshal(map[string]any{
		"reminders": entries,
		"count":     len(entries),
	})
	if err != nil {
		return pkg.ToolResult{ID: call.ID, Error: &pkg.ToolError{
			Kind: pkg.ErrInternal, Message: fmt.Sprintf("list_reminders: marshal: %v", err),
		}}, nil
	}
	return pkg.ToolResult{ID: call.ID, Content: string(out)}, nil
}
