package episodic

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
)

// EpisodicStore defines the append-only episode storage contract.
type EpisodicStore interface {
	Append(ctx context.Context, ep pkg.Episode) error
	Recent(ctx context.Context, sessionID string, n int) ([]pkg.Episode, error)
	// RecentAcrossSessions returns the n most recent episodes for a given
	// channel+user combination, spanning all sessions. Use this to retrieve
	// episodic context that survives daemon restarts and session boundaries.
	RecentAcrossSessions(ctx context.Context, channelID, userID string, n int) ([]pkg.Episode, error)
	Since(ctx context.Context, t time.Time) ([]pkg.Episode, error)
}

// Compile-time assertion that *Store satisfies EpisodicStore.
var _ EpisodicStore = (*Store)(nil)

// Store is an append-only episodic memory store backed by SQLite.
type Store struct {
	db *sql.DB
}

// NewStore returns a new Store using the provided database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Append inserts an Episode into the episodes table. If ep.ID is empty a new
// ULID is generated. Tags are serialized as a JSON array.
func (s *Store) Append(ctx context.Context, ep pkg.Episode) error {
	if ep.ID == "" {
		ep.ID = store.NewID()
	}

	tags := ep.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("episodic: marshal tags: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO episodes (id, session_id, role, content, timestamp, tags)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ep.ID,
		ep.SessionID,
		ep.Role,
		ep.Content,
		ep.Timestamp.Unix(),
		tagsJSON,
	)
	if err != nil {
		return fmt.Errorf("episodic: append: %w", err)
	}
	return nil
}

// Recent returns up to n episodes for the given session, newest first.
func (s *Store) Recent(ctx context.Context, sessionID string, n int) ([]pkg.Episode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, role, content, timestamp, tags
		 FROM episodes
		 WHERE session_id = ?
		 ORDER BY timestamp DESC
		 LIMIT ?`,
		sessionID, n,
	)
	if err != nil {
		return nil, fmt.Errorf("episodic: recent: %w", err)
	}
	defer rows.Close()

	return scanEpisodes(rows)
}

// RecentAcrossSessions returns the n most recent episodes for a given
// channel+user pair, joining across all sessions. This survives daemon
// restarts and session boundary transitions.
func (s *Store) RecentAcrossSessions(ctx context.Context, channelID, userID string, n int) ([]pkg.Episode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.session_id, e.role, e.content, e.timestamp, e.tags
		 FROM episodes e
		 JOIN sessions sess ON e.session_id = sess.id
		 WHERE sess.channel_id = ? AND sess.user_id = ?
		 ORDER BY e.timestamp DESC
		 LIMIT ?`,
		channelID, userID, n,
	)
	if err != nil {
		return nil, fmt.Errorf("episodic: recent_across_sessions: %w", err)
	}
	defer rows.Close()

	return scanEpisodes(rows)
}

// Since returns all episodes with a timestamp strictly after t, newest first.
func (s *Store) Since(ctx context.Context, t time.Time) ([]pkg.Episode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, role, content, timestamp, tags
		 FROM episodes
		 WHERE timestamp > ?
		 ORDER BY timestamp DESC`,
		t.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("episodic: since: %w", err)
	}
	defer rows.Close()

	return scanEpisodes(rows)
}

// scanEpisodes reads all rows from a query result into a slice of Episodes.
func scanEpisodes(rows *sql.Rows) ([]pkg.Episode, error) {
	var episodes []pkg.Episode
	for rows.Next() {
		var ep pkg.Episode
		var ts int64
		var tagsRaw []byte

		if err := rows.Scan(&ep.ID, &ep.SessionID, &ep.Role, &ep.Content, &ts, &tagsRaw); err != nil {
			return nil, fmt.Errorf("episodic: scan row: %w", err)
		}

		ep.Timestamp = time.Unix(ts, 0).UTC()

		if tagsRaw != nil {
			if err := json.Unmarshal(tagsRaw, &ep.Tags); err != nil {
				return nil, fmt.Errorf("episodic: unmarshal tags: %w", err)
			}
		} else {
			ep.Tags = []string{}
		}

		episodes = append(episodes, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("episodic: iterate rows: %w", err)
	}
	return episodes, nil
}
