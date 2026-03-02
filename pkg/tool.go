package pkg

import (
	"context"
	"encoding/json"
)

type ToolTier int

const (
	TierBuiltin  ToolTier = 1
	TierTrusted  ToolTier = 2
	TierIsolated ToolTier = 3 // out-of-process, restricted interface
)

type Capability string

const (
	CapMemoryRead  Capability = "memory:read"
	CapMemoryWrite Capability = "memory:write"
	CapNetworkOut  Capability = "network:outbound"
	CapChannelSend Capability = "channel:send"
	CapFileRead    Capability = "file:read"
	CapFileWrite   Capability = "file:write"
	CapProcessExec Capability = "process:exec"
)

type ToolDef struct {
	Name         string          // unique identifier, e.g. "homeassistant.set_state"
	Description  string          // shown to LLM in tool selection
	Parameters   json.RawMessage // JSON Schema describing input parameters
	Tier         ToolTier
	Capabilities []Capability // declared at registration, enforced by runtime
}

type ToolCall struct {
	ID         string
	Name       string
	Parameters json.RawMessage
	SkillName  string // Which skill triggered this call (empty for direct)
	PlanID     string // Which plan this belongs to (empty if ad-hoc)
	StepIndex  int    // Step index within plan
}

// ToolErrorKind distinguishes retry-able from terminal errors
type ToolErrorKind int

const (
	ErrTransient ToolErrorKind = iota // network, timeout, rate limit — retry
	ErrBadInput                       // invalid parameters — don't retry
	ErrAuth                           // authentication failure — don't retry
	ErrNotFound                       // resource not found — don't retry
	ErrInternal                       // unexpected error — log, maybe retry once
)

type ToolError struct {
	Kind    ToolErrorKind
	Message string
	Cause   error
}

func (e *ToolError) Error() string { return e.Message }
func (e *ToolError) Unwrap() error { return e.Cause }

type ToolResult struct {
	ID      string
	Content string
	Error   *ToolError     // typed error, nil on success
	Meta    map[string]any // latency, retries, etc — populated by executor
}

type Tool interface {
	Definition() ToolDef
	Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}
