package skills

import (
	"context"
	"testing"
	"time"
)

func TestCLIExplorer_Explore_LS(t *testing.T) {
	explorer := &CLIExplorer{
		MaxSubcommands: 5,
		MaxDepth:       1,
		CommandTimeout: 5 * time.Second,
		MaxOutputBytes: 64 * 1024,
	}
	caps, err := explorer.Explore(context.Background(), "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caps.Command != "ls" {
		t.Errorf("expected command ls, got %q", caps.Command)
	}
	if caps.HelpOutput == "" {
		t.Error("expected non-empty help output")
	}
}

func TestCLIExplorer_Explore_NonexistentBinary(t *testing.T) {
	explorer := &CLIExplorer{CommandTimeout: 2 * time.Second}
	_, err := explorer.Explore(context.Background(), "nonexistent_binary_xyz")
	if err == nil {
		t.Error("expected error for nonexistent binary")
	}
}

func TestCLIExplorer_Explore_Git(t *testing.T) {
	explorer := &CLIExplorer{
		MaxSubcommands: 3,
		MaxDepth:       1,
		CommandTimeout: 5 * time.Second,
		MaxOutputBytes: 64 * 1024,
	}
	caps, err := explorer.Explore(context.Background(), "git")
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	if len(caps.Subcommands) == 0 {
		t.Error("expected subcommands for git")
	}
	// Should be bounded.
	if len(caps.SubcommandHelp) > 3 {
		t.Errorf("expected at most 3 explored subcommands, got %d", len(caps.SubcommandHelp))
	}
}

func TestParseSubcommands(t *testing.T) {
	help := `usage: git <command>

   clone     Clone a repository
   init      Create an empty Git repository
   add       Add file contents to the index
   commit    Record changes to the repository
`
	subs := parseSubcommands(help)
	if len(subs) < 2 {
		t.Errorf("expected at least 2 subcommands, got %d: %v", len(subs), subs)
	}
}
