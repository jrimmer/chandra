package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
)

// ErrTransient is a sentinel that test mocks (and real tools) can wrap with
// pkg.ToolError{Kind: pkg.ErrTransient} to signal a retryable failure.
var ErrTransient = errors.New("transient error")

// ErrBadInput is a sentinel that test mocks (and real tools) can wrap with
// pkg.ToolError{Kind: pkg.ErrBadInput} to signal a non-retryable input error.
var ErrBadInput = errors.New("bad input")

// Executor dispatches tool calls in parallel with retry, timeout, and telemetry.
type Executor interface {
	Execute(ctx context.Context, calls []pkg.ToolCall) []pkg.ToolResult
}

var _ Executor = (*executor)(nil)

type executor struct {
	registry       Registry
	db             *sql.DB
	defaultTimeout time.Duration
	grantedCaps    []pkg.Capability
}

// NewExecutor creates an Executor backed by the given registry and database.
// defaultTimeout is applied to each individual tool call.
func NewExecutor(registry Registry, db *sql.DB, defaultTimeout time.Duration) *executor {
	if db == nil {
		panic("tools: NewExecutor requires non-nil db")
	}
	return &executor{
		registry:       registry,
		db:             db,
		defaultTimeout: defaultTimeout,
		grantedCaps:    nil,
	}
}

// WithGrantedCapabilities sets the capabilities that have been externally
// granted for this executor and returns the receiver for chaining.
// When grantedCaps is nil or empty, capability enforcement is skipped
// (all capabilities are considered granted by default).
func (e *executor) WithGrantedCapabilities(caps []pkg.Capability) *executor {
	e.grantedCaps = caps
	return e
}

// Execute dispatches all calls concurrently, returning results in the same
// order as the input slice. Each call is subject to capability enforcement,
// a per-call timeout, and up to 2 retries on transient errors.
func (e *executor) Execute(ctx context.Context, calls []pkg.ToolCall) []pkg.ToolResult {
	results := make([]pkg.ToolResult, len(calls))
	var wg sync.WaitGroup

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, c pkg.ToolCall) {
			defer wg.Done()
			results[idx] = e.dispatchOne(ctx, c)
		}(i, call)
	}

	wg.Wait()
	return results
}

// dispatchOne executes a single tool call with capability enforcement,
// timeout, retry logic, and telemetry recording.
func (e *executor) dispatchOne(ctx context.Context, call pkg.ToolCall) pkg.ToolResult {
	tool, ok := e.registry.Get(call.Name)
	if !ok {
		result := pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrBadInput,
				Message: fmt.Sprintf("unknown tool: %q", call.Name),
			},
		}
		e.recordTelemetry(call.Name, false, 0, result.Error.Message, 0)
		return result
	}

	// Enforce capabilities before any execution attempt.
	// When grantedCaps is nil/empty, enforcement is skipped (all capabilities
	// are granted by default; session-level enforcement happens in Phase 14).
	if len(e.grantedCaps) > 0 {
		if err := e.registry.EnforceCapabilities(call, e.grantedCaps); err != nil {
			result := pkg.ToolResult{
				ID: call.ID,
				Error: &pkg.ToolError{
					Kind:    pkg.ErrBadInput,
					Message: err.Error(),
				},
			}
			e.recordTelemetry(call.Name, false, 0, result.Error.Message, 0)
			return result
		}
	}

	const maxAttempts = 3
	backoffs := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}

	var (
		result   pkg.ToolResult
		execErr  error
		attempts int
		start    = time.Now()
	)

	for attempts = 1; attempts <= maxAttempts; attempts++ {
		callCtx, cancel := context.WithTimeout(ctx, e.defaultTimeout)
		result, execErr = tool.Execute(callCtx, call)
		cancel()

		if execErr == nil {
			break
		}

		// Decide whether to retry based on error kind.
		if isTransient(execErr) && attempts < maxAttempts {
			timer := time.NewTimer(backoffs[attempts-1])
			select {
			case <-ctx.Done():
				// Parent context cancelled; abort retries.
				timer.Stop()
				execErr = ctx.Err()
				goto done
			case <-timer.C:
			}
			continue
		}

		// Non-transient error or max attempts reached — stop retrying.
		break
	}

done:
	latencyMs := time.Since(start).Milliseconds()

	var errText string
	success := execErr == nil
	if !success {
		var toolErr *pkg.ToolError
		if errors.As(execErr, &toolErr) {
			errText = toolErr.Message
		} else {
			errText = execErr.Error()
		}
		if result.Error == nil {
			result.Error = &pkg.ToolError{
				Kind:    pkg.ErrInternal,
				Message: errText,
				Cause:   execErr,
			}
		}
		result.ID = call.ID
	}

	e.recordTelemetry(call.Name, success, latencyMs, errText, attempts-1)

	if result.Meta == nil {
		result.Meta = make(map[string]any)
	}
	result.Meta["retries"] = attempts - 1
	result.Meta["latency_ms"] = latencyMs

	return result
}

// isTransient returns true if err signals a retryable condition.
// It checks for pkg.ToolError with Kind==ErrTransient, context errors,
// and the ErrTransient sentinel.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	// Check for context cancellation / deadline — not retryable.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var toolErr *pkg.ToolError
	if errors.As(err, &toolErr) {
		return toolErr.Kind == pkg.ErrTransient
	}
	// Fallback: check for the ErrTransient sentinel directly.
	return errors.Is(err, ErrTransient)
}

// recordTelemetry inserts a row into tool_telemetry.
func (e *executor) recordTelemetry(toolName string, success bool, latencyMs int64, errText string, retries int) {
	id := store.NewID()
	calledAt := time.Now().UnixMilli()

	successInt := 0
	if success {
		successInt = 1
	}

	var errVal any
	if errText != "" {
		errVal = errText
	}

	// Use a background context so a cancelled per-call context doesn't drop telemetry.
	if _, err := e.db.ExecContext(context.Background(),
		`INSERT INTO tool_telemetry (id, tool_name, called_at, latency_ms, success, error, retries)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, toolName, calledAt, latencyMs, successInt, errVal, retries,
	); err != nil {
		slog.Warn("tools: failed to record telemetry", "tool", toolName, "error", err)
	}
}
