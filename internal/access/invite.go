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

	// Idempotency check: if the user is already in the allowlist for this channel,
	// return success without consuming a use. Prevents replay attacks from burning
	// through uses on already-authorized users.
	var alreadyAuthorized int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM allowed_users WHERE channel_id = ? AND user_id = ?`,
		channelID, userID,
	).Scan(&alreadyAuthorized); err != nil {
		return fmt.Errorf("check existing user: %w", err)
	}
	if alreadyAuthorized > 0 {
		// Already authorized — commit without decrementing uses.
		return tx.Commit()
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


// RedeemInviteMulti redeems an invite code for a user across multiple channel IDs.
// Uses are decremented exactly once regardless of how many channels are provided.
// If the user is already authorized in all channels, no uses are consumed (idempotent).
func (s *Store) RedeemInviteMulti(ctx context.Context, code string, channelIDs []string, userID, username string) (int, error) {
	if len(channelIDs) == 0 {
		return 0, fmt.Errorf("no channel IDs provided")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var uses int
	var expiresAt *int64
	err = tx.QueryRowContext(ctx,
		`SELECT uses_remaining, expires_at FROM invite_codes WHERE code = ?`, code,
	).Scan(&uses, &expiresAt)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("invite code not found or already used")
	}
	if err != nil {
		return 0, fmt.Errorf("check code: %w", err)
	}
	if uses == 0 {
		return 0, fmt.Errorf("invite code exhausted")
	}
	if expiresAt != nil && time.Now().Unix() > *expiresAt {
		return 0, fmt.Errorf("invite code expired")
	}

	// Check if the user is already authorized in ALL channels — idempotent return.
	now := time.Now().Unix()
	var added int
	for _, chID := range channelIDs {
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM allowed_users WHERE channel_id = ? AND user_id = ?`,
			chID, userID,
		).Scan(&count); err != nil {
			return 0, fmt.Errorf("check existing user: %w", err)
		}
		if count > 0 {
			continue // already authorized in this channel
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO allowed_users (channel_id, user_id, username, source, added_at) VALUES (?, ?, ?, 'invite', ?)`,
			chID, userID, username, now,
		); err != nil {
			return 0, fmt.Errorf("add user to channel %s: %w", chID, err)
		}
		added++
	}

	// Only decrement uses if at least one channel was newly authorized.
	if added > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE invite_codes SET uses_remaining = ? WHERE code = ?`, uses-1, code); err != nil {
			return 0, fmt.Errorf("decrement uses: %w", err)
		}
	}

	return added, tx.Commit()
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
		var nullUsername sql.NullString
		if err := rows.Scan(&u.UserID, &nullUsername, &u.Source, &addedAt); err != nil {
			return nil, err
		}
		if nullUsername.Valid && nullUsername.String != "" {
			u.Username = nullUsername.String
		} else {
			u.Username = "(unknown)"
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

// AccessRequest represents a pending access request.
type AccessRequest struct {
	ID           string
	ChannelID    string
	UserID       string
	Username     string
	FirstMessage string
	Status       string // pending | approved | denied | blocked
	CreatedAt    time.Time
	DecidedAt    *time.Time
}

// CreateRequest creates a new access request. Returns the request ID.
// If a pending request already exists for this user+channel, returns its ID
// and exists=true without creating a duplicate.
func (s *Store) CreateRequest(ctx context.Context, channelID, userID, username, firstMessage string) (id string, exists bool, err error) {
	// Check for existing pending request.
	var existingID string
	err = s.db.QueryRowContext(ctx,
		`SELECT id FROM access_requests WHERE channel_id = ? AND user_id = ? AND status = 'pending'`,
		channelID, userID,
	).Scan(&existingID)
	if err == nil {
		return existingID, true, nil
	}

	// Check if user was previously denied/blocked — don't allow re-request.
	var blockedStatus string
	err = s.db.QueryRowContext(ctx,
		`SELECT status FROM access_requests WHERE channel_id = ? AND user_id = ? AND status = 'blocked' ORDER BY created_at DESC LIMIT 1`,
		channelID, userID,
	).Scan(&blockedStatus)
	if err == nil {
		return "", false, fmt.Errorf("user is blocked")
	}

	id = fmt.Sprintf("req_%d_%s", time.Now().UnixMilli(), userID[:min(8, len(userID))])
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO access_requests (id, channel_id, user_id, username, first_message, status, created_at) VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		id, channelID, userID, username, firstMessage, time.Now().Unix(),
	)
	if err != nil {
		return "", false, fmt.Errorf("create access request: %w", err)
	}
	return id, false, nil
}

// ApproveRequest approves a pending access request and adds the user to
// allowed_users. Returns the request details for notification purposes.
func (s *Store) ApproveRequest(ctx context.Context, requestID string) (*AccessRequest, error) {
	var req AccessRequest
	var createdAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, user_id, username, first_message, status, created_at FROM access_requests WHERE id = ?`,
		requestID,
	).Scan(&req.ID, &req.ChannelID, &req.UserID, &req.Username, &req.FirstMessage, &req.Status, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("request not found: %s", requestID)
	}
	if req.Status != "pending" {
		return nil, fmt.Errorf("request %s is already %s", requestID, req.Status)
	}
	req.CreatedAt = time.Unix(createdAt, 0)

	now := time.Now()
	req.DecidedAt = &now

	_, err = s.db.ExecContext(ctx,
		`UPDATE access_requests SET status = 'approved', decided_at = ? WHERE id = ?`,
		now.Unix(), requestID,
	)
	if err != nil {
		return nil, fmt.Errorf("approve request: %w", err)
	}

	// Add to allowed_users.
	err = s.AddUser(ctx, req.ChannelID, req.UserID, req.Username, "request")
	if err != nil {
		return nil, fmt.Errorf("add user after approval: %w", err)
	}

	return &req, nil
}

// DenyRequest denies a pending access request. If block is true, marks as
// "blocked" so the user cannot re-request.
func (s *Store) DenyRequest(ctx context.Context, requestID string, block bool) (*AccessRequest, error) {
	var req AccessRequest
	var createdAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, user_id, username, first_message, status, created_at FROM access_requests WHERE id = ?`,
		requestID,
	).Scan(&req.ID, &req.ChannelID, &req.UserID, &req.Username, &req.FirstMessage, &req.Status, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("request not found: %s", requestID)
	}
	if req.Status != "pending" {
		return nil, fmt.Errorf("request %s is already %s", requestID, req.Status)
	}
	req.CreatedAt = time.Unix(createdAt, 0)

	status := "denied"
	if block {
		status = "blocked"
	}
	now := time.Now()
	req.DecidedAt = &now

	_, err = s.db.ExecContext(ctx,
		`UPDATE access_requests SET status = ?, decided_at = ? WHERE id = ?`,
		status, now.Unix(), requestID,
	)
	if err != nil {
		return nil, fmt.Errorf("deny request: %w", err)
	}
	return &req, nil
}

// FindRequestByApprovalMessage finds a pending access request by looking up
// which request was sent to the owner in a specific DM message.
func (s *Store) FindRequestByApprovalMessage(ctx context.Context, messageID string) (*AccessRequest, error) {
	var req AccessRequest
	var createdAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, user_id, username, first_message, status, created_at FROM access_requests WHERE approval_message_id = ? AND status = 'pending'`,
		messageID,
	).Scan(&req.ID, &req.ChannelID, &req.UserID, &req.Username, &req.FirstMessage, &req.Status, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("no pending request for message %s", messageID)
	}
	req.CreatedAt = time.Unix(createdAt, 0)
	return &req, nil
}

// SetApprovalMessageID stores the Discord message ID used to notify the owner
// about this request, enabling reaction-based approve/deny.
func (s *Store) SetApprovalMessageID(ctx context.Context, requestID, messageID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE access_requests SET approval_message_id = ? WHERE id = ?`,
		messageID, requestID,
	)
	return err
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
