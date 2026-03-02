package skills

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// CLICapabilities holds the discovered capabilities of a CLI tool.
type CLICapabilities struct {
	Command        string
	Version        string
	HelpOutput     string
	Subcommands    []string
	SubcommandHelp map[string]string
	HasJSON        bool
	HasVerbose     bool
	Truncated      bool
}

// CLIExplorer discovers CLI tool capabilities via --help and --version.
type CLIExplorer struct {
	MaxSubcommands int
	MaxDepth       int
	CommandTimeout time.Duration
	MaxOutputBytes int
}

// Explore discovers the capabilities of a CLI command.
func (e *CLIExplorer) Explore(ctx context.Context, command string) (*CLICapabilities, error) {
	if _, err := exec.LookPath(command); err != nil {
		return nil, fmt.Errorf("command not found: %s", command)
	}

	if e.MaxSubcommands == 0 {
		e.MaxSubcommands = 20
	}
	if e.MaxDepth == 0 {
		e.MaxDepth = 2
	}
	if e.CommandTimeout == 0 {
		e.CommandTimeout = 5 * time.Second
	}
	if e.MaxOutputBytes == 0 {
		e.MaxOutputBytes = 64 * 1024
	}

	caps := &CLICapabilities{
		Command:        command,
		SubcommandHelp: make(map[string]string),
	}

	// Get version.
	vCtx, cancel := context.WithTimeout(ctx, e.CommandTimeout)
	version, _ := e.execLimited(vCtx, command, "--version")
	cancel()
	caps.Version = strings.TrimSpace(version)

	// Get help.
	hCtx, cancel := context.WithTimeout(ctx, e.CommandTimeout)
	help, _ := e.execLimited(hCtx, command, "--help")
	cancel()
	caps.HelpOutput = help
	caps.HasJSON = strings.Contains(help, "--json")
	caps.HasVerbose = strings.Contains(help, "--verbose")

	// Parse subcommands from help output.
	caps.Subcommands = parseSubcommands(help)

	// Explore subcommands (bounded).
	explored := 0
	for _, sub := range caps.Subcommands {
		if explored >= e.MaxSubcommands {
			caps.Truncated = true
			break
		}
		sCtx, sCancel := context.WithTimeout(ctx, e.CommandTimeout)
		subHelp, err := e.execLimited(sCtx, command, sub, "--help")
		sCancel()
		if err == nil && subHelp != "" {
			caps.SubcommandHelp[sub] = subHelp
			explored++
		}
	}

	return caps, nil
}

func (e *CLIExplorer) execLimited(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	output := buf.String()
	if len(output) > e.MaxOutputBytes {
		output = output[:e.MaxOutputBytes]
	}
	return output, err
}

// subcommandRe matches common help output patterns for subcommands:
// "   command     Description text" with leading whitespace.
var subcommandRe = regexp.MustCompile(`(?m)^\s{2,}([a-z][\w-]{2,})\s{2,}`)

// parseSubcommands extracts subcommand names from --help output.
func parseSubcommands(helpOutput string) []string {
	matches := subcommandRe.FindAllStringSubmatch(helpOutput, -1)
	seen := make(map[string]bool)
	var subs []string
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			subs = append(subs, name)
		}
	}
	return subs
}
