package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AgentProfile holds Chandra's identity configuration.
type AgentProfile struct {
	Name         string
	Persona      string
	Traits       []string
	Capabilities []string
}

// UserProfile holds the profile of the user the agent serves.
type UserProfile struct {
	ID          string
	Name        string
	Timezone    string
	Preferences map[string]string
	Notes       string
}

// RelationshipState captures the state of the agent-user relationship.
type RelationshipState struct {
	TrustLevel         int       // 1–5
	CommunicationStyle string    // "concise" | "detailed" | "casual"
	OngoingContext     []string  // max 20 items; oldest are dropped when exceeded
	LastInteraction    time.Time
}

const (
	agentID       = "chandra"
	maxContextLen = 20
)

// Store provides persistent access to agent identity, user profile, and
// relationship state for a single agent-user pair.
type Store struct {
	db     *sql.DB
	userID string
}

// NewStore returns a new Store bound to the given user ID.
func NewStore(db *sql.DB, userID string) *Store {
	return &Store{db: db, userID: userID}
}

// SetAgent upserts the agent profile for id='chandra'.
func (s *Store) SetAgent(ctx context.Context, profile AgentProfile) error {
	traits, err := json.Marshal(profile.Traits)
	if err != nil {
		return fmt.Errorf("identity: marshal traits: %w", err)
	}
	capabilities, err := json.Marshal(profile.Capabilities)
	if err != nil {
		return fmt.Errorf("identity: marshal capabilities: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO agent_profile (id, name, persona, traits, capabilities)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name         = excluded.name,
		   persona      = excluded.persona,
		   traits       = excluded.traits,
		   capabilities = excluded.capabilities`,
		agentID,
		profile.Name,
		profile.Persona,
		string(traits),
		string(capabilities),
	)
	if err != nil {
		return fmt.Errorf("identity: set agent: %w", err)
	}
	return nil
}

// Agent returns the agent profile. Returns an error wrapping sql.ErrNoRows
// if no profile exists.
func (s *Store) Agent() (AgentProfile, error) {
	var profile AgentProfile
	var traitsRaw, capsRaw string

	err := s.db.QueryRow(
		`SELECT name, persona, traits, capabilities FROM agent_profile WHERE id = ?`,
		agentID,
	).Scan(&profile.Name, &profile.Persona, &traitsRaw, &capsRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentProfile{}, fmt.Errorf("identity: agent profile not found: %w", sql.ErrNoRows)
	}
	if err != nil {
		return AgentProfile{}, fmt.Errorf("identity: get agent: %w", err)
	}

	if err := json.Unmarshal([]byte(traitsRaw), &profile.Traits); err != nil {
		return AgentProfile{}, fmt.Errorf("identity: unmarshal traits: %w", err)
	}
	if err := json.Unmarshal([]byte(capsRaw), &profile.Capabilities); err != nil {
		return AgentProfile{}, fmt.Errorf("identity: unmarshal capabilities: %w", err)
	}
	return profile, nil
}

// SetUser upserts the user profile for the configured user ID.
func (s *Store) SetUser(ctx context.Context, profile UserProfile) error {
	var prefsJSON []byte
	if profile.Preferences != nil {
		var err error
		prefsJSON, err = json.Marshal(profile.Preferences)
		if err != nil {
			return fmt.Errorf("identity: marshal preferences: %w", err)
		}
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_profile (id, name, timezone, preferences, notes)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name        = excluded.name,
		   timezone    = excluded.timezone,
		   preferences = excluded.preferences,
		   notes       = excluded.notes`,
		profile.ID,
		profile.Name,
		profile.Timezone,
		prefsJSON,
		profile.Notes,
	)
	if err != nil {
		return fmt.Errorf("identity: set user: %w", err)
	}
	return nil
}

// User returns the user profile for the configured user ID.
func (s *Store) User() (UserProfile, error) {
	var profile UserProfile
	var prefsRaw []byte
	var notes sql.NullString

	err := s.db.QueryRow(
		`SELECT id, name, timezone, preferences, notes FROM user_profile WHERE id = ?`,
		s.userID,
	).Scan(&profile.ID, &profile.Name, &profile.Timezone, &prefsRaw, &notes)
	if errors.Is(err, sql.ErrNoRows) {
		return UserProfile{}, fmt.Errorf("identity: user profile not found: %w", sql.ErrNoRows)
	}
	if err != nil {
		return UserProfile{}, fmt.Errorf("identity: get user: %w", err)
	}

	if prefsRaw != nil {
		if err := json.Unmarshal(prefsRaw, &profile.Preferences); err != nil {
			return UserProfile{}, fmt.Errorf("identity: unmarshal preferences: %w", err)
		}
	}
	if notes.Valid {
		profile.Notes = notes.String
	}
	return profile, nil
}

// UpdateRelationship upserts the relationship state between the agent and the
// configured user. OngoingContext is capped at 20 items; the oldest are dropped
// when exceeded.
func (s *Store) UpdateRelationship(ctx context.Context, state RelationshipState) error {
	ctx2 := state.OngoingContext
	if len(ctx2) > maxContextLen {
		ctx2 = ctx2[len(ctx2)-maxContextLen:]
	}

	ctxJSON, err := json.Marshal(ctx2)
	if err != nil {
		return fmt.Errorf("identity: marshal ongoing_context: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO relationship_state
		   (agent_id, user_id, trust_level, communication_style, ongoing_context, last_interaction)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(agent_id, user_id) DO UPDATE SET
		   trust_level         = excluded.trust_level,
		   communication_style = excluded.communication_style,
		   ongoing_context     = excluded.ongoing_context,
		   last_interaction    = excluded.last_interaction`,
		agentID,
		s.userID,
		state.TrustLevel,
		state.CommunicationStyle,
		string(ctxJSON),
		state.LastInteraction.Unix(),
	)
	if err != nil {
		return fmt.Errorf("identity: update relationship: %w", err)
	}
	return nil
}

// Relationship returns the relationship state for the agent and configured user.
func (s *Store) Relationship() (RelationshipState, error) {
	var state RelationshipState
	var ctxRaw string
	var lastInteraction int64
	var style sql.NullString

	err := s.db.QueryRow(
		`SELECT trust_level, communication_style, ongoing_context, last_interaction
		 FROM relationship_state
		 WHERE agent_id = ? AND user_id = ?`,
		agentID, s.userID,
	).Scan(&state.TrustLevel, &style, &ctxRaw, &lastInteraction)
	if errors.Is(err, sql.ErrNoRows) {
		return RelationshipState{}, fmt.Errorf("identity: relationship not found: %w", sql.ErrNoRows)
	}
	if err != nil {
		return RelationshipState{}, fmt.Errorf("identity: get relationship: %w", err)
	}

	if style.Valid {
		state.CommunicationStyle = style.String
	}

	if ctxRaw != "" {
		if err := json.Unmarshal([]byte(ctxRaw), &state.OngoingContext); err != nil {
			return RelationshipState{}, fmt.Errorf("identity: unmarshal ongoing_context: %w", err)
		}
	} else {
		state.OngoingContext = []string{}
	}

	state.LastInteraction = time.Unix(lastInteraction, 0).UTC()
	return state, nil
}
