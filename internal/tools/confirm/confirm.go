// Package confirm implements an async confirmation gate for Tier 4 tool actions.
// It uses a dedicated SQLite table (tool_confirmations) to persist pending
// confirmation requests so they survive process restarts.
package confirm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jrimmer/chandra/store"
)

// ConfirmationStatus represents the lifecycle state of a confirmation request.
type ConfirmationStatus string

const (
	StatusPending  ConfirmationStatus = "pending"
	StatusApproved ConfirmationStatus = "approved"
	StatusRejected ConfirmationStatus = "rejected"
	StatusExpired  ConfirmationStatus = "expired"
)

// Confirmation is a persisted approval request for a tool call.
type Confirmation struct {
	ID        string
	ToolCall  string             // JSON-encoded tool call payload
	Status    ConfirmationStatus
	CreatedAt time.Time
	ExpiresAt time.Time
	UpdatedAt time.Time
}

// ErrExpired is returned when attempting to approve an already-expired confirmation.
var ErrExpired = errors.New("confirm: confirmation has expired")

// ErrNotFound is returned when a confirmation ID cannot be located.
var ErrNotFound = errors.New("confirm: confirmation not found")

// Gate manages the lifecycle of confirmation requests.
type Gate interface {
	Create(ctx context.Context, toolCall string, expiresIn time.Duration) (*Confirmation, error)
	Approve(ctx context.Context, id string) error
	Reject(ctx context.Context, id string) error
	ExpireStale(ctx context.Context) (int64, error)
	Cleanup(ctx context.Context, olderThan time.Duration) (int64, error)
	Get(ctx context.Context, id string) (*Confirmation, error)
}

// Compile-time assertion that *Store satisfies Gate.
var _ Gate = (*Store)(nil)

// Store is a SQLite-backed confirmation gate.
type Store struct {
	db *sql.DB
}

// schema is the DDL for the tool_confirmations table. This table is separate
// from the confirmations table in the main migration (which has a different
// shape); we own and create it ourselves.
const schema = `
CREATE TABLE IF NOT EXISTS tool_confirmations (
    id         TEXT PRIMARY KEY,
    tool_call  TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tool_confirmations_status
    ON tool_confirmations(status, expires_at);
`

// New creates a Store backed by db and ensures the tool_confirmations table
// exists.
func New(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("confirm: create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Create inserts a new pending confirmation and returns it.
func (s *Store) Create(ctx context.Context, toolCall string, expiresIn time.Duration) (*Confirmation, error) {
	id := store.NewID()
	now := time.Now().UTC()
	expiresAt := now.Add(expiresIn)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tool_confirmations (id, tool_call, status, created_at, expires_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id,
		toolCall,
		string(StatusPending),
		now.UnixMilli(),
		expiresAt.UnixMilli(),
		now.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("confirm: create: %w", err)
	}

	return &Confirmation{
		ID:        id,
		ToolCall:  toolCall,
		Status:    StatusPending,
		CreatedAt: now,
		ExpiresAt: expiresAt,
		UpdatedAt: now,
	}, nil
}

// Approve transitions a pending confirmation to approved, provided it has not
// yet expired. Returns ErrExpired if the confirmation is past its expiry time,
// and ErrNotFound if no such confirmation exists.
func (s *Store) Approve(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()

	// Fetch current state first.
	c, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	if c.ExpiresAt.Before(time.Now().UTC()) {
		return ErrExpired
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE tool_confirmations SET status = ?, updated_at = ?
		 WHERE id = ? AND status = 'pending' AND expires_at > ?`,
		string(StatusApproved), now,
		id, now,
	)
	if err != nil {
		return fmt.Errorf("confirm: approve: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("confirm: approve rows affected: %w", err)
	}
	if affected == 0 {
		// Re-check: if it expired between Get and Update, report ErrExpired.
		return ErrExpired
	}
	return nil
}

// Reject transitions a confirmation to rejected regardless of expiry.
func (s *Store) Reject(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()

	result, err := s.db.ExecContext(ctx,
		`UPDATE tool_confirmations SET status = ?, updated_at = ?
		 WHERE id = ?`,
		string(StatusRejected), now, id,
	)
	if err != nil {
		return fmt.Errorf("confirm: reject: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("confirm: reject rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ExpireStale transitions all pending confirmations whose expiry has passed to
// the expired status. Returns the number of rows updated.
func (s *Store) ExpireStale(ctx context.Context) (int64, error) {
	now := time.Now().UnixMilli()

	result, err := s.db.ExecContext(ctx,
		`UPDATE tool_confirmations SET status = ?, updated_at = ?
		 WHERE status = 'pending' AND expires_at <= ?`,
		string(StatusExpired), now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("confirm: expire stale: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("confirm: expire stale rows affected: %w", err)
	}
	return affected, nil
}

// Cleanup deletes confirmation rows whose created_at is older than olderThan
// ago. Returns the number of rows deleted.
func (s *Store) Cleanup(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UnixMilli()

	result, err := s.db.ExecContext(ctx,
		`DELETE FROM tool_confirmations WHERE created_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("confirm: cleanup: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("confirm: cleanup rows affected: %w", err)
	}
	return affected, nil
}

// Get retrieves a confirmation by ID. Returns ErrNotFound if not present.
func (s *Store) Get(ctx context.Context, id string) (*Confirmation, error) {
	var (
		c         Confirmation
		createdAt int64
		expiresAt int64
		updatedAt int64
		status    string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, tool_call, status, created_at, expires_at, updated_at
		 FROM tool_confirmations WHERE id = ?`,
		id,
	).Scan(&c.ID, &c.ToolCall, &status, &createdAt, &expiresAt, &updatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("confirm: get: %w", err)
	}

	c.Status = ConfirmationStatus(status)
	c.CreatedAt = time.UnixMilli(createdAt).UTC()
	c.ExpiresAt = time.UnixMilli(expiresAt).UTC()
	c.UpdatedAt = time.UnixMilli(updatedAt).UTC()

	return &c, nil
}
