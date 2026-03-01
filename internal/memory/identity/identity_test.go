package identity_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/memory/identity"
	"github.com/jrimmer/chandra/store"
)

// newTestDB creates a temporary database with migrations applied.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	db := s.DB()
	t.Cleanup(func() { db.Close() })
	return db
}

func TestIdentityStore_AgentProfile(t *testing.T) {
	db := newTestDB(t)
	st := identity.NewStore(db, "default")
	ctx := context.Background()

	profile := identity.AgentProfile{
		Name:         "Chandra",
		Persona:      "A helpful, thoughtful AI assistant",
		Traits:       []string{"curious", "precise", "warm"},
		Capabilities: []string{"code", "research", "writing"},
	}

	require.NoError(t, st.SetAgent(ctx, profile))

	got, err := st.Agent()
	require.NoError(t, err)

	assert.Equal(t, profile.Name, got.Name)
	assert.Equal(t, profile.Persona, got.Persona)
	assert.Equal(t, profile.Traits, got.Traits)
	assert.Equal(t, profile.Capabilities, got.Capabilities)
}

func TestIdentityStore_AgentProfile_Upsert(t *testing.T) {
	db := newTestDB(t)
	st := identity.NewStore(db, "default")
	ctx := context.Background()

	first := identity.AgentProfile{
		Name:         "Chandra v1",
		Persona:      "Original persona",
		Traits:       []string{"trait-a"},
		Capabilities: []string{"cap-a"},
	}
	require.NoError(t, st.SetAgent(ctx, first))

	second := identity.AgentProfile{
		Name:         "Chandra v2",
		Persona:      "Updated persona",
		Traits:       []string{"trait-a", "trait-b"},
		Capabilities: []string{"cap-a", "cap-b"},
	}
	require.NoError(t, st.SetAgent(ctx, second))

	got, err := st.Agent()
	require.NoError(t, err)

	assert.Equal(t, second.Name, got.Name)
	assert.Equal(t, second.Persona, got.Persona)
	assert.Equal(t, second.Traits, got.Traits)
	assert.Equal(t, second.Capabilities, got.Capabilities)

	// Confirm there is exactly one row in agent_profile.
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM agent_profile").Scan(&count))
	assert.Equal(t, 1, count, "upsert must not create a duplicate row")
}

func TestIdentityStore_UserProfile(t *testing.T) {
	db := newTestDB(t)
	st := identity.NewStore(db, "user-42")
	ctx := context.Background()

	profile := identity.UserProfile{
		ID:       "user-42",
		Name:     "Jane",
		Timezone: "America/New_York",
		Preferences: map[string]string{
			"theme":    "dark",
			"language": "en",
		},
		Notes: "Prefers bullet points",
	}

	require.NoError(t, st.SetUser(ctx, profile))

	got, err := st.User()
	require.NoError(t, err)

	assert.Equal(t, profile.ID, got.ID)
	assert.Equal(t, profile.Name, got.Name)
	assert.Equal(t, profile.Timezone, got.Timezone)
	assert.Equal(t, profile.Preferences, got.Preferences)
	assert.Equal(t, profile.Notes, got.Notes)
}

func TestIdentityStore_Relationship(t *testing.T) {
	db := newTestDB(t)
	st := identity.NewStore(db, "user-42")
	ctx := context.Background()

	// Agent and user profiles must exist before inserting relationship_state
	// due to FK constraints.
	agentProfile := identity.AgentProfile{
		Name:         "Chandra",
		Persona:      "Thoughtful assistant",
		Traits:       []string{"helpful"},
		Capabilities: []string{"code"},
	}
	require.NoError(t, st.SetAgent(ctx, agentProfile))

	userProfile := identity.UserProfile{
		ID:       "user-42",
		Name:     "Jane",
		Timezone: "UTC",
	}
	require.NoError(t, st.SetUser(ctx, userProfile))

	now := time.Now().Truncate(time.Second).UTC()
	state := identity.RelationshipState{
		TrustLevel:         4,
		CommunicationStyle: "detailed",
		OngoingContext:     []string{"working on project X", "prefers morning meetings"},
		LastInteraction:    now,
	}

	require.NoError(t, st.UpdateRelationship(ctx, state))

	got, err := st.Relationship()
	require.NoError(t, err)

	assert.Equal(t, state.TrustLevel, got.TrustLevel)
	assert.Equal(t, state.CommunicationStyle, got.CommunicationStyle)
	assert.Equal(t, state.OngoingContext, got.OngoingContext)
	assert.Equal(t, state.LastInteraction.Unix(), got.LastInteraction.Unix())
}

func TestIdentityStore_OngoingContext_MaxItems(t *testing.T) {
	db := newTestDB(t)
	st := identity.NewStore(db, "user-42")
	ctx := context.Background()

	agentProfile := identity.AgentProfile{
		Name:         "Chandra",
		Persona:      "Thoughtful assistant",
		Traits:       []string{"helpful"},
		Capabilities: []string{"code"},
	}
	require.NoError(t, st.SetAgent(ctx, agentProfile))

	userProfile := identity.UserProfile{
		ID:       "user-42",
		Name:     "Jane",
		Timezone: "UTC",
	}
	require.NoError(t, st.SetUser(ctx, userProfile))

	// Build 25 context items.
	items := make([]string, 25)
	for i := range items {
		items[i] = fmt.Sprintf("context item %d", i)
	}

	state := identity.RelationshipState{
		TrustLevel:         3,
		CommunicationStyle: "concise",
		OngoingContext:     items,
		LastInteraction:    time.Now().UTC(),
	}
	require.NoError(t, st.UpdateRelationship(ctx, state))

	got, err := st.Relationship()
	require.NoError(t, err)

	assert.Len(t, got.OngoingContext, 20, "OngoingContext must be capped at 20 items")

	// Oldest items (indices 0–4) should be dropped; newest 20 (indices 5–24) kept.
	for i, item := range got.OngoingContext {
		assert.Equal(t, fmt.Sprintf("context item %d", i+5), item,
			"item at index %d should be 'context item %d'", i, i+5)
	}
}
