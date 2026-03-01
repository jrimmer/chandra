package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jrimmer/chandra/internal/channels"
	"github.com/jrimmer/chandra/store"
)

// Session represents a single activity window for a user in a channel.
type Session struct {
	ID             string    // ULID — new per activity window
	ConversationID string    // stable: SHA256 of channel_id + ":" + user_id, hex-encoded, first 16 chars
	ChannelID      string
	UserID         string
	StartedAt      time.Time
	LastActive     time.Time

	// Runtime state — not persisted:
	cancelFn context.CancelFunc
	msgChan  chan channels.InboundMessage
}

// Manager manages sessions for the agent.
type Manager interface {
	GetOrCreate(ctx context.Context, conversationID string, channelID string, userID string) (*Session, error)
	Get(sessionID string) *Session
	Touch(sessionID string) error
	Close(sessionID string) error
	ActiveCount() int
	SetMaxConcurrent(n int)
}

// Compile-time assertion that *manager satisfies Manager.
var _ Manager = (*manager)(nil)

// manager is the concrete implementation of Manager.
type manager struct {
	db      *sql.DB
	timeout time.Duration

	// mu serializes GetOrCreate so that two concurrent callers for the same
	// channel/user pair do not both attempt to INSERT a new session row.
	// It also protects mutations to cached *Session fields (e.g. LastActive).
	mu    sync.Mutex
	cache sync.Map // key: "channelID:userID" → *Session

	// maxConcurrent is the maximum number of concurrent sessions allowed.
	// A value of 0 means unlimited.
	maxConcurrent int

	// startMu protects cancel and done to prevent data races between Start/Stop.
	startMu sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewManager returns a new manager with the given DB and inactivity timeout.
// A background goroutine that calls CleanupExpired every 5 minutes is started
// via Start; callers should call Stop to shut it down.
func NewManager(db *sql.DB, timeout time.Duration) (*manager, error) {
	if db == nil {
		return nil, fmt.Errorf("session: db must not be nil")
	}
	m := &manager{
		db:      db,
		timeout: timeout,
	}
	return m, nil
}

// Start launches the background cleanup goroutine. It is safe to call Start
// more than once; subsequent calls are no-ops if already running.
func (m *manager) Start(ctx context.Context) {
	m.startMu.Lock()
	if m.cancel != nil {
		m.startMu.Unlock()
		return
	}
	m.done = make(chan struct{})
	bgCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.startMu.Unlock()

	go m.runCleanup(bgCtx)
}

// Stop cancels the background goroutine and waits for it to exit.
func (m *manager) Stop() {
	m.startMu.Lock()
	cancel := m.cancel
	done := m.done
	m.cancel = nil
	m.startMu.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}
}

func (m *manager) runCleanup(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = m.CleanupExpired(ctx)
		}
	}
}

// cacheKey returns the sync.Map key for the given channel/user pair.
func cacheKey(channelID, userID string) string {
	return channelID + ":" + userID
}

// ComputeConversationID returns the stable conversation ID for a channel/user
// pair: SHA256(channelID + ":" + userID), hex-encoded, first 16 chars.
// Exported so tests can verify the expected value independently.
func ComputeConversationID(channelID, userID string) string {
	sum := sha256.Sum256([]byte(channelID + ":" + userID))
	return hex.EncodeToString(sum[:])[:16]
}

// SetMaxConcurrent sets the maximum number of concurrent sessions allowed.
// A value of 0 means unlimited.
func (m *manager) SetMaxConcurrent(n int) {
	m.mu.Lock()
	m.maxConcurrent = n
	m.mu.Unlock()
}

// ActiveCount returns the number of sessions currently in the in-memory cache.
func (m *manager) ActiveCount() int {
	count := 0
	m.cache.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// Get looks up a session by session ID in the in-memory cache.
// Returns nil if no session with that ID is found.
func (m *manager) Get(sessionID string) *Session {
	var found *Session
	m.cache.Range(func(_, v any) bool {
		s := v.(*Session)
		if s.ID == sessionID {
			found = s
			return false // stop iteration
		}
		return true
	})
	return found
}

// GetOrCreate returns the active session for the given channel/user pair,
// creating a new one if no session exists or the existing one has expired.
// The conversationID is passed in by the caller (computed by the channel layer).
// A mutex ensures only one goroutine per manager can be in the check-and-insert
// critical section at a time, preventing duplicate INSERT errors.
func (m *manager) GetOrCreate(ctx context.Context, conversationID string, channelID string, userID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := cacheKey(channelID, userID)
	now := time.Now().UTC()

	if raw, ok := m.cache.Load(key); ok {
		existing := raw.(*Session)
		if now.Sub(existing.LastActive) < m.timeout {
			// Session is still active — update the in-memory timestamp and the DB.
			existing.LastActive = now
			if _, err := m.db.ExecContext(ctx,
				`UPDATE sessions SET last_active = ? WHERE id = ?`,
				now.UnixMilli(), existing.ID,
			); err != nil {
				slog.Warn("session: failed to update last_active", "session_id", existing.ID, "error", err)
			}
			return existing, nil
		}
		// Session has expired — fall through to create a new one.
	}

	// Check max concurrent sessions limit.
	if m.maxConcurrent > 0 {
		count := 0
		m.cache.Range(func(_, _ any) bool {
			count++
			return true
		})
		if count >= m.maxConcurrent {
			return nil, fmt.Errorf("session: max concurrent sessions reached")
		}
	}

	sess := &Session{
		ID:             store.NewID(),
		ConversationID: conversationID,
		ChannelID:      channelID,
		UserID:         userID,
		StartedAt:      now,
		LastActive:     now,
	}

	if err := m.insertDB(ctx, sess); err != nil {
		return nil, fmt.Errorf("session: insert: %w", err)
	}

	m.cache.Store(key, sess)
	return sess, nil
}

// insertDB persists a new session to the database.
func (m *manager) insertDB(ctx context.Context, sess *Session) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO sessions (id, conversation_id, channel_id, user_id, started_at, last_active)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sess.ID,
		sess.ConversationID,
		sess.ChannelID,
		sess.UserID,
		sess.StartedAt.UnixMilli(),
		sess.LastActive.UnixMilli(),
	)
	return err
}

// Touch updates the last_active timestamp for the given session in both the
// database and the in-memory cache.
func (m *manager) Touch(sessionID string) error {
	now := time.Now().UTC()
	_, err := m.db.ExecContext(context.Background(),
		`UPDATE sessions SET last_active = ? WHERE id = ?`,
		now.UnixMilli(),
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("session: touch db: %w", err)
	}

	// Update the cache entry if present. Hold mu to protect the shared *Session
	// pointer's LastActive field against concurrent mutation.
	m.mu.Lock()
	m.cache.Range(func(k, v any) bool {
		s := v.(*Session)
		if s.ID == sessionID {
			s.LastActive = now
			return false // stop iteration
		}
		return true
	})
	m.mu.Unlock()

	return nil
}

// Close removes the session from the database first, then evicts it from the
// cache only on success.
func (m *manager) Close(sessionID string) error {
	// Delete from DB first.
	_, err := m.db.ExecContext(context.Background(), `DELETE FROM sessions WHERE id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("session: close db: %w", err)
	}

	// Only evict from cache after a successful DB delete.
	m.cache.Range(func(k, v any) bool {
		s := v.(*Session)
		if s.ID == sessionID {
			m.cache.Delete(k)
			return false
		}
		return true
	})

	return nil
}

// CleanupExpired deletes sessions whose last_active timestamp is older than
// the manager's inactivity timeout. Returns the number of rows deleted.
// This method is on the concrete type only and is not part of the Manager interface.
func (m *manager) CleanupExpired(ctx context.Context) (int64, error) {
	cutoff := time.Now().UTC().Add(-m.timeout).UnixMilli()
	result, err := m.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE last_active < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("session: cleanup expired: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("session: rows affected: %w", err)
	}

	// Evict stale entries from the in-memory cache to keep it consistent with
	// the database after the bulk DELETE.
	m.cache.Range(func(k, v any) bool {
		s := v.(*Session)
		if s.LastActive.UnixMilli() < cutoff {
			m.cache.Delete(k)
		}
		return true
	})

	return n, nil
}
