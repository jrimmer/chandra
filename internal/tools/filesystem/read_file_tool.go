// Package filesystemtool provides read_file and write_file built-in tools.
package filesystemtool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/jrimmer/chandra/pkg"
)

type readFileTool struct{}

// NewReadFileTool returns a pkg.Tool that reads file contents locally or via SSH.
func NewReadFileTool() pkg.Tool { return &readFileTool{} }

func (t *readFileTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name: "read_file",
		Description: "Read the contents of a file. Use for reading source code, config files, " +
			"logs, scripts, or any text file on the local system or a remote host. " +
			"Returns the file content as a string, truncated at max_bytes (default 100KB).",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Absolute or relative path to the file."
				},
				"host": {
					"type": "string",
					"description": "Optional SSH target, e.g. \"deploy@chandra-test\" or \"root@10.1.0.10\". Omit for local files."
				},
				"max_bytes": {
					"type": "integer",
					"description": "Maximum bytes to read. Defaults to 102400 (100KB)."
				}
			},
			"required": ["path"]
		}`),
	}
}

func (t *readFileTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		Path     string `json:"path"`
		Host     string `json:"host"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(call.Parameters, &args); err != nil {
		return fsErrResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error()), nil
	}
	if args.Path == "" {
		return fsErrResult(call.ID, pkg.ErrBadInput, "path is required"), nil
	}
	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 100 * 1024
	}

	var content []byte
	var readErr error

	if args.Host != "" {
		cmd := exec.CommandContext(ctx, "ssh",
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=10",
			"-o", "BatchMode=yes",
			args.Host,
			"cat", args.Path,
		)
		content, readErr = cmd.Output()
	} else {
		content, readErr = os.ReadFile(args.Path)
	}

	if readErr != nil {
		return fsErrResult(call.ID, pkg.ErrNotFound, fmt.Sprintf("read failed: %v", readErr)), nil
	}

	// Detect binary content by scanning first 512 bytes for null bytes.
	check := content
	if len(check) > 512 {
		check = check[:512]
	}
	for _, b := range check {
		if b == 0 {
			return pkg.ToolResult{
				ID:      call.ID,
				Content: fmt.Sprintf("(binary file, %d bytes — use exec to process)", len(content)),
			}, nil
		}
	}

	truncated := ""
	if len(content) > maxBytes {
		content = content[:maxBytes]
		truncated = fmt.Sprintf("\n\n[truncated at %d bytes — use max_bytes to read more]", maxBytes)
	}

	return pkg.ToolResult{ID: call.ID, Content: string(content) + truncated}, nil
}

// fsErrResult is a package-local helper (shared by write_file).
func fsErrResult(id string, kind pkg.ToolErrorKind, msg string) pkg.ToolResult {
	return pkg.ToolResult{ID: id, Error: &pkg.ToolError{Kind: kind, Message: msg}}
}

// hasBinaryContent is unused but kept for potential future use.
var _ = bytes.Contains
