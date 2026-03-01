package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/jrimmer/chandra/pkg"
)

// defaultDialTimeout is the time allowed to connect to a sandbox socket.
const defaultDialTimeout = 5 * time.Second

// Runner executes tools in an out-of-process sandbox over a Unix socket.
// The sandbox binary must be started separately; Runner only connects to it.
type Runner struct {
	socketPath string
	timeout    time.Duration
}

// NewRunner creates a runner that will connect to the sandbox at socketPath.
// timeout is the per-call deadline (0 = use context deadline only).
func NewRunner(socketPath string, timeout time.Duration) *Runner {
	return &Runner{socketPath: socketPath, timeout: timeout}
}

// Execute sends a ToolCall to the sandbox and returns the result.
// The runner opens a new connection per call for isolation (no persistent state).
func (r *Runner) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	conn, err := net.DialTimeout("unix", r.socketPath, defaultDialTimeout)
	if err != nil {
		return pkg.ToolResult{}, fmt.Errorf("sandbox: dial %s: %w", r.socketPath, err)
	}
	defer conn.Close()

	if r.timeout > 0 {
		conn.SetDeadline(time.Now().Add(r.timeout))
	}

	req := pkg.SandboxRequest{
		CallID:     call.ID,
		ToolName:   call.Name,
		Parameters: call.Parameters,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return pkg.ToolResult{}, fmt.Errorf("sandbox: send request: %w", err)
	}

	var resp pkg.SandboxResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return pkg.ToolResult{}, fmt.Errorf("sandbox: read response: %w", err)
	}

	result := pkg.ToolResult{ID: resp.CallID, Content: resp.Content}
	if resp.Error != "" {
		result.Error = &pkg.ToolError{Kind: pkg.ErrInternal, Message: resp.Error}
	}
	return result, nil
}
