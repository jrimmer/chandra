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
	ActionToolCall     ActionType = "tool_call"
	ActionMessageSent  ActionType = "message_sent"
	ActionError        ActionType = "error"
	ActionScheduled    ActionType = "scheduled"
	ActionConfirm      ActionType = "confirmation"
	ActionRollback     ActionType = "rollback"
	ActionPlanStart    ActionType = "plan_start"
	ActionPlanEnd      ActionType = "plan_end"
	ActionPlanExtended ActionType = "plan_extended"
)

// ActionEntry represents a single recorded agent action.
type ActionEntry struct {
	ID        string
	Timestamp time.Time
	Type      ActionType
	Summary   string         // human-readable one-liner (NOT NULL in schema)
	Details   map[string]any // stored as JSON in the database
	SessionID string
	ToolName  string // populated for tool_call actions
	Success   *bool  // nil for non-tool actions; true/false for tool calls
}

// ToolCount is a name+count pair used in rollup top-tools reporting.
type ToolCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ActionRollup is a time-bucketed summary of actions over a period.
type ActionRollup struct {
	ID          string
	Period      string
	StartTime   time.Time
	EndTime     time.Time
	Summary     string
	ActionCount int
	ErrorCount  int
	TopTools    []ToolCount
}

// ActionLog is the interface for recording and querying agent actions.
type ActionLog interface {
	Record(ctx context.Context, entry ActionEntry) error
	Query(ctx context.Context, since, until time.Time, types []ActionType) ([]ActionEntry, error)
	Recent(ctx context.Context, n int) ([]ActionEntry, error)
	GetRollup(ctx context.Context, period string, t time.Time) (ActionRollup, error)
	GenerateRollups(ctx context.Context) error
}

// Compile-time assertion that *Log satisfies ActionLog.
var _ ActionLog = (*Log)(nil)

// Log is a SQLite-backed implementation of ActionLog.
type Log struct {
	db *sql.DB
}

// NewLog returns a new Log backed by the provided database.
func NewLog(db *sql.DB) (*Log, error) {
	return &Log{db: db}, nil
}

// Record inserts a new action into the action_log table.
// If entry.Summary is empty a fallback summary is generated automatically.
// entry.Details (map[string]any) is serialised to JSON for storage.
// entry.Success is stored as 1 (true), 0 (false), or NULL (nil).
func (l *Log) Record(ctx context.Context, entry ActionEntry) error {
	id := store.NewID()
	now := time.Now().Unix()

	// Build fallback summary when the caller omits one.
	summary := entry.Summary
	if summary == "" {
		if entry.SessionID != "" {
			summary = string(entry.Type) + " in session " + entry.SessionID
		} else {
			summary = string(entry.Type)
		}
	}

	// Serialise Details map to JSON (NULL when nil/empty).
	var detailsVal interface{}
	if len(entry.Details) > 0 {
		b, err := json.Marshal(entry.Details)
		if err != nil {
			return fmt.Errorf("actionlog: marshal details: %w", err)
		}
		detailsVal = string(b)
	}

	// Nullable session_id.
	var sid interface{}
	if entry.SessionID != "" {
		sid = entry.SessionID
	}

	// Nullable tool_name.
	var toolName interface{}
	if entry.ToolName != "" {
		toolName = entry.ToolName
	}

	// Nullable success (INTEGER 1/0/NULL).
	var successVal interface{}
	if entry.Success != nil {
		if *entry.Success {
			successVal = 1
		} else {
			successVal = 0
		}
	}

	_, err := l.db.ExecContext(ctx,
		`INSERT INTO action_log (id, timestamp, type, summary, details, session_id, tool_name, success)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, now, string(entry.Type), summary, detailsVal, sid, toolName, successVal,
	)
	if err != nil {
		return fmt.Errorf("actionlog: record: %w", err)
	}
	return nil
}

// Query returns actions whose timestamp falls within [since, until).
// If types is nil or empty, all action types are returned.
func (l *Log) Query(ctx context.Context, since, until time.Time, types []ActionType) ([]ActionEntry, error) {
	query := `SELECT id, timestamp, type, summary, details, session_id, tool_name, success
	           FROM action_log
	           WHERE timestamp >= ? AND timestamp < ?`
	args := []interface{}{since.Unix(), until.Unix()}

	if len(types) == 1 {
		query += ` AND type = ?`
		args = append(args, string(types[0]))
	} else if len(types) > 1 {
		placeholders := make([]byte, 0, len(types)*2)
		for i, t := range types {
			if i > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
			args = append(args, string(t))
		}
		query += ` AND type IN (` + string(placeholders) + `)`
	}
	query += ` ORDER BY timestamp ASC`

	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("actionlog: query: %w", err)
	}
	defer rows.Close()

	return scanEntries(rows)
}

// Recent returns up to n actions ordered by timestamp descending (newest first).
func (l *Log) Recent(ctx context.Context, n int) ([]ActionEntry, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT id, timestamp, type, summary, details, session_id, tool_name, success
		 FROM action_log
		 ORDER BY timestamp DESC, rowid DESC
		 LIMIT ?`,
		n,
	)
	if err != nil {
		return nil, fmt.Errorf("actionlog: recent: %w", err)
	}
	defer rows.Close()

	return scanEntries(rows)
}

// GetByID retrieves a single action by its ID. This method is NOT part of the
// ActionLog interface; it is kept as a convenience method on *Log for the
// log.drill API handler. Returns an error wrapping sql.ErrNoRows if not found.
func (l *Log) GetByID(ctx context.Context, id string) (*ActionEntry, error) {
	var a ActionEntry
	var ts int64
	var sessionID sql.NullString
	var toolName sql.NullString
	var detailsRaw sql.NullString
	var successRaw sql.NullInt64

	err := l.db.QueryRowContext(ctx,
		`SELECT id, timestamp, type, summary, details, session_id, tool_name, success
		 FROM action_log WHERE id = ?`,
		id,
	).Scan(&a.ID, &ts, &a.Type, &a.Summary, &detailsRaw, &sessionID, &toolName, &successRaw)
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
	if toolName.Valid {
		a.ToolName = toolName.String
	}
	if detailsRaw.Valid && detailsRaw.String != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(detailsRaw.String), &m); err == nil {
			a.Details = m
		}
	}
	if successRaw.Valid {
		b := successRaw.Int64 != 0
		a.Success = &b
	}
	return &a, nil
}

// GenerateRollups generates rollups for common periods.
// It always generates an hourly rollup for the previous clock-hour.
// Daily and weekly rollups are only generated if they don't already exist for
// the current day / week start.
func (l *Log) GenerateRollups(ctx context.Context) error {
	now := time.Now().UTC()

	// Always regenerate the previous hour.
	prevHour := now.Truncate(time.Hour).Add(-time.Hour)
	if _, err := l.generateHourlyRollup(ctx, prevHour); err != nil {
		return fmt.Errorf("actionlog: generate hourly rollup: %w", err)
	}

	// Daily rollup for today — only if missing.
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	existing, err := l.GetRollup(ctx, "day", dayStart)
	if err != nil {
		return fmt.Errorf("actionlog: check daily rollup: %w", err)
	}
	if existing.ID == "" {
		if err := l.generatePeriodRollup(ctx, "day", dayStart, dayStart.Add(24*time.Hour)); err != nil {
			return fmt.Errorf("actionlog: generate daily rollup: %w", err)
		}
	}

	// Weekly rollup — find start of the current ISO week (Monday).
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday → 7 in ISO week
	}
	weekStart := time.Date(now.Year(), now.Month(), now.Day()-weekday+1, 0, 0, 0, 0, time.UTC)
	existingW, err := l.GetRollup(ctx, "week", weekStart)
	if err != nil {
		return fmt.Errorf("actionlog: check weekly rollup: %w", err)
	}
	if existingW.ID == "" {
		if err := l.generatePeriodRollup(ctx, "week", weekStart, weekStart.Add(7*24*time.Hour)); err != nil {
			return fmt.Errorf("actionlog: generate weekly rollup: %w", err)
		}
	}

	return nil
}

// generateHourlyRollup aggregates all actions for the clock-hour starting at
// hour (truncated to the hour boundary) and upserts a rollup row.
// This is kept as a private implementation helper (not part of the interface).
func (l *Log) generateHourlyRollup(ctx context.Context, hour time.Time) (*ActionRollup, error) {
	hour = hour.Truncate(time.Hour).UTC()
	hourEnd := hour.Add(time.Hour)
	return l.generatePeriodRollupAndReturn(ctx, "hour", hour, hourEnd)
}

// generatePeriodRollup generates a rollup for an arbitrary named period without returning the result.
func (l *Log) generatePeriodRollup(ctx context.Context, period string, start, end time.Time) error {
	_, err := l.generatePeriodRollupAndReturn(ctx, period, start, end)
	return err
}

// generatePeriodRollupAndReturn handles the common rollup logic for any period.
func (l *Log) generatePeriodRollupAndReturn(ctx context.Context, period string, start, end time.Time) (*ActionRollup, error) {
	// Count total actions in the period.
	var count int
	err := l.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM action_log WHERE timestamp >= ? AND timestamp < ?`,
		start.Unix(), end.Unix(),
	).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("actionlog: rollup count: %w", err)
	}

	// Count error actions.
	var errCount int
	err = l.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM action_log WHERE timestamp >= ? AND timestamp < ? AND type = ?`,
		start.Unix(), end.Unix(), string(ActionError),
	).Scan(&errCount)
	if err != nil {
		return nil, fmt.Errorf("actionlog: rollup error count: %w", err)
	}

	// Collect top tools.
	topTools, err := l.topToolsForPeriod(ctx, start, end)
	if err != nil {
		return nil, err
	}

	topToolNames := make([]string, len(topTools))
	for i, tc := range topTools {
		topToolNames[i] = tc.Name
	}
	topToolsJSON, err := json.Marshal(topToolNames)
	if err != nil {
		return nil, fmt.Errorf("actionlog: marshal top tools: %w", err)
	}

	summary := fmt.Sprintf("%s %s: %d actions, top tools: %s",
		period,
		start.Format("2006-01-02 15:00"),
		count,
		string(topToolsJSON),
	)

	id := store.NewID()

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("actionlog: rollup begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`DELETE FROM action_rollups WHERE period = ? AND start_time = ?`,
		period, start.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("actionlog: rollup delete existing: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO action_rollups (id, period, start_time, end_time, summary, action_count, error_count, top_tools)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, period, start.Unix(), end.Unix(), summary, count, errCount, string(topToolsJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("actionlog: rollup insert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("actionlog: rollup commit: %w", err)
	}

	return &ActionRollup{
		ID:          id,
		Period:      period,
		StartTime:   start,
		EndTime:     end,
		Summary:     summary,
		ActionCount: count,
		ErrorCount:  errCount,
		TopTools:    topTools,
	}, nil
}

// topToolsForPeriod returns the top 3 most-frequent tool names from tool_call
// actions in the given window, extracted from the tool_name column (falling
// back to the details JSON "tool" key for backward compatibility).
func (l *Log) topToolsForPeriod(ctx context.Context, from, to time.Time) ([]ToolCount, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT tool_name, details FROM action_log
		 WHERE type = ? AND timestamp >= ? AND timestamp < ?`,
		string(ActionToolCall), from.Unix(), to.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("actionlog: top tools query: %w", err)
	}
	defer rows.Close()

	freq := make(map[string]int)
	for rows.Next() {
		var toolNameRaw sql.NullString
		var detailsRaw sql.NullString
		if err := rows.Scan(&toolNameRaw, &detailsRaw); err != nil {
			return nil, fmt.Errorf("actionlog: top tools scan: %w", err)
		}

		// Prefer the dedicated tool_name column.
		if toolNameRaw.Valid && toolNameRaw.String != "" {
			freq[toolNameRaw.String]++
			continue
		}

		// Fallback: extract from details JSON {"tool": "..."}.
		if !detailsRaw.Valid || detailsRaw.String == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(detailsRaw.String), &m); err != nil {
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
	result := make([]ToolCount, 0, topN)
	for i := 0; i < len(sorted) && i < topN; i++ {
		result = append(result, ToolCount{Name: sorted[i].name, Count: sorted[i].count})
	}
	return result, nil
}

// GetRollup retrieves a previously generated rollup for a period and start time.
// Returns a zero-value ActionRollup (ID == "") when no matching row exists.
func (l *Log) GetRollup(ctx context.Context, period string, periodStart time.Time) (ActionRollup, error) {
	var r ActionRollup
	var startUnix int64
	var endUnix sql.NullInt64
	var topToolsRaw sql.NullString
	var errorCount sql.NullInt64

	err := l.db.QueryRowContext(ctx,
		`SELECT id, period, start_time, end_time, summary, action_count, error_count, top_tools
		 FROM action_rollups
		 WHERE period = ? AND start_time = ?`,
		period, periodStart.Unix(),
	).Scan(&r.ID, &r.Period, &startUnix, &endUnix, &r.Summary, &r.ActionCount, &errorCount, &topToolsRaw)
	if err == sql.ErrNoRows {
		return ActionRollup{}, nil
	}
	if err != nil {
		return ActionRollup{}, fmt.Errorf("actionlog: get rollup: %w", err)
	}

	r.StartTime = time.Unix(startUnix, 0).UTC()
	if endUnix.Valid {
		r.EndTime = time.Unix(endUnix.Int64, 0).UTC()
	}
	if errorCount.Valid {
		r.ErrorCount = int(errorCount.Int64)
	}
	if topToolsRaw.Valid && topToolsRaw.String != "" {
		// Stored as a JSON array of tool names; convert to []ToolCount with Count=0
		// (count data is not persisted separately).
		var names []string
		if err := json.Unmarshal([]byte(topToolsRaw.String), &names); err == nil {
			r.TopTools = make([]ToolCount, len(names))
			for i, n := range names {
				r.TopTools[i] = ToolCount{Name: n}
			}
		}
	}
	return r, nil
}

// scanEntries reads all rows into a slice of ActionEntry.
func scanEntries(rows *sql.Rows) ([]ActionEntry, error) {
	var entries []ActionEntry
	for rows.Next() {
		var a ActionEntry
		var ts int64
		var sessionID sql.NullString
		var toolName sql.NullString
		var detailsRaw sql.NullString
		var successRaw sql.NullInt64

		if err := rows.Scan(&a.ID, &ts, &a.Type, &a.Summary, &detailsRaw, &sessionID, &toolName, &successRaw); err != nil {
			return nil, fmt.Errorf("actionlog: scan row: %w", err)
		}
		a.Timestamp = time.Unix(ts, 0).UTC()
		if sessionID.Valid {
			a.SessionID = sessionID.String
		}
		if toolName.Valid {
			a.ToolName = toolName.String
		}
		if detailsRaw.Valid && detailsRaw.String != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(detailsRaw.String), &m); err == nil {
				a.Details = m
			}
		}
		if successRaw.Valid {
			b := successRaw.Int64 != 0
			a.Success = &b
		}
		entries = append(entries, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("actionlog: iterate rows: %w", err)
	}
	return entries, nil
}
