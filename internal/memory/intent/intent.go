// Package intent provides persistent intent storage with status lifecycle management.
package intent

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jrimmer/chandra/store"
)

// IntentStatus represents the lifecycle state of an intent.
type IntentStatus string

const (
	IntentActive    IntentStatus = "active"
	IntentPaused    IntentStatus = "paused"
	IntentCompleted IntentStatus = "completed"
)

// Intent represents a persistent agent intent with scheduling metadata.
type Intent struct {
	ID          string
	Description string
	Condition   string
	Action      string
	Status      IntentStatus
	CreatedAt   time.Time
	LastChecked time.Time
	NextCheck   time.Time
	// Delivery target: where to send the response when this intent fires.
	ChannelID string
	UserID    string
}

// IntentStore defines the persistence contract for intents.
type IntentStore interface {
	Create(ctx context.Context, intent Intent) error
	Update(ctx context.Context, intent Intent) error
	Active(ctx context.Context) ([]Intent, error)
	Due(ctx context.Context) ([]Intent, error)
	Complete(ctx context.Context, id string) error
}

// Compile-time assertion that *Store satisfies IntentStore.
var _ IntentStore = (*Store)(nil)

// Store is an intent store backed by SQLite.
type Store struct {
	db *sql.DB
}

// NewStore returns a new Store using the provided database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create inserts a new intent with IntentActive and NextCheck set to now.
// If intent.ID is empty a new ULID is generated. Timestamps are always set
// to the current time regardless of what is in the provided intent value.
func (s *Store) Create(ctx context.Context, intent Intent) error {
	now := time.Now().UTC()
	id := intent.ID
	if id == "" {
		id = store.NewID()
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO intents (id, description, condition, action, status, created_at, last_checked, next_check, channel_id, user_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		intent.Description,
		intent.Condition,
		intent.Action,
		string(IntentActive),
		now.UnixMilli(),
		now.UnixMilli(),
		now.UnixMilli(),
		intent.ChannelID,
		intent.UserID,
	)
	if err != nil {
		return fmt.Errorf("intent: create: %w", err)
	}
	return nil
}

// Update modifies all mutable fields of an existing intent.
func (s *Store) Update(ctx context.Context, intent Intent) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE intents
		 SET description = ?, condition = ?, action = ?, status = ?,
		     last_checked = ?, next_check = ?
		 WHERE id = ?`,
		intent.Description,
		intent.Condition,
		intent.Action,
		string(intent.Status),
		intent.LastChecked.UnixMilli(),
		intent.NextCheck.UnixMilli(),
		intent.ID,
	)
	if err != nil {
		return fmt.Errorf("intent: update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("intent: update rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("intent: update: no intent with id %q", intent.ID)
	}
	return nil
}

// Active returns all intents with IntentActive.
func (s *Store) Active(ctx context.Context) ([]Intent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, description, condition, action, status, created_at, last_checked, next_check, channel_id, user_id
	 FROM intents
		 WHERE status = ?`,
		string(IntentActive),
	)
	if err != nil {
		return nil, fmt.Errorf("intent: active: %w", err)
	}
	defer rows.Close()

	return scanIntents(rows)
}

// Due returns all active intents whose next_check is at or before now.
func (s *Store) Due(ctx context.Context) ([]Intent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, description, condition, action, status, created_at, last_checked, next_check, channel_id, user_id
	 FROM intents
		 WHERE status = ? AND next_check <= ?`,
		string(IntentActive),
		time.Now().UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("intent: due: %w", err)
	}
	defer rows.Close()

	return scanIntents(rows)
}

// Complete sets the status of the identified intent to IntentCompleted.
func (s *Store) Complete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE intents SET status = ? WHERE id = ?`,
		string(IntentCompleted),
		id,
	)
	if err != nil {
		return fmt.Errorf("intent: complete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("intent: complete rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("intent: complete: no intent with id %q", id)
	}
	return nil
}

// scanIntents reads all rows from a query result into a slice of Intent values.
// last_checked and next_check are nullable in the schema so we use sql.NullInt64.
func scanIntents(rows *sql.Rows) ([]Intent, error) {
	var intents []Intent
	for rows.Next() {
		var in Intent
		var statusStr string
		var createdAtMs int64
		var lastCheckedMs, nextCheckMs sql.NullInt64

		if err := rows.Scan(
			&in.ID,
			&in.Description,
			&in.Condition,
			&in.Action,
			&statusStr,
			&createdAtMs,
			&lastCheckedMs,
			&nextCheckMs,
			&in.ChannelID,
			&in.UserID,
		); err != nil {
			return nil, fmt.Errorf("intent: scan row: %w", err)
		}

		in.Status = IntentStatus(statusStr)
		in.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		if lastCheckedMs.Valid {
			in.LastChecked = time.UnixMilli(lastCheckedMs.Int64).UTC()
		}
		if nextCheckMs.Valid {
			in.NextCheck = time.UnixMilli(nextCheckMs.Int64).UTC()
		}

		intents = append(intents, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("intent: iterate rows: %w", err)
	}
	return intents, nil
}
