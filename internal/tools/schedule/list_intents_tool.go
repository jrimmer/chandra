package scheduletool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/pkg"
)

// SkillCategoryLookup resolves the category declared in a skill's SKILL.md.
// Returns empty string if the skill is unknown.
type SkillCategoryLookup func(skillName string) string

type listIntentsTool struct {
	store          intent.IntentStore
	categoryLookup SkillCategoryLookup // may be nil
}

// NewListIntentsTool returns a pkg.Tool that lists active scheduled intents.
// categoryLookup is optional; pass nil to skip category resolution.
func NewListIntentsTool(store intent.IntentStore, categoryLookup SkillCategoryLookup) pkg.Tool {
	return &listIntentsTool{store: store, categoryLookup: categoryLookup}
}

func (t *listIntentsTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name: "list_intents",
		Description: "List active scheduled intents (reminders and recurring jobs). " +
			"Use to answer questions like 'what do I have scheduled?', 'do I have a heartbeat?', " +
			"'what recurring jobs are running?'. " +
			"Filters: kind (all|user|skill, default all), category (matches skill category, e.g. proactive).",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"kind": {
					"type": "string",
					"enum": ["all", "user", "skill"],
					"description": "Filter by intent kind. 'user' = created by schedule_reminder tool. 'skill' = created by a skill cron. 'all' = no filter.",
					"default": "all"
				},
				"category": {
					"type": "string",
					"description": "Optional. Only return skill intents whose skill has this category (e.g. 'proactive', 'monitoring'). Ignored unless kind is 'skill' or 'all'."
				}
			},
			"required": []
		}`),
	}
}

type intentEntry struct {
	ID             string `json:"id"`
	Description    string `json:"description"`
	Kind           string `json:"kind"`           // "user" or "skill"
	SkillName      string `json:"skill_name,omitempty"`
	SkillCategory  string `json:"skill_category,omitempty"`
	Condition      string `json:"condition,omitempty"`
	NextFireUTC    string `json:"next_fire_utc"`
	RecurringEvery string `json:"recurring_every,omitempty"`
	Action         string `json:"action,omitempty"`
	ChannelID      string `json:"channel_id,omitempty"`
}

func (t *listIntentsTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		Kind     string `json:"kind"`
		Category string `json:"category"`
	}
	if len(call.Parameters) > 0 {
		_ = json.Unmarshal(call.Parameters, &args)
	}
	if args.Kind == "" {
		args.Kind = "all"
	}

	active, err := t.store.Active(ctx)
	if err != nil {
		return pkg.ToolResult{ID: call.ID, Error: &pkg.ToolError{
			Kind:    pkg.ErrInternal,
			Message: fmt.Sprintf("list_intents: %v", err),
		}}, nil
	}

	entries := make([]intentEntry, 0, len(active))
	for _, in := range active {
		// Classify the intent.
		isSkillCron := strings.HasPrefix(in.Condition, "skill_cron:")
		kind := "user"
		skillName := ""
		skillCategory := ""
		if isSkillCron {
			kind = "skill"
			skillName = strings.TrimPrefix(in.Condition, "skill_cron:")
			if t.categoryLookup != nil {
				skillCategory = t.categoryLookup(skillName)
			}
		}

		// Apply kind filter.
		switch args.Kind {
		case "user":
			if kind != "user" {
				continue
			}
		case "skill":
			if kind != "skill" {
				continue
			}
		}

		// Apply category filter (only meaningful for skill intents).
		if args.Category != "" && skillCategory != args.Category {
			continue
		}

		e := intentEntry{
			ID:            in.ID,
			Description:   in.Description,
			Kind:          kind,
			SkillName:     skillName,
			SkillCategory: skillCategory,
			Condition:     in.Condition,
			NextFireUTC:   in.NextCheck.UTC().Format(time.RFC3339),
			Action:        in.Action,
			ChannelID:     in.ChannelID,
		}
		if in.RecurrenceInterval > 0 {
			e.RecurringEvery = in.RecurrenceInterval.String()
		}
		entries = append(entries, e)
	}

	out, _ := json.Marshal(map[string]any{
		"intents": entries,
		"count":   len(entries),
	})
	return pkg.ToolResult{ID: call.ID, Content: string(out)}, nil
}
