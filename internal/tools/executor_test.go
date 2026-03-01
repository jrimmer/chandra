package tools_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/tools"
	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
)

func init() {
	vec.Auto()
}

// newTestDB opens a migrated SQLite database in a temp dir for tests.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	db := s.DB()
	t.Cleanup(func() { db.Close() })
	return db
}

// slowTool sleeps for the given duration then succeeds.
type slowTool struct {
	name  string
	sleep time.Duration
}

func (s *slowTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{Name: s.name, Tier: pkg.TierBuiltin}
}
func (s *slowTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	select {
	case <-time.After(s.sleep):
		return pkg.ToolResult{ID: call.ID, Content: "ok"}, nil
	case <-ctx.Done():
		return pkg.ToolResult{ID: call.ID}, ctx.Err()
	}
}

// countingTransientTool returns ErrTransient for the first `failTimes` calls
// then succeeds. It uses an atomic counter to be goroutine-safe.
type countingTransientTool struct {
	name      string
	failTimes int32
	calls     atomic.Int32
}

func (c *countingTransientTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{Name: c.name, Tier: pkg.TierBuiltin}
}
func (c *countingTransientTool) Execute(_ context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	n := c.calls.Add(1)
	if n <= c.failTimes {
		return pkg.ToolResult{}, &pkg.ToolError{Kind: pkg.ErrTransient, Message: "transient"}
	}
	return pkg.ToolResult{ID: call.ID, Content: "ok"}, nil
}

// badInputTool always returns ErrBadInput.
type badInputTool struct {
	name  string
	calls atomic.Int32
}

func (b *badInputTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{Name: b.name, Tier: pkg.TierBuiltin}
}
func (b *badInputTool) Execute(_ context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	b.calls.Add(1)
	return pkg.ToolResult{}, &pkg.ToolError{Kind: pkg.ErrBadInput, Message: "bad input"}
}

// newRegistryWithTool creates a registry, registers the given tool, and returns it.
func newRegistryWithTool(t *testing.T, tool pkg.Tool) tools.Registry {
	t.Helper()
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)
	require.NoError(t, reg.Register(tool))
	return reg
}

func TestExecutor_ParallelDispatch(t *testing.T) {
	db := newTestDB(t)

	// Register three slow tools (each takes 50ms).
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)
	for _, name := range []string{"tool.a", "tool.b", "tool.c"} {
		require.NoError(t, reg.Register(&slowTool{name: name, sleep: 50 * time.Millisecond}))
	}

	exec := tools.NewExecutor(reg, db, 5*time.Second)

	calls := []pkg.ToolCall{
		{ID: "1", Name: "tool.a"},
		{ID: "2", Name: "tool.b"},
		{ID: "3", Name: "tool.c"},
	}

	start := time.Now()
	results := exec.Execute(context.Background(), calls)
	elapsed := time.Since(start)

	// All three run in parallel so total time should be well under 150ms.
	assert.Less(t, elapsed, 150*time.Millisecond, "calls should execute in parallel")
	require.Len(t, results, 3)
	for _, r := range results {
		assert.Nil(t, r.Error)
	}
}

func TestExecutor_RetriesTransientErrors(t *testing.T) {
	db := newTestDB(t)

	// Tool fails twice then succeeds on 3rd attempt.
	tool := &countingTransientTool{name: "retry.tool", failTimes: 2}
	reg := newRegistryWithTool(t, tool)

	// Use short timeout so retries don't slow down the test much.
	exec := tools.NewExecutor(reg, db, 5*time.Second)

	results := exec.Execute(context.Background(), []pkg.ToolCall{{ID: "r1", Name: "retry.tool"}})
	require.Len(t, results, 1)

	assert.Nil(t, results[0].Error, "should eventually succeed")
	assert.Equal(t, "ok", results[0].Content)
	assert.Equal(t, int32(3), tool.calls.Load(), "should have been called 3 times total")
}

func TestExecutor_NoRetryOnBadInput(t *testing.T) {
	db := newTestDB(t)

	tool := &badInputTool{name: "bad.tool"}
	reg := newRegistryWithTool(t, tool)

	exec := tools.NewExecutor(reg, db, 5*time.Second)

	results := exec.Execute(context.Background(), []pkg.ToolCall{{ID: "b1", Name: "bad.tool"}})
	require.Len(t, results, 1)

	assert.NotNil(t, results[0].Error, "should have error")
	assert.Equal(t, int32(1), tool.calls.Load(), "should have been called only once")
}

func TestExecutor_Timeout(t *testing.T) {
	db := newTestDB(t)

	// Tool sleeps 5s but timeout is 100ms.
	tool := &slowTool{name: "slow.tool", sleep: 5 * time.Second}
	reg := newRegistryWithTool(t, tool)

	exec := tools.NewExecutor(reg, db, 100*time.Millisecond)

	start := time.Now()
	results := exec.Execute(context.Background(), []pkg.ToolCall{{ID: "t1", Name: "slow.tool"}})
	elapsed := time.Since(start)

	require.Len(t, results, 1)
	assert.NotNil(t, results[0].Error, "should have timeout error")
	assert.Less(t, elapsed, 2*time.Second, "should timeout quickly")
}

func TestExecutor_RecordsTelemetry(t *testing.T) {
	db := newTestDB(t)

	tool := &slowTool{name: "telemetry.tool", sleep: 0}
	reg := newRegistryWithTool(t, tool)

	exec := tools.NewExecutor(reg, db, 5*time.Second)
	exec.Execute(context.Background(), []pkg.ToolCall{{ID: "tel1", Name: "telemetry.tool"}})

	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM tool_telemetry WHERE tool_name = 'telemetry.tool'`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "should have recorded one telemetry row")

	var success int
	err = db.QueryRow(`SELECT success FROM tool_telemetry WHERE tool_name = 'telemetry.tool'`).Scan(&success)
	require.NoError(t, err)
	assert.Equal(t, 1, success, "should be recorded as successful")
}
