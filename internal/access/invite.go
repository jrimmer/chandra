// Package access manages user access control: invite codes, access requests,
// and the allowed_users table.
package access

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// InviteCode represents a single invite code record.
type InviteCode struct {
	Code          string
	UsesRemaining int
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

// IsExpired returns true if the code has passed its expiry time.
func (c InviteCode) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt)
}

// GenerateCode creates a cryptographically random invite code with the
// chandra-inv- prefix and 12 random hex characters.
func GenerateCode() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("access: crypto/rand failed: " + err.Error())
	}
	return "chandra-inv-" + hex.EncodeToString(b)
}

// Store manages invite codes and allowed users in the database.
type Store struct {
	db *sql.DB
}

// NewStore creates a new access.Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateInvite creates a new invite code with the given parameters.
// uses=1 for single-use. ttl=0 for no expiry.
func (s *Store) CreateInvite(ctx context.Context, uses int, ttl time.Duration) (InviteCode, error) {
	code := GenerateCode()
	now := time.Now()
	var expiresAt *int64
	var expTime time.Time
	if ttl > 0 {
		t := now.Add(ttl)
		ts := t.Unix()
		expiresAt = &ts
		expTime = t
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO invite_codes (code, uses_remaining, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		code, uses, expiresAt, now.Unix(),
	)
	if err != nil {
		return InviteCode{}, fmt.Errorf("create invite: %w", err)
	}
	return InviteCode{
		Code:          code,
		UsesRemaining: uses,
		ExpiresAt:     expTime,
		CreatedAt:     now,
	}, nil
}

// ListInvites returns all active (non-expired, uses > 0) invite codes.
func (s *Store) ListInvites(ctx context.Context) ([]InviteCode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT code, uses_remaining, expires_at, created_at FROM invite_codes
		 WHERE uses_remaining != 0
		 ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	defer rows.Close()

	var codes []InviteCode
	for rows.Next() {
		var c InviteCode
		var expiresAt *int64
		var createdAt int64
		if err := rows.Scan(&c.Code, &c.UsesRemaining, &expiresAt, &createdAt); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(createdAt, 0)
		if expiresAt != nil {
			c.ExpiresAt = time.Unix(*expiresAt, 0)
		}
		codes = append(codes, c)
	}
	return codes, rows.Err()
}

// RevokeInvite deletes an invite code.
func (s *Store) RevokeInvite(ctx context.Context, code string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM invite_codes WHERE code = ?`, code)
	if err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("invite code not found: %s", code)
	}
	return nil
}

// RedeemInvite validates the code, decrements uses_remaining, adds the user
// to allowed_users, and returns nil on success.
func (s *Store) RedeemInvite(ctx context.Context, code, channelID, userID, username string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var uses int
	var expiresAt *int64
	err = tx.QueryRowContext(ctx,
		`SELECT uses_remaining, expires_at FROM invite_codes WHERE code = ?`, code,
	).Scan(&uses, &expiresAt)
	if err == sql.ErrNoRows {
		return fmt.Errorf("invite code not found or already used")
	}
	if err != nil {
		return fmt.Errorf("check code: %w", err)
	}

	if uses == 0 {
		return fmt.Errorf("invite code exhausted")
	}
	if expiresAt != nil && time.Now().Unix() > *expiresAt {
		return fmt.Errorf("invite code expired")
	}

	newUses := uses - 1
	if _, err := tx.ExecContext(ctx, `UPDATE invite_codes SET uses_remaining = ? WHERE code = ?`, newUses, code); err != nil {
		return fmt.Errorf("decrement uses: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO allowed_users (channel_id, user_id, username, source, added_at) VALUES (?, ?, ?, 'invite', ?)`,
		channelID, userID, username, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("add allowed user: %w", err)
	}

	return tx.Commit()
}

// AddUser adds a user directly to the allowlist (for manual and hello_world sources).
func (s *Store) AddUser(ctx context.Context, channelID, userID, username, source string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO allowed_users (channel_id, user_id, username, source, added_at) VALUES (?, ?, ?, ?, ?)`,
		channelID, userID, username, source, time.Now().Unix(),
	)
	return err
}

// RemoveUser removes a user from the allowlist.
func (s *Store) RemoveUser(ctx context.Context, channelID, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM allowed_users WHERE channel_id = ? AND user_id = ?`, channelID, userID)
	return err
}

// ListUsers returns all allowed users for a channel.
func (s *Store) ListUsers(ctx context.Context, channelID string) ([]AllowedUser, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, username, source, added_at FROM allowed_users WHERE channel_id = ? ORDER BY added_at DESC`,
		channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []AllowedUser
	for rows.Next() {
		var u AllowedUser
		var addedAt int64
		if err := rows.Scan(&u.UserID, &u.Username, &u.Source, &addedAt); err != nil {
			return nil, err
		}
		u.AddedAt = time.Unix(addedAt, 0)
		users = append(users, u)
	}
	return users, rows.Err()
}

// AllowedUser represents a user in the access allowlist.
type AllowedUser struct {
	UserID   string
	Username string
	Source   string
	AddedAt  time.Time
}
