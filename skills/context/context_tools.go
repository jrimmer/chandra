package context_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jrimmer/chandra/internal/memory/identity"
	"github.com/jrimmer/chandra/pkg"
)

// NoteContext implements pkg.Tool, allowing Chandra to add an item to the
// RelationshipState.OngoingContext list — the "active threads" Chandra is
// tracking for the user.
//
// Design intent: OngoingContext is displayed in every system prompt so Chandra
// stays aware of what the user is currently working on, planning, or tracking.
// It is populated by Chandra calling this tool when it detects something worth
// remembering as an active thread (e.g., "user is planning a trip to Tokyo",
// "waiting for invoice from Acme Corp").
type NoteContext struct {
	store identity.IdentityStore
}

var _ pkg.Tool = (*NoteContext)(nil)

// NewNoteContext returns a new NoteContext tool backed by the given identity store.
func NewNoteContext(store identity.IdentityStore) *NoteContext {
	return &NoteContext{store: store}
}

func (t *NoteContext) Definition() pkg.ToolDef {
	params := json.RawMessage(`{
		"type": "object",
		"properties": {
			"item": {
				"type": "string",
				"description": "A concise phrase describing the active thread or context item to track (e.g. \"user planning trip to Tokyo next month\")"
			}
		},
		"required": ["item"]
	}`)
	return pkg.ToolDef{
		Name:         "note_context",
		Description:  "Add an active context item to track (e.g. ongoing projects, things to follow up on). Use when the user mentions something they are working on, planning, or waiting for.",
		Tier:         pkg.TierBuiltin,
		Capabilities: []pkg.Capability{},
		Parameters:   params,
	}
}

type noteContextParams struct {
	Item string `json:"item"`
}

func (t *NoteContext) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var p noteContextParams
	if err := json.Unmarshal(call.Parameters, &p); err != nil {
		return errResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error(), err), nil
	}
	p.Item = strings.TrimSpace(p.Item)
	if p.Item == "" {
		return errResult(call.ID, pkg.ErrBadInput, "item must not be empty", nil), nil
	}

	rel, err := t.store.Relationship()
	if err != nil {
		// No relationship row yet — start with an empty state.
		rel = identity.RelationshipState{TrustLevel: 3, CommunicationStyle: "concise"}
	}

	// Deduplicate: don't add if a very similar item already exists.
	lower := strings.ToLower(p.Item)
	for _, existing := range rel.OngoingContext {
		if strings.ToLower(existing) == lower {
			return pkg.ToolResult{
				ID:      call.ID,
				Content: fmt.Sprintf("already tracking: %q", existing),
			}, nil
		}
	}

	rel.OngoingContext = append(rel.OngoingContext, p.Item)
	if err := t.store.UpdateRelationship(ctx, rel); err != nil {
		return errResult(call.ID, pkg.ErrInternal, "failed to save context: "+err.Error(), err), nil
	}

	return pkg.ToolResult{
		ID:      call.ID,
		Content: fmt.Sprintf("noted: %q (tracking %d item(s))", p.Item, len(rel.OngoingContext)),
	}, nil
}

// ForgetContext removes an item from OngoingContext by exact or substring match.
type ForgetContext struct {
	store identity.IdentityStore
}

var _ pkg.Tool = (*ForgetContext)(nil)

// NewForgetContext returns a new ForgetContext tool backed by the given identity store.
func NewForgetContext(store identity.IdentityStore) *ForgetContext {
	return &ForgetContext{store: store}
}

func (t *ForgetContext) Definition() pkg.ToolDef {
	params := json.RawMessage(`{
		"type": "object",
		"properties": {
			"item": {
				"type": "string",
				"description": "The context item to remove. Use the exact phrase or a substring of it."
			}
		},
		"required": ["item"]
	}`)
	return pkg.ToolDef{
		Name:         "forget_context",
		Description:  "Remove an active context item that is no longer relevant (e.g. the trip was completed, the invoice was paid). Use when something tracked is resolved or the user asks to drop it.",
		Tier:         pkg.TierBuiltin,
		Capabilities: []pkg.Capability{},
		Parameters:   params,
	}
}

type forgetContextParams struct {
	Item string `json:"item"`
}

func (t *ForgetContext) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var p forgetContextParams
	if err := json.Unmarshal(call.Parameters, &p); err != nil {
		return errResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error(), err), nil
	}
	p.Item = strings.TrimSpace(p.Item)
	if p.Item == "" {
		return errResult(call.ID, pkg.ErrBadInput, "item must not be empty", nil), nil
	}

	rel, err := t.store.Relationship()
	if err != nil {
		// No relationship row yet means nothing is being tracked — treat as no-match.
		return pkg.ToolResult{
			ID:      call.ID,
			Content: fmt.Sprintf("no context item matching %q found (nothing currently tracked)", p.Item),
		}, nil
	}

	lower := strings.ToLower(p.Item)
	kept := rel.OngoingContext[:0]
	removed := 0
	for _, existing := range rel.OngoingContext {
		if strings.Contains(strings.ToLower(existing), lower) {
			removed++
		} else {
			kept = append(kept, existing)
		}
	}

	if removed == 0 {
		return pkg.ToolResult{
			ID:      call.ID,
			Content: fmt.Sprintf("no context item matching %q found (current: %v)", p.Item, rel.OngoingContext),
		}, nil
	}

	rel.OngoingContext = kept
	if err := t.store.UpdateRelationship(ctx, rel); err != nil {
		return errResult(call.ID, pkg.ErrInternal, "failed to save context: "+err.Error(), err), nil
	}

	return pkg.ToolResult{
		ID:      call.ID,
		Content: fmt.Sprintf("removed %d item(s) matching %q (%d remaining)", removed, p.Item, len(kept)),
	}, nil
}

// errResult is a helper that builds a ToolResult with an error payload.
func errResult(id string, kind pkg.ToolErrorKind, msg string, cause error) pkg.ToolResult {
	return pkg.ToolResult{
		ID: id,
		Error: &pkg.ToolError{
			Kind:    kind,
			Message: msg,
			Cause:   cause,
		},
	}
}
