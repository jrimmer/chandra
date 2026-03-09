package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

// ── systemd unit template ───────────────────────────────────────────────────

const systemdTemplate = `[Unit]
Description=Chandra — Autonomous LLM Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.BinaryPath}} --foreground
Restart=on-failure
RestartSec=5
{{- if .IsUser}}
# User service — no User/Group needed
{{- else}}
User={{.User}}
Group={{.Group}}
{{- end}}
Environment=HOME={{.Home}}
WorkingDirectory={{.Home}}

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=chandrad

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths={{.ConfigDir}} {{.DataDir}}

[Install]
WantedBy={{.WantedBy}}
`

// ── launchd plist template ──────────────────────────────────────────────────

const launchdTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>ai.chandra.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
        <string>--foreground</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>{{.LogDir}}/chandrad.log</string>
    <key>StandardErrorPath</key>
    <string>{{.LogDir}}/chandrad.error.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key>
        <string>{{.Home}}</string>
    </dict>
    <key>WorkingDirectory</key>
    <string>{{.Home}}</string>
</dict>
</plist>
`

// ── template data ───────────────────────────────────────────────────────────

type serviceConfig struct {
	BinaryPath string
	Home       string
	User       string
	Group      string
	ConfigDir  string
	DataDir    string
	LogDir     string
	IsUser     bool   // systemd user service vs system service
	WantedBy   string // multi-user.target or default.target
}

func newServiceConfig() (*serviceConfig, error) {
	binPath, err := exec.LookPath("chandrad")
	if err != nil {
		// Fall back to /usr/local/bin
		binPath = "/usr/local/bin/chandrad"
	}
	// Resolve to absolute path
	binPath, _ = filepath.Abs(binPath)

	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("cannot determine current user: %w", err)
	}

	home := u.HomeDir
	configDir := filepath.Join(home, ".config", "chandra")
	dataDir := configDir // DB lives in config dir by default

	sc := &serviceConfig{
		BinaryPath: binPath,
		Home:       home,
		User:       u.Username,
		Group:      u.Username,
		ConfigDir:  configDir,
		DataDir:    dataDir,
		LogDir:     filepath.Join(home, ".config", "chandra", "logs"),
		IsUser:     u.Uid != "0",
		WantedBy:   "multi-user.target",
	}

	if sc.IsUser {
		sc.WantedBy = "default.target"
	}

	return sc, nil
}

// ── commands ────────────────────────────────────────────────────────────────

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage the chandrad system service",
	Long:  "Install, uninstall, start, stop, restart, and check status of the chandrad service.",
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install chandrad as a system service",
	RunE: func(cmd *cobra.Command, args []string) error {
		sc, err := newServiceConfig()
		if err != nil {
			return err
		}

		switch runtime.GOOS {
		case "linux":
			return installSystemd(sc)
		case "darwin":
			return installLaunchd(sc)
		default:
			return fmt.Errorf("unsupported OS: %s (only Linux and macOS are supported)", runtime.GOOS)
		}
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the chandrad system service",
	RunE: func(cmd *cobra.Command, args []string) error {
		switch runtime.GOOS {
		case "linux":
			return uninstallSystemd()
		case "darwin":
			return uninstallLaunchd()
		default:
			return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
		}
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the chandrad service",
	RunE: func(cmd *cobra.Command, args []string) error {
		return serviceAction("start")
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the chandrad service",
	RunE: func(cmd *cobra.Command, args []string) error {
		return serviceAction("stop")
	},
}

var serviceRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the chandrad service",
	RunE: func(cmd *cobra.Command, args []string) error {
		return serviceAction("restart")
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show chandrad service status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return serviceAction("status")
	},
}

// ── Linux (systemd) ─────────────────────────────────────────────────────────

func systemdUnitPath(isUser bool) string {
	if isUser {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "systemd", "user", "chandrad.service")
	}
	return "/etc/systemd/system/chandrad.service"
}

func installSystemd(sc *serviceConfig) error {
	unitPath := systemdUnitPath(sc.IsUser)

	// Check if already installed.
	if _, err := os.Stat(unitPath); err == nil {
		fmt.Printf("⚠️  Service file already exists at %s\n", unitPath)
		fmt.Println("   Run 'chandra service uninstall' first to reinstall.")
		return nil
	}

	fmt.Println("▸ Installing chandrad as a systemd service")

	// Ensure parent directory exists (for user services).
	if err := os.MkdirAll(filepath.Dir(unitPath), 0755); err != nil {
		return fmt.Errorf("create unit dir: %w", err)
	}

	// Render template.
	tmpl, err := template.New("systemd").Parse(systemdTemplate)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	f, err := os.Create(unitPath)
	if err != nil {
		return fmt.Errorf("create unit file: %w", err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, sc); err != nil {
		return fmt.Errorf("render unit file: %w", err)
	}

	fmt.Printf("  ✓ Unit file written to %s\n", unitPath)

	// Reload and enable.
	if sc.IsUser {
		run("systemctl", "--user", "daemon-reload")
		run("systemctl", "--user", "enable", "chandrad.service")
		fmt.Println("  ✓ Service enabled (user mode — starts on login)")
		fmt.Println()
		fmt.Println("  Start now with:  chandra service start")
		fmt.Println("  View logs with:  journalctl --user -u chandrad -f")
	} else {
		run("systemctl", "daemon-reload")
		run("systemctl", "enable", "chandrad.service")
		fmt.Println("  ✓ Service enabled (system mode — starts on boot)")
		fmt.Println()
		fmt.Println("  Start now with:  chandra service start")
		fmt.Println("  View logs with:  journalctl -u chandrad -f")
	}

	return nil
}

func uninstallSystemd() error {
	// Try user service first, then system.
	u, _ := user.Current()
	isUser := u != nil && u.Uid != "0"

	unitPath := systemdUnitPath(isUser)
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		// Try the other mode.
		unitPath = systemdUnitPath(!isUser)
		isUser = !isUser
		if _, err := os.Stat(unitPath); os.IsNotExist(err) {
			fmt.Println("No chandrad service found to uninstall.")
			return nil
		}
	}

	fmt.Println("▸ Uninstalling chandrad service")

	if isUser {
		run("systemctl", "--user", "stop", "chandrad.service")
		run("systemctl", "--user", "disable", "chandrad.service")
	} else {
		run("systemctl", "stop", "chandrad.service")
		run("systemctl", "disable", "chandrad.service")
	}

	if err := os.Remove(unitPath); err != nil {
		return fmt.Errorf("remove unit file: %w", err)
	}

	if isUser {
		run("systemctl", "--user", "daemon-reload")
	} else {
		run("systemctl", "daemon-reload")
	}

	fmt.Printf("  ✓ Service removed (%s)\n", unitPath)
	return nil
}

// ── macOS (launchd) ─────────────────────────────────────────────────────────

func launchdPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "ai.chandra.daemon.plist")
}

func installLaunchd(sc *serviceConfig) error {
	plistPath := launchdPlistPath()

	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("⚠️  Plist already exists at %s\n", plistPath)
		fmt.Println("   Run 'chandra service uninstall' first to reinstall.")
		return nil
	}

	fmt.Println("▸ Installing chandrad as a launchd service")

	// Ensure log directory exists.
	if err := os.MkdirAll(sc.LogDir, 0755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return fmt.Errorf("create plist dir: %w", err)
	}

	tmpl, err := template.New("launchd").Parse(launchdTemplate)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	f, err := os.Create(plistPath)
	if err != nil {
		return fmt.Errorf("create plist: %w", err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, sc); err != nil {
		return fmt.Errorf("render plist: %w", err)
	}

	fmt.Printf("  ✓ Plist written to %s\n", plistPath)

	run("launchctl", "load", plistPath)
	fmt.Println("  ✓ Service loaded (starts on login)")
	fmt.Println()
	fmt.Println("  Check status:  chandra service status")
	fmt.Printf("  View logs:     tail -f %s/chandrad.log\n", sc.LogDir)

	return nil
}

func uninstallLaunchd() error {
	plistPath := launchdPlistPath()
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		fmt.Println("No chandrad service found to uninstall.")
		return nil
	}

	fmt.Println("▸ Uninstalling chandrad service")

	run("launchctl", "unload", plistPath)

	if err := os.Remove(plistPath); err != nil {
		return fmt.Errorf("remove plist: %w", err)
	}

	fmt.Printf("  ✓ Service removed (%s)\n", plistPath)
	return nil
}

// ── cross-platform service action ───────────────────────────────────────────

func serviceAction(action string) error {
	switch runtime.GOOS {
	case "linux":
		return serviceActionLinux(action)
	case "darwin":
		return serviceActionDarwin(action)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func serviceActionLinux(action string) error {
	u, _ := user.Current()
	isUser := u != nil && u.Uid != "0"

	var args []string
	if isUser {
		args = []string{"--user"}
	}

	if action == "status" {
		args = append(args, "status", "chandrad.service")
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	args = append(args, action, "chandrad.service")
	return run("systemctl", args...)
}

func serviceActionDarwin(action string) error {
	plistPath := launchdPlistPath()
	label := "ai.chandra.daemon"

	switch action {
	case "start":
		return run("launchctl", "load", plistPath)
	case "stop":
		return run("launchctl", "unload", plistPath)
	case "restart":
		_ = run("launchctl", "unload", plistPath)
		return run("launchctl", "load", plistPath)
	case "status":
		cmd := exec.Command("launchctl", "list", label)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			fmt.Println("Service is not running.")
		}
		return nil
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func run(name string, args ...string) error {
	// Flatten: if args[0] is a slice-like thing from append, handle it.
	var flatArgs []string
	for _, a := range args {
		flatArgs = append(flatArgs, a)
	}
	display := name + " " + strings.Join(flatArgs, " ")
	cmd := exec.Command(name, flatArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("  ⚠ %s: %v\n", display, err)
		return err
	}
	return nil
}

func init() {
	serviceCmd.AddCommand(serviceInstallCmd)
	serviceCmd.AddCommand(serviceUninstallCmd)
	serviceCmd.AddCommand(serviceStartCmd)
	serviceCmd.AddCommand(serviceStopCmd)
	serviceCmd.AddCommand(serviceRestartCmd)
	serviceCmd.AddCommand(serviceStatusCmd)
	rootCmd.AddCommand(serviceCmd)
}
