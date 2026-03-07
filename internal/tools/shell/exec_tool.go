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

// ─── Context key for channel ID ──────────────────────────────────────────────

type ctxKey string

const channelIDKey ctxKey = "exec_channel_id"

// WithChannelID returns a new context with the channelID stored for use by the
// exec tool's approval flow. Called by the agent loop before executor.Execute.
func WithChannelID(ctx context.Context, channelID string) context.Context {
	return context.WithValue(ctx, channelIDKey, channelID)
}

func channelIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(channelIDKey).(string); ok {
		return v
	}
	return ""
}

// ─── Approver interface ───────────────────────────────────────────────────────

// Approver is implemented by the Discord adapter (and any future channel adapter)
// to support interactive exec approval: post a message with ✅/❌ reactions,
// wait for an operator response, and return the decision.
// ApproverFunc is a functional adapter that implements Approver.
// Useful in main.go where a closure captures a late-bound delegate.
type ApproverFunc func(ctx context.Context, channelID, prompt string, timeout time.Duration) (bool, error)

func (f ApproverFunc) RequestApproval(ctx context.Context, channelID, prompt string, timeout time.Duration) (bool, error) {
	return f(ctx, channelID, prompt, timeout)
}

type Approver interface {
	RequestApproval(ctx context.Context, channelID, prompt string, timeout time.Duration) (bool, error)
}

// ─── Dangerous patterns ───────────────────────────────────────────────────────

// dangerousPatterns are hard-blocked regardless of confirmed flag.
// Matched case-insensitively against the full command string.
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

// approvalPatterns are tier-2: not hard-blocked, but require interactive
// operator approval before execution. These cover high-risk but legitimate
// operations where a human should confirm intent.
var approvalPatterns = []struct {
	pattern     string
	description string
}{
	// Remote code execution via pipe-to-shell
	{"| bash", "pipes output to bash (remote code execution risk)"},
	{"| sh", "pipes output to shell (remote code execution risk)"},
	// Service disruption
	{"systemctl stop ", "stops a running system service"},
	{"systemctl disable ", "disables a service from auto-starting"},
	// Firewall changes
	{"iptables ", "modifies kernel firewall rules"},
	{"ufw ", "modifies firewall rules"},
	// System config writes (> /etc/ includes >/etc/ after normalization)
	{"> /etc/", "writes to system configuration directory"},
	{">/etc/", "writes to system configuration directory"},
	{"> /usr/", "writes to system binaries directory"},
	{">/usr/", "writes to system binaries directory"},
}

// ─── Tool ─────────────────────────────────────────────────────────────────────

type execTool struct {
	approver Approver // nil = no interactive approval, pattern gate only
}

// NewExecTool returns a pkg.Tool that runs shell commands with pattern-gate
// protection but no interactive approval flow.
func NewExecTool() pkg.Tool { return &execTool{} }

// NewExecToolWithApproval returns a pkg.Tool that will request interactive
// operator approval (via the Approver) before running tier-2 commands.
func NewExecToolWithApproval(approver Approver) pkg.Tool { return &execTool{approver: approver} }

func (t *execTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name: "exec",
		Description: "Run a shell command locally or on a remote host via SSH. " +
			"Use for: building code (go build, make), running tests (go test ./...), " +
			"git operations (git add/commit/push), system management (systemctl, pkill, cp), " +
			"checking logs, running CLIs (hetzner, gh, kubectl), and any shell operation. " +
			"For remote execution provide host (e.g. \"deploy@chandra-test\", \"root@10.1.0.10\"). " +
			"Hard-blocked commands (rm -rf, shutdown, mkfs, etc.) are never permitted. " +
			"High-risk commands (pipe-to-shell, systemctl stop/disable, firewall changes, " +
			"system config writes) will pause and ask the operator for approval before running.",
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
				}
			},
			"required": ["command"]
		}`),
	}
}

func (t *execTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		Command string `json:"command"`
		Host    string `json:"host"`
		WorkDir string `json:"workdir"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(call.Parameters, &args); err != nil {
		return shellErrResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error()), nil
	}
	if args.Command == "" {
		return shellErrResult(call.ID, pkg.ErrBadInput, "command is required"), nil
	}

	cmdLower := strings.ToLower(args.Command)

	// Tier 1: hard block — never execute regardless of any flag.
	for _, pattern := range dangerousPatterns {
		if strings.Contains(cmdLower, strings.ToLower(pattern)) {
			return pkg.ToolResult{
				ID: call.ID,
				Content: fmt.Sprintf(
					"⛔ Command hard-blocked (matches %q). "+
						"This class of command is never permitted.\nCommand: %s",
					pattern, args.Command),
			}, nil
		}
	}

	// Tier 2: requires interactive operator approval.
	if t.approver != nil {
		for _, ap := range approvalPatterns {
			if strings.Contains(cmdLower, strings.ToLower(ap.pattern)) {
				channelID := channelIDFromCtx(ctx)
				if channelID == "" {
					// No channel context — fall back to soft block with explanation.
					return pkg.ToolResult{
						ID: call.ID,
						Content: fmt.Sprintf(
							"⚠️ Command requires operator approval (%s) but no channel context is available. "+
								"Explain what you intend to run and ask the user to confirm explicitly before retrying.\nCommand: %s",
							ap.description, args.Command),
					}, nil
				}

				prompt := fmt.Sprintf(
					"**Exec approval required**\n"+
						"Risk: %s\n"+
						"```\n%s\n```\n"+
						"React ✅ to approve or ❌ to deny (60s timeout).",
					ap.description, args.Command)

				slog.Info("exec: requesting operator approval",
					"command", args.Command, "risk", ap.description, "channel", channelID)

				approved, err := t.approver.RequestApproval(ctx, channelID, prompt, 60*time.Second)
				if err != nil {
					return shellErrResult(call.ID, pkg.ErrInternal,
						fmt.Sprintf("approval request failed: %v", err)), nil
				}
				if !approved {
					return pkg.ToolResult{
						ID:      call.ID,
						Content: "❌ Operator declined — command not executed.",
					}, nil
				}

				slog.Info("exec: approval granted", "command", args.Command)
				break
			}
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
