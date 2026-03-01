package tools_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/tools"
	"github.com/jrimmer/chandra/store"
)

func TestReliability_ComputesFromTelemetry(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	now := time.Now().UnixMilli()

	// Insert 100 rows: 90 success, 10 failure, all within 30 days.
	for i := 0; i < 100; i++ {
		success := 1
		errText := interface{}(nil)
		if i < 10 {
			success = 0
			errText = "some error"
		}
		id := store.NewID()
		_, err := db.Exec(
			`INSERT INTO tool_telemetry (id, tool_name, called_at, latency_ms, success, error, retries)
			 VALUES (?, ?, ?, ?, ?, ?, 0)`,
			id, "my.tool", now-(int64(i)*1000), int64(10+i), success, errText,
		)
		require.NoError(t, err)
	}

	rel, err := tools.ComputeReliability(ctx, db, "my.tool")
	require.NoError(t, err)

	assert.Equal(t, "my.tool", rel.ToolName)
	assert.Equal(t, 100, rel.SampleSize)
	assert.InDelta(t, 0.9, rel.SuccessRate, 0.001)
	assert.NotEmpty(t, rel.LastError)
	assert.False(t, rel.LastErrorAt.IsZero())
}

func TestReliability_30DayWindow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	oldTimestamp := now - int64(31*24*time.Hour/time.Millisecond)

	// Insert 5 old failures (>30 days ago) — should NOT count.
	for i := 0; i < 5; i++ {
		id := store.NewID()
		_, err := db.Exec(
			`INSERT INTO tool_telemetry (id, tool_name, called_at, latency_ms, success, error, retries)
			 VALUES (?, ?, ?, ?, 0, 'old error', 0)`,
			id, "window.tool", oldTimestamp-int64(i*1000), int64(50),
		)
		require.NoError(t, err)
	}

	// Insert 10 recent successes (within 30 days) — should count.
	for i := 0; i < 10; i++ {
		id := store.NewID()
		_, err := db.Exec(
			`INSERT INTO tool_telemetry (id, tool_name, called_at, latency_ms, success, error, retries)
			 VALUES (?, ?, ?, ?, 1, NULL, 0)`,
			id, "window.tool", now-int64(i*1000), int64(20+i),
		)
		require.NoError(t, err)
	}

	rel, err := tools.ComputeReliability(ctx, db, "window.tool")
	require.NoError(t, err)

	assert.Equal(t, 10, rel.SampleSize, "old rows should be excluded")
	assert.InDelta(t, 1.0, rel.SuccessRate, 0.001, "only recent successes should be included")
}

func TestReliability_LatencyPercentiles(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	now := time.Now().UnixMilli()

	// Insert 100 rows with latencies 1..100 ms.
	for i := 1; i <= 100; i++ {
		id := store.NewID()
		_, err := db.Exec(
			`INSERT INTO tool_telemetry (id, tool_name, called_at, latency_ms, success, error, retries)
			 VALUES (?, ?, ?, ?, 1, NULL, 0)`,
			id, "perc.tool", now-int64(i*1000), int64(i),
		)
		require.NoError(t, err)
	}

	rel, err := tools.ComputeReliability(ctx, db, "perc.tool")
	require.NoError(t, err)

	assert.Equal(t, 100, rel.SampleSize)
	// P50 of 1..100 = 50 (nearest-rank: ceil(0.50*100)=50, sorted[49]=50).
	assert.Equal(t, 50, rel.P50LatencyMs)
	// P95 of 1..100 = 95 (nearest-rank: ceil(0.95*100)=95, sorted[94]=95).
	assert.Equal(t, 95, rel.P95LatencyMs)
}

func TestReliability_EmptyData(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	rel, err := tools.ComputeReliability(ctx, db, "nonexistent.tool")
	require.NoError(t, err)
	assert.Equal(t, "nonexistent.tool", rel.ToolName)
	assert.Equal(t, 0, rel.SampleSize)
	assert.Equal(t, float32(0.0), rel.SuccessRate)
}
