package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jrimmer/chandra/internal/api"
	"github.com/jrimmer/chandra/internal/channels/discord"
	"github.com/jrimmer/chandra/internal/config"
	"github.com/jrimmer/chandra/internal/doctor"
	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver
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
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Skip first-run check for commands that don't need config.
		switch cmd.Name() {
		case "init", "help", "version":
			return
		}
		// If running a daemon command (start, stop, status, health), skip.
		// Those connect to a running daemon, not the config.
		switch cmd.Name() {
		case "start", "stop", "status", "health":
			return
		}
		cfgPath := resolveDefaultConfigPath()
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "\nNo configuration found at %s\n\n", cfgPath)
			fmt.Fprintln(os.Stderr, "Run 'chandra init' to set up Chandra, or see 'chandra --help'.")
			os.Exit(1)
		}
	},
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

// ---- provider commands -------------------------------------------------------

var providerCmd = &cobra.Command{
	Use:   "provider",
	Short: "Provider operations",
}

var providerTestVerbose bool

var providerTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Test provider connection and API key",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath := resolveDefaultConfigPath()
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Testing %s connection (%s)...\n", cfg.Provider.Type, cfg.Provider.BaseURL)
		check := doctor.NewProviderCheck(&cfg.Provider)
		result := check.Run(cmd.Context())

		if result.Status == doctor.Pass {
			fmt.Printf("✓ %s\n", result.Detail)
		} else {
			fmt.Printf("✗ %s\n", result.Detail)
			if result.Fix != "" {
				fmt.Printf("  Fix: %s\n", result.Fix)
			}
			os.Exit(1)
		}
	},
}

// ---- channel commands -------------------------------------------------------

var channelCmd = &cobra.Command{
	Use:   "channel",
	Short: "Channel operations",
}

var channelTestCmd = &cobra.Command{
	Use:   "test [channel]",
	Short: "Send a Hello World loop test to a channel",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if args[0] != "discord" {
			fmt.Fprintf(os.Stderr, "error: only 'discord' is supported\n")
			os.Exit(1)
		}

		cfgPath := resolveDefaultConfigPath()
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
			os.Exit(1)
		}

		if cfg.Channels.Discord == nil || cfg.Channels.Discord.BotToken == "" {
			fmt.Fprintf(os.Stderr, "error: discord not configured (channels.discord.bot_token is empty)\n")
			os.Exit(1)
		}

		if len(cfg.Channels.Discord.ChannelIDs) == 0 {
			fmt.Fprintf(os.Stderr, "error: no channel IDs configured (channels.discord.channel_ids)\n")
			os.Exit(1)
		}

		channelID := cfg.Channels.Discord.ChannelIDs[0]
		fmt.Printf("Sending verification message to channel %s...\n", channelID)
		fmt.Println("Waiting for your reply (2 min timeout)...")

		opts := discord.DefaultVerifyOptions()
		result, err := discord.RunLoopTest(cmd.Context(), cfg.Channels.Discord.BotToken, channelID, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ Loop test failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "  Config saved but channel loop is unverified.")
			os.Exit(1)
		}

		fmt.Printf("✓ Reply received from %s (id: %s)\n", result.ReplyUsername, result.ReplyUserID)
		fmt.Println("  Full loop confirmed — inbound and outbound working.")

		// Write verification timestamp to DB.
		dbPath := cfg.Database.Path
		if dbPath == "" {
			home, _ := os.UserHomeDir()
			dbPath = filepath.Join(home, ".config", "chandra", "chandra.db")
		}
		if err := persistVerification(dbPath, channelID, result.ReplyUserID); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not persist verification: %v\n", err)
		}
	},
}

// persistVerification writes the loop test result to the channel_verifications table.
func persistVerification(dbPath, channelID, userID string) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(
		`INSERT OR REPLACE INTO channel_verifications (channel_id, verified_at, verified_user_id) VALUES (?, ?, ?)`,
		channelID, time.Now().Unix(), userID,
	)
	return err
}

// ---- doctor command ----------------------------------------------------------

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Verify the entire Chandra stack",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath := resolveDefaultConfigPath()
		cfgDir := filepath.Dir(cfgPath)

		// Load config (may fail — that's ok, config check handles it)
		cfg, cfgErr := config.Load(cfgPath)

		var dbPath string
		if cfgErr == nil && cfg.Database.Path != "" {
			dbPath = cfg.Database.Path
		} else {
			home, _ := os.UserHomeDir()
			dbPath = filepath.Join(home, ".config", "chandra", "chandra.db")
		}

		checks := []doctor.Check{
			doctor.NewConfigCheck(cfgPath),
			doctor.NewPermissionsCheck(cfgDir, cfgPath),
			doctor.NewDBCheck(dbPath),
		}

		if cfgErr == nil {
			checks = append(checks, doctor.NewProviderCheck(&cfg.Provider))
			checks = append(checks, doctor.NewAllowlistCheck(cfg, dbPath))
			if cfg.Channels.Discord != nil && cfg.Channels.Discord.BotToken != "" {
				// One ChannelVerifiedCheck per configured Discord channel ID.
				for _, chID := range cfg.Channels.Discord.ChannelIDs {
					checks = append(checks, doctor.NewChannelVerifiedCheck(chID, dbPath))
				}
			}
			checks = append(checks, doctor.NewSchedulerCheck(""))
			checks = append(checks, doctor.NewDaemonCheck(""))
		}

		fmt.Println("Chandra Doctor — Stack Verification")
		fmt.Println("────────────────────────────────────")
		fmt.Println()

		results := doctor.RunAll(cmd.Context(), checks, 10)

		for _, r := range results {
			switch r.Status {
			case doctor.Pass:
				fmt.Printf("  %-14s ✓ %s\n", r.Name, r.Detail)
			case doctor.Warn:
				fmt.Printf("  %-14s ⚠ %s\n", r.Name, r.Detail)
				if r.Fix != "" {
					fmt.Printf("  %-14s   %s\n", "", r.Fix)
				}
			case doctor.Fail:
				fmt.Printf("  %-14s ✗ %s\n", r.Name, r.Detail)
				if r.Fix != "" {
					fmt.Printf("  %-14s   %s\n", "", r.Fix)
				}
			}
		}

		fmt.Println()
		if doctor.AnyFailed(results) {
			fmt.Println("One or more checks failed. See above for remediation.")
			os.Exit(1)
		}
		if doctor.AnyWarned(results) {
			fmt.Println("Checks completed with warnings. Review the items above.")
			return
		}
		fmt.Println("All checks passed. Chandra is healthy.")
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

	// Provider subcommands.
	providerTestCmd.Flags().BoolVarP(&providerTestVerbose, "verbose", "v", false, "show request/response detail")
	providerCmd.AddCommand(providerTestCmd)
	rootCmd.AddCommand(providerCmd)

	// Channel subcommands.
	channelCmd.AddCommand(channelTestCmd)
	rootCmd.AddCommand(channelCmd)

	// Doctor command.
	rootCmd.AddCommand(doctorCmd)
}

// resolveDefaultConfigPath returns the default config file path, respecting
// CHANDRA_CONFIG env var if set.
func resolveDefaultConfigPath() string {
	if envPath := os.Getenv("CHANDRA_CONFIG"); envPath != "" {
		return envPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "chandra", "config.toml")
}
