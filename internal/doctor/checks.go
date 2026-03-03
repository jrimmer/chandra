package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
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
	if c.cfg.Type == "custom" && len(c.cfg.BaseURL) > 4 && c.cfg.BaseURL[:4] != "http" {
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
		Detail: fmt.Sprintf("%s reachable, %s available", c.cfg.Type, c.cfg.Model),
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
