package pkg

import "encoding/json"

// SandboxRequest is the wire format for Tier 3 (out-of-process) tool calls.
// The runtime sends this over a Unix socket to the sandbox binary.
type SandboxRequest struct {
	CallID     string          `json:"call_id"`   // matches ToolCall.ID
	ToolName   string          `json:"tool_name"`
	Parameters json.RawMessage `json:"parameters"`
}

// SandboxResponse is the wire format for Tier 3 tool results.
// The sandbox binary sends this back over the Unix socket.
type SandboxResponse struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"` // empty on success
}
