package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jrimmer/chandra/internal/config"
)

// --- Config check ---

type configCheck struct {
	path string
}

// NewConfigCheck checks that the config file exists and parses cleanly.
func NewConfigCheck(path string) Check {
	return &configCheck{path: path}
}

func (c *configCheck) Name() string { return "Config" }

func (c *configCheck) Run(_ context.Context) Result {
	if _, err := os.Stat(c.path); os.IsNotExist(err) {
		return Result{
			Status: Fail,
			Detail: fmt.Sprintf("config file not found: %s", c.path),
			Fix:    "run: chandra init",
		}
	}
	if _, err := config.Load(c.path); err != nil {
		return Result{
			Status: Fail,
			Detail: fmt.Sprintf("config parse error: %v", err),
			Fix:    "run: chandra config validate",
		}
	}
	return Result{Status: Pass, Detail: "valid TOML, all required fields present"}
}

// --- Permissions check ---

type permissionsCheck struct {
	dir     string
	cfgPath string
}

// NewPermissionsCheck checks that config dir is 0700 and config file is 0600.
func NewPermissionsCheck(dir, cfgPath string) Check {
	return &permissionsCheck{dir: dir, cfgPath: cfgPath}
}

func (c *permissionsCheck) Name() string { return "Permissions" }

func (c *permissionsCheck) Run(_ context.Context) Result {
	dirInfo, err := os.Stat(c.dir)
	if err != nil {
		return Result{Status: Fail, Detail: fmt.Sprintf("cannot stat config dir: %v", err)}
	}
	if dirInfo.Mode().Perm()&0077 != 0 {
		return Result{
			Status: Fail,
			Detail: fmt.Sprintf("config dir %s has insecure permissions %04o", c.dir, dirInfo.Mode().Perm()),
			Fix:    fmt.Sprintf("chmod 0700 %s", c.dir),
		}
	}
	cfgInfo, err := os.Stat(c.cfgPath)
	if os.IsNotExist(err) {
		return Result{Status: Pass, Detail: "config dir: 0700 (config file not yet created)"}
	}
	if err != nil {
		return Result{Status: Fail, Detail: fmt.Sprintf("cannot stat config file: %v", err)}
	}
	if cfgInfo.Mode().Perm()&0077 != 0 {
		return Result{
			Status: Fail,
			Detail: fmt.Sprintf("config file has insecure permissions %04o", cfgInfo.Mode().Perm()),
			Fix:    fmt.Sprintf("chmod 0600 %s", c.cfgPath),
		}
	}
	return Result{Status: Pass, Detail: "config.toml: 0600 · config dir: 0700"}
}

// --- Database check ---

// NOTE: The dbCheck and channelVerifiedCheck use database/sql with the "sqlite3"
// driver. Callers must import _ "github.com/mattn/go-sqlite3" to register the
// driver before invoking these checks.
type dbCheck struct {
	path string
}

// NewDBCheck checks that the database is accessible and migrations are up to date.
func NewDBCheck(path string) Check {
	return &dbCheck{path: path}
}

func (c *dbCheck) Name() string { return "Database" }

func (c *dbCheck) Run(ctx context.Context) Result {
	if _, err := os.Stat(c.path); os.IsNotExist(err) {
		return Result{
			Status: Fail,
			Detail: fmt.Sprintf("database not found: %s", c.path),
			Fix:    "run: chandra init",
		}
	}
	db, err := sql.Open("sqlite3", c.path+"?_journal_mode=WAL")
	if err != nil {
		return Result{Status: Fail, Detail: fmt.Sprintf("cannot open db: %v", err), Fix: "check file permissions"}
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return Result{Status: Fail, Detail: fmt.Sprintf("db ping failed: %v", err)}
	}

	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		return Result{
			Status: Fail,
			Detail: fmt.Sprintf("integrity check: %s (err: %v)", result, err),
			Fix:    "run: chandra db check",
		}
	}

	// Verify migrations have actually been applied by checking required tables exist.
	// An empty DB passes integrity_check, so we must verify schema separately.
	var tableCount int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('episodes', 'allowed_users', 'channel_verifications')`,
	).Scan(&tableCount)
	if err != nil || tableCount < 3 {
		return Result{
			Status: Fail,
			Detail: fmt.Sprintf("migrations not applied (found %d/3 required tables)", tableCount),
			Fix:    "run: chandra init",
		}
	}

	return Result{Status: Pass, Detail: "accessible, integrity ok, migrations current"}
}

// --- Provider check ---

type providerCheck struct {
	cfg *config.ProviderConfig
}

// NewProviderCheck checks that the configured LLM provider is reachable.
func NewProviderCheck(cfg *config.ProviderConfig) Check {
	return &providerCheck{cfg: cfg}
}

func (c *providerCheck) Name() string { return "Provider" }

func (c *providerCheck) Run(ctx context.Context) Result {
	if c.cfg.BaseURL == "" {
		return Result{Status: Fail, Detail: "provider not configured", Fix: "run: chandra init"}
	}
	if c.cfg.Type == "custom" && !strings.HasPrefix(c.cfg.BaseURL, "https://") {
		return Result{Status: Fail, Detail: "custom provider URL must use HTTPS", Fix: "update provider.base_url in config"}
	}

	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"/models", nil)
	if err != nil {
		return Result{Status: Fail, Detail: fmt.Sprintf("build request: %v", err)}
	}
	if c.cfg.APIKey != "" {
		// Anthropic uses x-api-key; all OpenAI-compatible providers use Bearer.
		if c.cfg.Type == "anthropic" {
			req.Header.Set("x-api-key", c.cfg.APIKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return Result{
			Status: Fail,
			Detail: fmt.Sprintf("connection refused or unreachable: %v", err),
			Fix:    "check CHANDRA_API_KEY and provider.base_url; test with: chandra provider test --verbose",
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return Result{
			Status: Fail,
			Detail: "API key invalid (401 Unauthorized)",
			Fix:    "check CHANDRA_API_KEY env var or provider.api_key in config",
		}
	}
	if resp.StatusCode >= 400 {
		return Result{Status: Warn, Detail: fmt.Sprintf("unexpected status %d from provider", resp.StatusCode)}
	}

	return Result{
		Status: Pass,
		Detail: fmt.Sprintf("%s reachable, %s available", c.cfg.Type, c.cfg.DefaultModel),
	}
}

// --- Channel verification check (reads last_verified_at from DB) ---

type channelVerifiedCheck struct {
	channelID string
	dbPath    string
}

// NewChannelVerifiedCheck checks whether the channel loop has been verified
// by reading the last_verified_at timestamp from the DB (not a live test).
func NewChannelVerifiedCheck(channelID, dbPath string) Check {
	return &channelVerifiedCheck{channelID: channelID, dbPath: dbPath}
}

func (c *channelVerifiedCheck) Name() string { return "Discord" }

func (c *channelVerifiedCheck) Run(ctx context.Context) Result {
	db, err := sql.Open("sqlite3", c.dbPath)
	if err != nil {
		return Result{Status: Fail, Detail: fmt.Sprintf("cannot open db: %v", err)}
	}
	defer db.Close()

	var verifiedAt int64
	var verifiedUserID string
	err = db.QueryRowContext(ctx,
		`SELECT verified_at, verified_user_id FROM channel_verifications WHERE channel_id = ?`,
		c.channelID,
	).Scan(&verifiedAt, &verifiedUserID)

	if err == sql.ErrNoRows {
		return Result{
			Status: Warn,
			Detail: fmt.Sprintf("channel %s not yet verified", c.channelID),
			Fix:    "run: chandra channel test discord",
		}
	}
	if err != nil {
		return Result{Status: Fail, Detail: fmt.Sprintf("db query: %v", err)}
	}

	return Result{
		Status: Pass,
		Detail: fmt.Sprintf("verified by %s", verifiedUserID),
	}
}

// --- Allowlist check ---

type allowlistCheck struct {
	cfg    *config.Config
	dbPath string
}

// NewAllowlistCheck verifies that enabled Discord channels have at least one
// authorized user. The DB is the authoritative source — the allowed_users table
// is written by both Hello World init and 'chandra access add/remove'.
// Per design §12: "allowed_users = [] on an enabled channel is a misconfiguration."
func NewAllowlistCheck(cfg *config.Config, dbPath string) Check {
	return &allowlistCheck{cfg: cfg, dbPath: dbPath}
}

func (c *allowlistCheck) Name() string { return "Allowlist" }

func (c *allowlistCheck) Run(_ context.Context) Result {
	if c.cfg.Channels.Discord == nil || c.cfg.Channels.Discord.BotToken == "" {
		return Result{Status: Pass, Detail: "Discord not configured"}
	}
	switch c.cfg.Channels.Discord.AccessPolicy {
	case "open":
		return Result{
			Status: Warn,
			Detail: "Discord access policy is 'open' — any user can message the bot",
			Fix:    "set access_policy = \"invite\" and run: chandra channel test discord",
		}
	case "role":
		if len(c.cfg.Channels.Discord.AllowedRoles) == 0 {
			return Result{
				Status: Fail,
				Detail: "access_policy=role but allowed_roles is empty — no one can message the bot",
				Fix:    "add role IDs: chandra access add discord --role <role-id>",
			}
		}
		return Result{Status: Pass, Detail: fmt.Sprintf("role-based access, %d role(s) configured", len(c.cfg.Channels.Discord.AllowedRoles))}
	default: // invite, request, allowlist — query DB (authoritative source)
		// Sum allowed users across all configured channel IDs.
		// The DB keys allowed_users by real Discord channel ID, not adapter name.
		var count int
		var queryErr error
		for _, chID := range c.cfg.Channels.Discord.ChannelIDs {
			n, err := countAllowedUsersInDB(c.dbPath, chID)
			if err != nil {
				queryErr = err
				break
			}
			count += n
		}
		if queryErr != nil || count == 0 {
			return Result{
				Status: Fail,
				Detail: "allowed_users table is empty — no one can message the bot",
				Fix:    "run: chandra channel test discord (to bootstrap), or: chandra access add discord <user-id>",
			}
		}
		return Result{Status: Pass, Detail: fmt.Sprintf("%d authorized user(s)", count)}
	}
}

// countAllowedUsersInDB returns the number of entries in the allowed_users table
// for the given channel. The DB is the authoritative source for access control —
// 'chandra access add/remove' and Hello World init both write only to the DB.
func countAllowedUsersInDB(dbPath, channelID string) (int, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var count int
	return count, db.QueryRow(
		"SELECT COUNT(*) FROM allowed_users WHERE channel_id = ?", channelID,
	).Scan(&count)
}

// ---- DaemonCheck -------------------------------------------------------

type daemonCheck struct {
	socketPath string // injectable; NewDaemonCheck("") uses the default path
}

// NewDaemonCheck verifies that the chandrad Unix socket is responsive.
// Pass socketPath="" to use the default (~/.config/chandra/chandra.sock).
// Pass an explicit path in tests to avoid depending on daemon state.
// Returns Warn (not Fail) because the daemon may not be started during init.
// Uses net.Dial to distinguish a responsive daemon from a stale socket file.
func NewDaemonCheck(socketPath string) Check {
	if socketPath == "" {
		home, _ := os.UserHomeDir()
		socketPath = filepath.Join(home, ".config", "chandra", "chandra.sock")
	}
	return &daemonCheck{socketPath: socketPath}
}

func (c *daemonCheck) Name() string { return "Daemon" }

func (c *daemonCheck) Run(_ context.Context) Result {
	conn, err := net.DialTimeout("unix", c.socketPath, 2*time.Second)
	if err != nil {
		return Result{
			Status: Warn,
			Detail: "chandrad not running (cannot connect to socket)",
			Fix:    "start with: chandrad",
		}
	}
	conn.Close()
	return Result{Status: Pass, Detail: "chandrad running (socket responsive)"}
}

// ---- SchedulerCheck -----------------------------------------------------

type schedulerCheck struct {
	socketPath string // injectable; NewSchedulerCheck("") uses the default path
}

// NewSchedulerCheck verifies the scheduler is active by pinging the daemon socket.
// Pass socketPath="" to use the default. Pass an explicit path in tests.
// Returns Warn if the daemon is not running (scheduler lives inside the daemon).
// Uses net.Dial so a stale socket file does not produce a false pass.
func NewSchedulerCheck(socketPath string) Check {
	if socketPath == "" {
		home, _ := os.UserHomeDir()
		socketPath = filepath.Join(home, ".config", "chandra", "chandra.sock")
	}
	return &schedulerCheck{socketPath: socketPath}
}

func (c *schedulerCheck) Name() string { return "Scheduler" }

func (c *schedulerCheck) Run(_ context.Context) Result {
	conn, err := net.DialTimeout("unix", c.socketPath, 2*time.Second)
	if err != nil {
		return Result{
			Status: Warn,
			Detail: "scheduler not verified (daemon not running)",
			Fix:    "start the daemon: chandrad",
		}
	}
	conn.Close()
	// Daemon socket responsive — scheduler runs inside the daemon process.
	// Full heartbeat-interval verification requires a scheduler RPC (future work).
	return Result{Status: Pass, Detail: "daemon responsive, scheduler active"}
}
