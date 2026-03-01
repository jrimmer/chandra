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
	StatusActive    IntentStatus = "active"
	StatusPaused    IntentStatus = "paused"
	StatusCompleted IntentStatus = "completed"
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
}

// IntentStore defines the persistence contract for intents.
type IntentStore interface {
	Create(ctx context.Context, description, condition, action string) (*Intent, error)
	Update(ctx context.Context, intent *Intent) error
	Active(ctx context.Context) ([]*Intent, error)
	Due(ctx context.Context) ([]*Intent, error)
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

// Create inserts a new intent with StatusActive and NextCheck set to now.
func (s *Store) Create(ctx context.Context, description, condition, action string) (*Intent, error) {
	now := time.Now().UTC()
	id := store.NewID()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO intents (id, description, condition, action, status, created_at, last_checked, next_check)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		description,
		condition,
		action,
		string(StatusActive),
		now.UnixMilli(),
		now.UnixMilli(),
		now.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("intent: create: %w", err)
	}

	return &Intent{
		ID:          id,
		Description: description,
		Condition:   condition,
		Action:      action,
		Status:      StatusActive,
		CreatedAt:   now,
		LastChecked: now,
		NextCheck:   now,
	}, nil
}

// Update modifies all mutable fields of an existing intent.
func (s *Store) Update(ctx context.Context, intent *Intent) error {
	_, err := s.db.ExecContext(ctx,
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
	return nil
}

// Active returns all intents with StatusActive.
func (s *Store) Active(ctx context.Context) ([]*Intent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, description, condition, action, status, created_at, last_checked, next_check
		 FROM intents
		 WHERE status = ?`,
		string(StatusActive),
	)
	if err != nil {
		return nil, fmt.Errorf("intent: active: %w", err)
	}
	defer rows.Close()

	return scanIntents(rows)
}

// Due returns all active intents whose next_check is at or before now.
func (s *Store) Due(ctx context.Context) ([]*Intent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, description, condition, action, status, created_at, last_checked, next_check
		 FROM intents
		 WHERE status = ? AND next_check <= ?`,
		string(StatusActive),
		time.Now().UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("intent: due: %w", err)
	}
	defer rows.Close()

	return scanIntents(rows)
}

// Complete sets the status of the identified intent to StatusCompleted.
func (s *Store) Complete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE intents SET status = ? WHERE id = ?`,
		string(StatusCompleted),
		id,
	)
	if err != nil {
		return fmt.Errorf("intent: complete: %w", err)
	}
	return nil
}

// scanIntents reads all rows from a query result into a slice of Intent pointers.
// last_checked and next_check are nullable in the schema so we use sql.NullInt64.
func scanIntents(rows *sql.Rows) ([]*Intent, error) {
	var intents []*Intent
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

		intents = append(intents, &in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("intent: iterate rows: %w", err)
	}
	return intents, nil
}
