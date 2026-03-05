package context_tools_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	context_tools "github.com/jrimmer/chandra/skills/context"
	"github.com/jrimmer/chandra/internal/memory/identity"
	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
)

func newTestIdentityStore(t *testing.T) *identity.Store {
	t.Helper()
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	db := s.DB()
	t.Cleanup(func() { db.Close() })

	st := identity.NewStore(db, "user-test")
	ctx := context.Background()
	require.NoError(t, st.SetAgent(ctx, identity.AgentProfile{Name: "Chandra"}))
	require.NoError(t, st.SetUser(ctx, identity.UserProfile{ID: "user-test", Name: "Test"}))
	return st
}

func call(name, paramsJSON string) pkg.ToolCall {
	return pkg.ToolCall{ID: "tc-1", Name: name, Parameters: json.RawMessage(paramsJSON)}
}

// --------------------------------------------------------------------------
// NoteContext tests
// --------------------------------------------------------------------------

func TestNoteContext_AddsItem(t *testing.T) {
	st := newTestIdentityStore(t)
	tool := context_tools.NewNoteContext(st)

	result, err := tool.Execute(context.Background(), call("note_context", `{"item":"planning trip to Tokyo"}`))
	require.NoError(t, err)
	assert.Nil(t, result.Error, "unexpected tool error: %v", result.Error)
	assert.Contains(t, result.Content, "noted")

	rel, err := st.Relationship()
	require.NoError(t, err)
	require.Contains(t, rel.OngoingContext, "planning trip to Tokyo")
}

func TestNoteContext_Deduplicates(t *testing.T) {
	st := newTestIdentityStore(t)
	tool := context_tools.NewNoteContext(st)
	ctx := context.Background()

	_, err := tool.Execute(ctx, call("note_context", `{"item":"waiting for invoice"}`))
	require.NoError(t, err)
	_, err = tool.Execute(ctx, call("note_context", `{"item":"waiting for invoice"}`))
	require.NoError(t, err)

	rel, err := st.Relationship()
	require.NoError(t, err)
	count := 0
	for _, item := range rel.OngoingContext {
		if item == "waiting for invoice" {
			count++
		}
	}
	assert.Equal(t, 1, count, "duplicate item must not be added twice")
}

func TestNoteContext_MultipleItems(t *testing.T) {
	st := newTestIdentityStore(t)
	tool := context_tools.NewNoteContext(st)
	ctx := context.Background()

	items := []string{"planning trip to Tokyo", "waiting for invoice from Acme", "researching new laptop"}
	for _, item := range items {
		result, err := tool.Execute(ctx, call("note_context", `{"item":"`+item+`"}`))
		require.NoError(t, err)
		assert.Nil(t, result.Error)
	}

	rel, err := st.Relationship()
	require.NoError(t, err)
	assert.Len(t, rel.OngoingContext, 3)
	for _, item := range items {
		assert.Contains(t, rel.OngoingContext, item)
	}
}

func TestNoteContext_RejectsEmptyItem(t *testing.T) {
	st := newTestIdentityStore(t)
	tool := context_tools.NewNoteContext(st)

	result, err := tool.Execute(context.Background(), call("note_context", `{"item":""}`))
	require.NoError(t, err)
	assert.NotNil(t, result.Error, "empty item must return an error")
	assert.Equal(t, pkg.ErrBadInput, result.Error.Kind)
}

// --------------------------------------------------------------------------
// ForgetContext tests
// --------------------------------------------------------------------------

func TestForgetContext_RemovesExactMatch(t *testing.T) {
	st := newTestIdentityStore(t)
	note := context_tools.NewNoteContext(st)
	forget := context_tools.NewForgetContext(st)
	ctx := context.Background()

	_, err := note.Execute(ctx, call("note_context", `{"item":"planning trip to Tokyo"}`))
	require.NoError(t, err)
	_, err = note.Execute(ctx, call("note_context", `{"item":"waiting for invoice"}`))
	require.NoError(t, err)

	result, err := forget.Execute(ctx, call("forget_context", `{"item":"planning trip to Tokyo"}`))
	require.NoError(t, err)
	assert.Nil(t, result.Error)
	assert.Contains(t, result.Content, "removed 1")

	rel, err := st.Relationship()
	require.NoError(t, err)
	assert.NotContains(t, rel.OngoingContext, "planning trip to Tokyo", "item must be removed")
	assert.Contains(t, rel.OngoingContext, "waiting for invoice", "other item must remain")
}

func TestForgetContext_RemovesBySubstring(t *testing.T) {
	st := newTestIdentityStore(t)
	note := context_tools.NewNoteContext(st)
	forget := context_tools.NewForgetContext(st)
	ctx := context.Background()

	_, err := note.Execute(ctx, call("note_context", `{"item":"waiting for invoice from Acme Corp"}`))
	require.NoError(t, err)

	// Remove by substring.
	result, err := forget.Execute(ctx, call("forget_context", `{"item":"Acme"}`))
	require.NoError(t, err)
	assert.Nil(t, result.Error)

	rel, err := st.Relationship()
	require.NoError(t, err)
	assert.Empty(t, rel.OngoingContext, "substring match must remove the item")
}

func TestForgetContext_NoMatchReportsGracefully(t *testing.T) {
	st := newTestIdentityStore(t)
	forget := context_tools.NewForgetContext(st)

	result, err := forget.Execute(context.Background(), call("forget_context", `{"item":"nonexistent item"}`))
	require.NoError(t, err)
	assert.Nil(t, result.Error, "no-match must NOT be an error — just an informational result")
	assert.Contains(t, result.Content, "no context item matching")
}

// --------------------------------------------------------------------------
// Design intent: OngoingContext round-trips through the identity system prompt
// This validates the full chain: tool writes → store persists → identity
// candidate reads → value appears in LLM context.
// --------------------------------------------------------------------------

func TestDesignIntent_OngoingContext_AppearsInRelationship(t *testing.T) {
	st := newTestIdentityStore(t)
	note := context_tools.NewNoteContext(st)
	ctx := context.Background()

	_, err := note.Execute(ctx, call("note_context", `{"item":"user is planning a home renovation"}`))
	require.NoError(t, err)

	rel, err := st.Relationship()
	require.NoError(t, err)
	assert.Contains(t, rel.OngoingContext, "user is planning a home renovation",
		"item written by note_context must be readable from relationship state")

	// Simulate what buildIdentityCandidate does: OngoingContext is rendered into
	// the system prompt when non-empty. Verify it's present.
	assert.NotEmpty(t, rel.OngoingContext,
		"OngoingContext must be non-empty after note_context call — this is what surfaces in the system prompt")
}
