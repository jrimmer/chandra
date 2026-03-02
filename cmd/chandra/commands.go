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

// ---- plan commands ----------------------------------------------------------

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan execution operations",
}

var planListCmd = &cobra.Command{
	Use:   "list",
	Short: "List execution plans",
	Run: func(cmd *cobra.Command, args []string) {
		status, _ := cmd.Flags().GetString("status")
		params := map[string]string{}
		if status != "" {
			params["status"] = status
		}
		call("plan.list", params)
	},
}

var planShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show plan details with tree-formatted steps",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("plan.show", map[string]string{"id": args[0]})
	},
}

var planExtendCmd = &cobra.Command{
	Use:   "extend <id>",
	Short: "Extend a paused plan's checkpoint timeout",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		duration, _ := cmd.Flags().GetString("duration")
		params := map[string]string{"id": args[0]}
		if duration != "" {
			params["duration"] = duration
		}
		call("plan.extend", params)
	},
}

var planDryRunCmd = &cobra.Command{
	Use:   "dry-run <goal>",
	Short: "Decompose a goal into a plan without executing",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("plan.dry_run", map[string]string{"goal": args[0]})
	},
}

var planCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel a running or paused plan",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("plan.cancel", map[string]string{"id": args[0]})
	},
}

var planRunCmd = &cobra.Command{
	Use:   "run <goal>",
	Short: "Decompose a goal into a plan and execute it",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		call("plan.run", map[string]any{"goal": args[0], "dry_run": dryRun})
	},
}

var planResumeCmd = &cobra.Command{
	Use:   "resume <id>",
	Short: "Resume a paused plan from its checkpoint",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("plan.resume", map[string]any{"id": args[0], "approved": true})
	},
}

var planRetryCmd = &cobra.Command{
	Use:   "retry <id>",
	Short: "Retry a failed plan from its failed step",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("plan.retry", map[string]string{"id": args[0]})
	},
}

var planRollbackCmd = &cobra.Command{
	Use:   "rollback <id>",
	Short: "Rollback a failed plan's completed steps",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("plan.rollback", map[string]string{"id": args[0]})
	},
}

var planAbandonCmd = &cobra.Command{
	Use:   "abandon <id>",
	Short: "Mark a failed plan as complete without rollback",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		call("plan.abandon", map[string]string{"id": args[0]})
	},
}

// ---- infra commands ---------------------------------------------------------

var infraCmd = &cobra.Command{
	Use:   "infra",
	Short: "Infrastructure operations",
}

var infraListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all hosts and services",
	Run: func(cmd *cobra.Command, args []string) {
		call("infra.list", nil)
	},
}

var infraShowCmd = &cobra.Command{
	Use:   "show <host-id>",
	Short: "Show host details",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		reveal, _ := cmd.Flags().GetBool("reveal")
		call("infra.show", map[string]any{"host_id": args[0], "reveal": reveal})
	},
}

var infraDiscoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Run infrastructure discovery scan",
	Run: func(cmd *cobra.Command, args []string) {
		call("infra.discover", nil)
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

	// Plan subcommands.
	planListCmd.Flags().String("status", "", "filter by plan status")
	planExtendCmd.Flags().String("duration", "24h", "extension duration")
	planRunCmd.Flags().Bool("dry-run", false, "decompose without executing")
	planCmd.AddCommand(planListCmd, planShowCmd, planExtendCmd, planDryRunCmd, planCancelCmd, planRunCmd, planResumeCmd, planRetryCmd, planRollbackCmd, planAbandonCmd)
	rootCmd.AddCommand(planCmd)

	// Infra subcommands.
	infraShowCmd.Flags().Bool("reveal", false, "reveal masked credentials")
	infraCmd.AddCommand(infraListCmd, infraShowCmd, infraDiscoverCmd)
	rootCmd.AddCommand(infraCmd)

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
