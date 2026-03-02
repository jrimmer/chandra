package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jrimmer/chandra/internal/api"
)

// call is a helper that creates an api.Client, calls the given method with
// params, and pretty-prints the result to stdout. On any error it writes to
// stderr and exits 1.
func call(method string, params any) {
	client := api.NewClient(api.SocketPath())
	var result json.RawMessage
	if err := client.Call(context.Background(), method, params, &result); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if result == nil {
		return
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: marshal output: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

// rootCmd is the top-level chandra command.
var rootCmd = &cobra.Command{
	Use:   "chandra",
	Short: "Chandra AI agent CLI",
	Long:  "chandra is the command-line interface for the Chandra AI agent daemon (chandrad).",
}

// ---- daemon commands --------------------------------------------------------

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon (sends daemon.start)",
	Run: func(cmd *cobra.Command, args []string) {
		call("daemon.start", nil)
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the daemon",
	Run: func(cmd *cobra.Command, args []string) {
		call("daemon.stop", nil)
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print daemon status",
	Run: func(cmd *cobra.Command, args []string) {
		call("daemon.status", nil)
	},
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Print daemon health",
	Run: func(cmd *cobra.Command, args []string) {
		call("daemon.health", nil)
	},
}

// ---- memory commands --------------------------------------------------------

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Memory operations",
}

var memorySearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search semantic memory",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("memory.search", map[string]string{"query": args[0]})
	},
}

// ---- intent commands --------------------------------------------------------

var intentCmd = &cobra.Command{
	Use:   "intent",
	Short: "Intent operations",
}

var intentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active intents",
	Run: func(cmd *cobra.Command, args []string) {
		call("intent.list", nil)
	},
}

var intentAddCmd = &cobra.Command{
	Use:   "add <description>",
	Short: "Add a new intent",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("intent.add", map[string]string{"description": args[0]})
	},
}

var intentCompleteCmd = &cobra.Command{
	Use:   "complete <id>",
	Short: "Mark an intent as complete",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("intent.complete", map[string]string{"id": args[0]})
	},
}

// ---- tool commands ----------------------------------------------------------

var toolCmd = &cobra.Command{
	Use:   "tool",
	Short: "Tool operations",
}

var toolListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered tools",
	Run: func(cmd *cobra.Command, args []string) {
		call("tool.list", nil)
	},
}

var toolTelemetryCmd = &cobra.Command{
	Use:   "telemetry <name>",
	Short: "Print telemetry for a tool",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("tool.telemetry", map[string]string{"name": args[0]})
	},
}

// ---- skill commands ---------------------------------------------------------

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Skill operations",
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List loaded skills",
	Run: func(cmd *cobra.Command, args []string) {
		call("skill.list", nil)
	},
}

var skillShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show details of a skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("skill.show", map[string]string{"name": args[0]})
	},
}

var skillReloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload skills from disk",
	Run: func(cmd *cobra.Command, args []string) {
		call("skill.reload", nil)
	},
}

var skillPendingCmd = &cobra.Command{
	Use:   "pending",
	Short: "List skills pending review",
	Run: func(cmd *cobra.Command, args []string) {
		call("skill.pending", nil)
	},
}

var skillApproveCmd = &cobra.Command{
	Use:   "approve <name>",
	Short: "Approve a generated skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("skill.approve", map[string]string{"name": args[0]})
	},
}

var skillRejectCmd = &cobra.Command{
	Use:   "reject <name>",
	Short: "Reject a generated skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("skill.reject", map[string]string{"name": args[0]})
	},
}

// ---- log command ------------------------------------------------------------

// logFlags holds the parsed flags for the log command.
var logFlags struct {
	today bool
	tail  int
	day   string
	week  bool
	drill string
}

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Query the action log",
	Run: func(cmd *cobra.Command, args []string) {
		f := &logFlags
		switch {
		case f.drill != "":
			call("log.drill", map[string]string{"id": f.drill})
		case f.today:
			call("log.today", nil)
		case f.week:
			call("log.week", nil)
		case f.tail > 0:
			call("log.tail", map[string]int{"n": f.tail})
		case f.day != "":
			call("log.day", map[string]string{"date": f.day})
		default:
			fmt.Fprintln(os.Stderr, "error: specify one of --today, --tail N, --day YYYY-MM-DD, --week, or --drill <id>")
			os.Exit(1)
		}
	},
}

// ---- confirm command --------------------------------------------------------

var confirmCmd = &cobra.Command{
	Use:   "confirm <id>",
	Short: "Approve a pending confirmation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("confirm.approve", map[string]string{"id": args[0]})
	},
}

// init registers all subcommands on rootCmd and wires up flags.
func init() {
	// Daemon commands.
	rootCmd.AddCommand(startCmd, stopCmd, statusCmd, healthCmd)

	// Memory subcommands.
	memoryCmd.AddCommand(memorySearchCmd)
	rootCmd.AddCommand(memoryCmd)

	// Intent subcommands.
	intentCmd.AddCommand(intentListCmd, intentAddCmd, intentCompleteCmd)
	rootCmd.AddCommand(intentCmd)

	// Tool subcommands.
	toolCmd.AddCommand(toolListCmd, toolTelemetryCmd)
	rootCmd.AddCommand(toolCmd)

	// Skill subcommands.
	skillCmd.AddCommand(skillListCmd, skillShowCmd, skillReloadCmd, skillPendingCmd, skillApproveCmd, skillRejectCmd)
	rootCmd.AddCommand(skillCmd)

	// Log flags.
	logCmd.Flags().BoolVar(&logFlags.today, "today", false, "show today's log")
	logCmd.Flags().IntVar(&logFlags.tail, "tail", 0, "show last N log entries")
	logCmd.Flags().StringVar(&logFlags.day, "day", "", "show log for YYYY-MM-DD")
	logCmd.Flags().BoolVar(&logFlags.week, "week", false, "show this week's log")
	logCmd.Flags().StringVar(&logFlags.drill, "drill", "", "drill into a specific log entry by id")
	rootCmd.AddCommand(logCmd)

	// Confirm command.
	rootCmd.AddCommand(confirmCmd)
}
