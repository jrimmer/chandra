// Package actionlog provides an audit trail for agent actions with rollup
// aggregation support.
package actionlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jrimmer/chandra/store"
)

// ActionType classifies what kind of agent action was taken.
type ActionType string

const (
	ActionToolCall    ActionType = "tool_call"
	ActionMessageSent ActionType = "message_sent"
	ActionError       ActionType = "error"
	ActionScheduled   ActionType = "scheduled_turn"
	ActionConfirm     ActionType = "confirmation"
)

// Action represents a single recorded agent action.
type Action struct {
	ID        string
	Type      ActionType
	SessionID string
	Details   string
	Timestamp time.Time
}

// Rollup is a time-bucketed summary of actions over a period.
type Rollup struct {
	ID          string
	Period      string
	PeriodStart time.Time
	Summary     string
	ActionCount int
	TopTools    string // JSON array of top tool names
}

// Log is the interface for recording and querying agent actions.
type Log interface {
	Record(ctx context.Context, sessionID string, actionType ActionType, details string) error
	Query(ctx context.Context, since, until time.Time, actionType ActionType) ([]*Action, error)
	Recent(ctx context.Context, limit int) ([]*Action, error)
	GetByID(ctx context.Context, id string) (*Action, error)
	GenerateHourlyRollup(ctx context.Context, hour time.Time) (*Rollup, error)
	GetRollup(ctx context.Context, period string, periodStart time.Time) (*Rollup, error)
}

// Compile-time assertion that *ActionLog satisfies Log.
var _ Log = (*ActionLog)(nil)

// ActionLog is a SQLite-backed implementation of Log.
type ActionLog struct {
	db *sql.DB
}

// NewLog returns a new ActionLog backed by the provided database.
func NewLog(db *sql.DB) (*ActionLog, error) {
	return &ActionLog{db: db}, nil
}

// Record inserts a new action into the action_log table.
// The summary column (NOT NULL in schema) is set to the actionType string.
// The session_id FK is nullable; if sessionID is empty no FK is stored.
func (l *ActionLog) Record(ctx context.Context, sessionID string, actionType ActionType, details string) error {
	id := store.NewID()
	now := time.Now().Unix()

	var sid interface{}
	if sessionID != "" {
		sid = sessionID
	}

	_, err := l.db.ExecContext(ctx,
		`INSERT INTO action_log (id, timestamp, type, summary, details, session_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, now, string(actionType), string(actionType), details, sid,
	)
	if err != nil {
		return fmt.Errorf("actionlog: record: %w", err)
	}
	return nil
}

// Query returns actions whose timestamp falls within [since, until).
// If actionType is non-empty the results are filtered to that type.
func (l *ActionLog) Query(ctx context.Context, since, until time.Time, actionType ActionType) ([]*Action, error) {
	query := `SELECT id, type, session_id, details, timestamp
	           FROM action_log
	           WHERE timestamp >= ? AND timestamp < ?`
	args := []interface{}{since.Unix(), until.Unix()}

	if actionType != "" {
		query += ` AND type = ?`
		args = append(args, string(actionType))
	}
	query += ` ORDER BY timestamp ASC`

	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("actionlog: query: %w", err)
	}
	defer rows.Close()

	return scanActions(rows)
}

// Recent returns up to limit actions ordered by timestamp descending (newest first).
func (l *ActionLog) Recent(ctx context.Context, limit int) ([]*Action, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT id, type, session_id, details, timestamp
		 FROM action_log
		 ORDER BY timestamp DESC, rowid DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("actionlog: recent: %w", err)
	}
	defer rows.Close()

	return scanActions(rows)
}

// GetByID retrieves a single action by its ID. Returns an error wrapping
// sql.ErrNoRows (via fmt.Errorf) if no matching row exists.
func (l *ActionLog) GetByID(ctx context.Context, id string) (*Action, error) {
	var a Action
	var ts int64
	var sessionID sql.NullString
	var details sql.NullString

	err := l.db.QueryRowContext(ctx,
		`SELECT id, type, session_id, details, timestamp FROM action_log WHERE id = ?`,
		id,
	).Scan(&a.ID, &a.Type, &sessionID, &details, &ts)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("actionlog: action %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("actionlog: get by id: %w", err)
	}

	a.Timestamp = time.Unix(ts, 0).UTC()
	if sessionID.Valid {
		a.SessionID = sessionID.String
	}
	if details.Valid {
		a.Details = details.String
	}
	return &a, nil
}

// GenerateHourlyRollup aggregates all actions for the clock-hour starting at
// hour (truncated to the hour boundary) and upserts a rollup row.
func (l *ActionLog) GenerateHourlyRollup(ctx context.Context, hour time.Time) (*Rollup, error) {
	hour = hour.Truncate(time.Hour).UTC()
	hourEnd := hour.Add(time.Hour)

	// Count total actions in the hour.
	var count int
	err := l.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM action_log WHERE timestamp >= ? AND timestamp < ?`,
		hour.Unix(), hourEnd.Unix(),
	).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("actionlog: rollup count: %w", err)
	}

	// Collect tool names from tool_call actions in this hour.
	topTools, err := l.topToolsForHour(ctx, hour, hourEnd)
	if err != nil {
		return nil, err
	}

	topToolsJSON, err := json.Marshal(topTools)
	if err != nil {
		return nil, fmt.Errorf("actionlog: marshal top tools: %w", err)
	}

	summary := fmt.Sprintf("Hour %s: %d actions, top tools: %s",
		hour.Format("2006-01-02 15:00"),
		count,
		string(topToolsJSON),
	)

	id := store.NewID()

	// The schema has no UNIQUE constraint on (period, start_time), so we use
	// a DELETE + INSERT pattern to guarantee idempotency. Wrap in a transaction
	// so the two operations are atomic.
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("actionlog: rollup begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`DELETE FROM action_rollups WHERE period = ? AND start_time = ?`,
		"hourly", hour.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("actionlog: rollup delete existing: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO action_rollups (id, period, start_time, end_time, summary, action_count, error_count, top_tools)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		id, "hourly", hour.Unix(), hourEnd.Unix(), summary, count, string(topToolsJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("actionlog: rollup insert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("actionlog: rollup commit: %w", err)
	}

	return &Rollup{
		ID:          id,
		Period:      "hourly",
		PeriodStart: hour,
		Summary:     summary,
		ActionCount: count,
		TopTools:    string(topToolsJSON),
	}, nil
}

// topToolsForHour returns the top 3 most-frequent tool names extracted from
// tool_call action Details JSON for the given hour window.
func (l *ActionLog) topToolsForHour(ctx context.Context, from, to time.Time) ([]string, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT details FROM action_log
		 WHERE type = ? AND timestamp >= ? AND timestamp < ?`,
		string(ActionToolCall), from.Unix(), to.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("actionlog: top tools query: %w", err)
	}
	defer rows.Close()

	freq := make(map[string]int)
	for rows.Next() {
		var details sql.NullString
		if err := rows.Scan(&details); err != nil {
			return nil, fmt.Errorf("actionlog: top tools scan: %w", err)
		}
		if !details.Valid || details.String == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(details.String), &m); err != nil {
			// Non-JSON details — skip tool extraction.
			continue
		}
		if toolVal, ok := m["tool"]; ok {
			if toolName, ok := toolVal.(string); ok && toolName != "" {
				freq[toolName]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("actionlog: top tools iterate: %w", err)
	}

	// Sort by frequency descending, then alphabetically for determinism.
	type kv struct {
		name  string
		count int
	}
	sorted := make([]kv, 0, len(freq))
	for name, c := range freq {
		sorted = append(sorted, kv{name, c})
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].count != sorted[j].count {
			return sorted[i].count > sorted[j].count
		}
		return sorted[i].name < sorted[j].name
	})

	const topN = 3
	result := make([]string, 0, topN)
	for i := 0; i < len(sorted) && i < topN; i++ {
		result = append(result, sorted[i].name)
	}
	return result, nil
}

// GetRollup retrieves a previously generated rollup for a period and start time.
func (l *ActionLog) GetRollup(ctx context.Context, period string, periodStart time.Time) (*Rollup, error) {
	var r Rollup
	var startUnix int64
	var topTools sql.NullString

	err := l.db.QueryRowContext(ctx,
		`SELECT id, period, start_time, summary, action_count, top_tools
		 FROM action_rollups
		 WHERE period = ? AND start_time = ?`,
		period, periodStart.Unix(),
	).Scan(&r.ID, &r.Period, &startUnix, &r.Summary, &r.ActionCount, &topTools)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("actionlog: get rollup: %w", err)
	}

	r.PeriodStart = time.Unix(startUnix, 0).UTC()
	if topTools.Valid {
		r.TopTools = topTools.String
	}
	return &r, nil
}

// scanActions reads all rows into a slice of *Action.
func scanActions(rows *sql.Rows) ([]*Action, error) {
	var actions []*Action
	for rows.Next() {
		var a Action
		var ts int64
		var sessionID sql.NullString
		var details sql.NullString

		if err := rows.Scan(&a.ID, &a.Type, &sessionID, &details, &ts); err != nil {
			return nil, fmt.Errorf("actionlog: scan row: %w", err)
		}
		a.Timestamp = time.Unix(ts, 0).UTC()
		if sessionID.Valid {
			a.SessionID = sessionID.String
		}
		if details.Valid {
			a.Details = details.String
		}
		actions = append(actions, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("actionlog: iterate rows: %w", err)
	}
	return actions, nil
}
