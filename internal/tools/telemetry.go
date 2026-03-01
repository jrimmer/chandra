package tools

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// ToolReliability summarises the reliability of a tool over the past 30 days.
type ToolReliability struct {
	ToolName     string
	SuccessRate  float32
	P50LatencyMs int
	P95LatencyMs int
	LastError    string
	LastErrorAt  time.Time
	SampleSize   int
}

// ComputeReliability queries tool_telemetry for the named tool and computes
// its success rate and latency percentiles over the most recent 30 days.
// Returns an error if the query fails.
func ComputeReliability(ctx context.Context, db *sql.DB, toolName string) (ToolReliability, error) {
	windowStart := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()

	rows, err := db.QueryContext(ctx,
		`SELECT success, latency_ms
		 FROM tool_telemetry
		 WHERE tool_name = ? AND called_at >= ?`,
		toolName, windowStart,
	)
	if err != nil {
		return ToolReliability{}, fmt.Errorf("tools/telemetry: query telemetry: %w", err)
	}
	defer rows.Close()

	var (
		latencies []int64
		successes int
	)

	for rows.Next() {
		var success int
		var latencyMs int64
		if err := rows.Scan(&success, &latencyMs); err != nil {
			return ToolReliability{}, fmt.Errorf("tools/telemetry: scan row: %w", err)
		}
		latencies = append(latencies, latencyMs)
		if success == 1 {
			successes++
		}
	}
	if err := rows.Err(); err != nil {
		return ToolReliability{}, fmt.Errorf("tools/telemetry: iterate rows: %w", err)
	}

	total := len(latencies)
	if total == 0 {
		return ToolReliability{ToolName: toolName}, nil
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := percentile(latencies, 50)
	p95 := percentile(latencies, 95)

	rel := ToolReliability{
		ToolName:     toolName,
		SuccessRate:  float32(successes) / float32(total),
		P50LatencyMs: p50,
		P95LatencyMs: p95,
		SampleSize:   total,
	}

	// Query for the most recent error.
	var lastErrText string
	var lastErrAtMs int64
	errRow := db.QueryRowContext(ctx,
		`SELECT error, called_at FROM tool_telemetry
		 WHERE tool_name = ? AND success = 0 AND error != ''
		 ORDER BY called_at DESC LIMIT 1`,
		toolName,
	)
	if scanErr := errRow.Scan(&lastErrText, &lastErrAtMs); scanErr == nil {
		rel.LastError = lastErrText
		rel.LastErrorAt = time.UnixMilli(lastErrAtMs)
	}

	return rel, nil
}

// percentile returns the value at the given percentile (0-100) in a sorted
// ascending slice. Uses nearest-rank method.
func percentile(sorted []int64, p int) int {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	// Nearest-rank: rank = ceil(p/100 * n), clamped to [1,n].
	rank := (p*n + 99) / 100 // integer ceiling of p*n/100
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return int(sorted[rank-1])
}
