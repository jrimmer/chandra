package filesystemtool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jrimmer/chandra/pkg"
)

// sensitiveExtensions require confirmed=true before writing.
var sensitiveExtensions = []string{
	".go", ".toml", ".env", ".sh", ".bash", ".py", ".rb",
	".sql", ".yaml", ".yml", ".json", ".service",
}

type writeFileTool struct{}

// NewWriteFileTool returns a pkg.Tool that writes file contents locally or via SSH/scp.
func NewWriteFileTool() pkg.Tool { return &writeFileTool{} }

func (t *writeFileTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name: "write_file",
		Description: "Write or overwrite a file with the given content. Creates parent directories " +
			"if they don't exist. For source code, config, scripts, and other sensitive file types " +
			"(*.go, *.toml, *.sh, etc.) you MUST set confirmed=true — only do this after the user " +
			"has explicitly approved the change. Safe to use without confirmation for *.md skill files " +
			"and other documentation.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Absolute or relative path to write."
				},
				"content": {
					"type": "string",
					"description": "File content to write."
				},
				"host": {
					"type": "string",
					"description": "Optional SSH target for remote write, e.g. \"deploy@chandra-test\"."
				},
				"confirmed": {
					"type": "boolean",
					"description": "Required true for sensitive file types (*.go, *.toml, *.sh, etc.). Ask the user before setting this."
				}
			},
			"required": ["path", "content"]
		}`),
	}
}

func (t *writeFileTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Host      string `json:"host"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := json.Unmarshal(call.Parameters, &args); err != nil {
		return fsErrResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error()), nil
	}
	if args.Path == "" {
		return fsErrResult(call.ID, pkg.ErrBadInput, "path is required"), nil
	}

	// Confirmation gate for sensitive file types.
	ext := strings.ToLower(filepath.Ext(args.Path))
	needsConfirm := false
	for _, se := range sensitiveExtensions {
		if ext == se {
			needsConfirm = true
			break
		}
	}
	if needsConfirm && !args.Confirmed {
		return pkg.ToolResult{
			ID: call.ID,
			Content: fmt.Sprintf(
				"⚠️ Writing to %q requires confirmation (sensitive file type: %s). "+
					"Show the user the content you intend to write, get their approval, then call write_file again with confirmed=true.",
				args.Path, ext),
		}, nil
	}

	if args.Host != "" {
		// Remote write: pipe content via SSH stdin.
		dir := filepath.Dir(args.Path)
		mkdirCmd := exec.CommandContext(ctx, "ssh",
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=10",
			"-o", "BatchMode=yes",
			args.Host,
			fmt.Sprintf("mkdir -p %q && cat > %q", dir, args.Path),
		)
		mkdirCmd.Stdin = strings.NewReader(args.Content)
		if out, err := mkdirCmd.CombinedOutput(); err != nil {
			return fsErrResult(call.ID, pkg.ErrInternal,
				fmt.Sprintf("remote write failed: %v\n%s", err, string(out))), nil
		}
	} else {
		// Local write.
		if err := os.MkdirAll(filepath.Dir(args.Path), 0755); err != nil {
			return fsErrResult(call.ID, pkg.ErrInternal,
				fmt.Sprintf("mkdir failed: %v", err)), nil
		}
		// Executable bit for shell scripts.
		mode := os.FileMode(0644)
		if ext == ".sh" || ext == ".bash" {
			mode = 0755
		}
		if err := os.WriteFile(args.Path, []byte(args.Content), mode); err != nil {
			return fsErrResult(call.ID, pkg.ErrInternal,
				fmt.Sprintf("write failed: %v", err)), nil
		}
	}

	return pkg.ToolResult{
		ID:      call.ID,
		Content: fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path),
	}, nil
}
