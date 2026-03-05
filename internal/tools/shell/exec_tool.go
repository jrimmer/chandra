// Package shelltool provides the exec built-in tool.
package shelltool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/jrimmer/chandra/pkg"
)

// dangerousPatterns are command substrings that require confirmed=true.
// Matched case-insensitively.
var dangerousPatterns = []string{
	"rm -rf",
	"rm -r /",
	"mkfs",
	"dd if=/dev/zero",
	"dd if=/dev/urandom",
	":(){:|:&};:",
	"shutdown",
	"reboot",
	"halt",
	"poweroff",
	"drop table",
	"drop database",
	"truncate table",
}

type execTool struct{}

// NewExecTool returns a pkg.Tool that runs shell commands locally or via SSH.
func NewExecTool() pkg.Tool { return &execTool{} }

func (t *execTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name: "exec",
		Description: "Run a shell command locally or on a remote host via SSH. " +
			"Use for: building code (go build, make), running tests (go test ./...), " +
			"git operations (git add/commit/push), system management (systemctl, pkill, cp), " +
			"checking logs, running CLIs (hetzner, gh, kubectl), and any shell operation. " +
			"For remote execution provide host (e.g. \"deploy@chandra-test\", \"root@10.1.0.10\"). " +
			"Dangerous commands (rm -rf, shutdown, mkfs, etc.) require confirmed=true — " +
			"always get explicit user approval before setting confirmed=true.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {
					"type": "string",
					"description": "Shell command to run. Executed via bash -c locally, or the remote shell via SSH."
				},
				"host": {
					"type": "string",
					"description": "Optional SSH target. e.g. \"deploy@chandra-test\", \"root@10.1.0.10\". Omit to run locally."
				},
				"workdir": {
					"type": "string",
					"description": "Working directory for local commands. Ignored for remote."
				},
				"timeout_seconds": {
					"type": "integer",
					"description": "Timeout in seconds. Default 120. Use higher values for long builds."
				},
				"confirmed": {
					"type": "boolean",
					"description": "Set true to authorize dangerous commands after explicit user approval."
				}
			},
			"required": ["command"]
		}`),
	}
}

func (t *execTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		Command   string `json:"command"`
		Host      string `json:"host"`
		WorkDir   string `json:"workdir"`
		Timeout   int    `json:"timeout_seconds"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := json.Unmarshal(call.Parameters, &args); err != nil {
		return shellErrResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error()), nil
	}
	if args.Command == "" {
		return shellErrResult(call.ID, pkg.ErrBadInput, "command is required"), nil
	}

	// Dangerous pattern check.
	cmdLower := strings.ToLower(args.Command)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(cmdLower, strings.ToLower(pattern)) {
			if !args.Confirmed {
				return pkg.ToolResult{
					ID: call.ID,
					Content: fmt.Sprintf(
						"⚠️ Dangerous command detected (matches %q). "+
							"Show the user what you intend to run, get explicit approval, "+
							"then call exec again with confirmed=true.\nCommand: %s",
						pattern, args.Command),
				}, nil
			}
			slog.Warn("exec: running dangerous command with confirmation",
				"command", args.Command, "host", args.Host)
			break
		}
	}

	timeout := time.Duration(args.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if args.Host != "" {
		slog.Info("exec: running remote command", "host", args.Host, "command", args.Command)
		cmd = exec.CommandContext(tctx, "ssh",
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=10",
			"-o", "BatchMode=yes",
			args.Host,
			args.Command,
		)
	} else {
		slog.Info("exec: running local command", "command", args.Command)
		cmd = exec.CommandContext(tctx, "bash", "-c", args.Command)
		if args.WorkDir != "" {
			cmd.Dir = args.WorkDir
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	combined := stdout.String()
	if stderr.Len() > 0 {
		if combined != "" {
			combined += "\n--- stderr ---\n"
		}
		combined += stderr.String()
	}

	// Truncate large output.
	const maxOut = 10 * 1024
	if len(combined) > maxOut {
		combined = combined[:maxOut] + "\n[output truncated at 10KB]"
	}

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return shellErrResult(call.ID, pkg.ErrInternal,
				fmt.Sprintf("exec failed: %v\n%s", runErr, combined)), nil
		}
	}

	result := fmt.Sprintf("exit_code: %d\n%s", exitCode, combined)
	return pkg.ToolResult{ID: call.ID, Content: result}, nil
}

func shellErrResult(id string, kind pkg.ToolErrorKind, msg string) pkg.ToolResult {
	return pkg.ToolResult{ID: id, Error: &pkg.ToolError{Kind: kind, Message: msg}}
}
