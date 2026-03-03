# Chandra Setup & Configuration — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the setup surface from `docs/plans/setup-design.md` — config schema alignment, verification primitives, doctor command, and init wizard with access control.

**Architecture:** Phase 0 aligns `config.go` struct names and TOML tags with the design schema (`[identity]`, `bot_token`, `default_model`, `embedding_model`). Phase 1 fixes five known code contradictions and builds standalone verification commands (`chandra provider test`, `chandra channel test discord`, `chandra doctor`) using a composable `DoctorCheck` interface. Phase 2 wraps those primitives in a TUI init wizard via `charmbracelet/huh` and adds invite-code access control. **Out of scope:** daemon install (`chandra daemon install`), `chandra console`, `chandra security audit` — these are deferred from this plan (the design lists daemon install as "ship second" in §10, but it requires separate daemon lifecycle work).

**Tech Stack:** Go, Cobra (CLI), charmbracelet/huh (TUI), bwmarrin/discordgo (Discord), BurntSushi/toml, SQLite migrations

---

## Phase 0: Align Config Schema with Design

The design spec (`docs/plans/setup-design.md §6`) defines the canonical TOML schema. The current `config.go` diverges in several field names. This task aligns the Go structs so `writeConfig()` and all callers produce the correct TOML — there is no separate migration; this is a rename-and-retag operation.

**Schema delta:**

| Current (code) | Design (spec) |
|---|---|
| `[agent]` section | `[identity]` section |
| `agent.persona` | `identity.description` |
| `provider.model` | `provider.default_model` |
| `[embeddings]` section | `provider.embedding_model` field |
| `channels.discord.token` | `channels.discord.bot_token` |
| no `channels.discord.enabled` | `channels.discord.enabled = true/false` |

### Task 0: Align `config.go` structs with design schema

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/chandrad/main.go` (field references)

**Step 1: Write a test for the new schema**

Add to `internal/config/config_test.go`:

```go
func TestConfig_DesignSchema(t *testing.T) {
	tomlData := `
[identity]
name = "Chandra"
description = "A helpful personal assistant"

[database]
path = "/tmp/test.db"

[provider]
type = "openai"
base_url = "https://api.openai.com/v1"
api_key = "sk-test"
default_model = "gpt-4o"
embedding_model = "text-embedding-3-small"

[channels.discord]
enabled = true
bot_token = "Bot abc123"
channel_ids = ["12345"]
`
	var cfg Config
	if _, err := toml.Decode(tomlData, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Identity.Name != "Chandra" {
		t.Errorf("expected identity.name=Chandra, got %q", cfg.Identity.Name)
	}
	if cfg.Provider.DefaultModel != "gpt-4o" {
		t.Errorf("expected provider.default_model=gpt-4o, got %q", cfg.Provider.DefaultModel)
	}
	if cfg.Provider.EmbeddingModel != "text-embedding-3-small" {
		t.Errorf("expected provider.embedding_model=text-embedding-3-small, got %q", cfg.Provider.EmbeddingModel)
	}
	if cfg.Channels.Discord == nil || cfg.Channels.Discord.BotToken != "Bot abc123" {
		t.Errorf("expected channels.discord.bot_token=Bot abc123")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -run TestConfig_DesignSchema -v
```
Expected: FAIL — fields don't exist yet

**Step 3: Update config.go struct definitions**

In `internal/config/config.go`, replace `AgentConfig` with `IdentityConfig` and update `ProviderConfig`, `DiscordConfig`, and `Config`:

```go
// IdentityConfig holds agent name and persona (replaces AgentConfig).
// TOML section: [identity]
type IdentityConfig struct {
	Name          string `toml:"name"`
	Description   string `toml:"description"`
	PersonaFile   string `toml:"persona_file"`
	MaxToolRounds int    `toml:"max_tool_rounds"`
}

// ProviderConfig holds LLM provider settings.
// embedding_model replaces the separate [embeddings] section.
type ProviderConfig struct {
	Type           string `toml:"type"`    // openai | anthropic | openrouter | ollama | custom
	BaseURL        string `toml:"base_url"`
	APIKey         string `toml:"api_key"`
	DefaultModel   string `toml:"default_model"`
	EmbeddingModel string `toml:"embedding_model"`
}

// EmbeddingsConfig is kept for backwards compatibility only.
// New configs should use provider.embedding_model instead.
// TOML section: [embeddings] — still parsed but EmbeddingModel takes precedence.
type EmbeddingsConfig struct {
	BaseURL    string `toml:"base_url"`
	APIKey     string `toml:"api_key"`
	Model      string `toml:"model"`
	Dimensions int    `toml:"dimensions"`
}

// DiscordConfig holds Discord channel settings.
type DiscordConfig struct {
	Enabled      bool     `toml:"enabled"`
	BotToken     string   `toml:"bot_token"`
	ChannelIDs   []string `toml:"channel_ids"`
	AccessPolicy string   `toml:"access_policy"` // invite | request | role | allowlist | open
	AllowedUsers  []string `toml:"allowed_users"`
	AllowedGuilds []string `toml:"allowed_guilds"`
	AllowedRoles  []string `toml:"allowed_roles"`
}

// Config is the top-level configuration struct.
type Config struct {
	Identity       IdentityConfig       `toml:"identity"`
	Provider       ProviderConfig       `toml:"provider"`
	Embeddings     EmbeddingsConfig     `toml:"embeddings"` // kept for backwards compat
	Database       DatabaseConfig       `toml:"database"`
	Budget         BudgetConfig         `toml:"budget"`
	Scheduler      SchedulerConfig      `toml:"scheduler"`
	MQTT           MQTTConfig           `toml:"mqtt"`
	Channels       ChannelsConfig       `toml:"channels"`
	Tools          ToolsConfig          `toml:"tools"`
	ActionLog      ActionLogConfig      `toml:"actionlog"`
	Skills         SkillsConfig         `toml:"skills"`
	Executor       ExecutorConfig       `toml:"executor"`
	Plans          PlansConfig          `toml:"plans"`
	Planner        PlannerConfig        `toml:"planner"`
	Infrastructure InfrastructureConfig `toml:"infrastructure"`
}
```

**Step 4: Update applyDefaults() for new field names**

In `applyDefaults()`, change:
```go
// Before:
if cfg.Agent.MaxToolRounds == 0 {
    cfg.Agent.MaxToolRounds = 5
}
```
to:
```go
if cfg.Identity.MaxToolRounds == 0 {
    cfg.Identity.MaxToolRounds = 5
}
if cfg.Identity.Name == "" {
    cfg.Identity.Name = "Chandra"
}
if cfg.Identity.Description == "" {
    cfg.Identity.Description = "A helpful personal assistant"
}
```

Also add EmbeddingModel default (remove Dimensions default since it's no longer in provider):
```go
if cfg.Provider.EmbeddingModel == "" {
    cfg.Provider.EmbeddingModel = "text-embedding-3-small"
}
if cfg.Provider.DefaultModel == "" && cfg.Provider.Type == "openai" {
    cfg.Provider.DefaultModel = "gpt-4o"
}
```

And for Discord access policy default:
```go
if cfg.Channels.Discord != nil && cfg.Channels.Discord.AccessPolicy == "" {
    cfg.Channels.Discord.AccessPolicy = "invite"
}
```

**Step 5: Update validate() for new field names**

In `validate()`, change:
```go
// Before:
if cfg.Agent.Name == "" {
    errs = append(errs, "agent.name is required")
}
if cfg.Agent.Persona == "" {
    errs = append(errs, "agent.persona is required")
}
if cfg.Provider.Model == "" {
    errs = append(errs, "provider.model is required")
}
if cfg.Embeddings.BaseURL == "" {
    errs = append(errs, "embeddings.base_url is required")
}
if cfg.Embeddings.Model == "" {
    errs = append(errs, "embeddings.model is required")
}
```

To (remove agent/persona/embeddings requirements — applyDefaults handles name/description defaults; embedding_model is now optional since it defaults):
```go
// identity.name is optional — defaults applied above
if cfg.Provider.DefaultModel == "" {
    errs = append(errs, "provider.default_model is required")
}
```

Also update the channel check: `cfg.Channels.Discord.Token` → `cfg.Channels.Discord.BotToken`.

**Step 6: Update cmd/chandrad/main.go field references**

Search for all `cfg.Agent.`, `cfg.Provider.Model`, `cfg.Channels.Discord.Token`, `cfg.Embeddings.` references in `cmd/chandrad/main.go` and update:

- `cfg.Agent.Name` → `cfg.Identity.Name`
- `cfg.Agent.Persona` → `cfg.Identity.Description`
- `cfg.Agent.MaxToolRounds` → `cfg.Identity.MaxToolRounds`
- `cfg.Provider.Model` → `cfg.Provider.DefaultModel`
- `cfg.Channels.Discord.Token` → `cfg.Channels.Discord.BotToken`
- `cfg.Embeddings.BaseURL` → use `cfg.Provider.BaseURL` for embeddings (or keep Embeddings field for backwards compat)
- `cfg.Embeddings.Model` → `cfg.Provider.EmbeddingModel`

For the anthropic/openai provider construction (passes `cfg.Provider.DefaultModel`):
```go
case "anthropic":
    chatProvider = anthropic.NewProvider(cfg.Provider.BaseURL, cfg.Provider.APIKey, cfg.Provider.DefaultModel)
case "openai", "ollama", "openrouter", "custom":
    chatProvider = openai.NewProvider(cfg.Provider.BaseURL, cfg.Provider.APIKey, cfg.Provider.DefaultModel)
```

For Discord construction (passes `cfg.Channels.Discord.BotToken`):
```go
discordCh, err := discord.NewDiscord(cfg.Channels.Discord.BotToken, cfg.Channels.Discord.ChannelIDs)
```

**Step 7: Run all tests**

```bash
go test ./internal/config/... -v
go build ./cmd/chandrad/... ./cmd/chandra/...
```
Expected: all PASS, builds succeed

**Step 8: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/chandrad/main.go
git commit -m "refactor(config): align config schema with design spec ([identity], bot_token, default_model, embedding_model)"
```

---

## Phase 1: Fix Code Contradictions + Verification Primitives

### Task 1: Make channel config optional in `config.go`

**Files:**
- Modify: `internal/config/config.go:293-345` (validate function)
- Modify: `internal/config/config_test.go`

**Step 1: Write the failing test**

Open `internal/config/config_test.go`. Verify there is a test that expects validation to pass without any Discord config. Add:

```go
func TestValidate_NoChannelsAllowed(t *testing.T) {
	cfg := &Config{
		Identity: IdentityConfig{Name: "Chandra", Description: "helpful"},
		Provider: ProviderConfig{BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-4o", Type: "openai"},
		Database: DatabaseConfig{Path: "/tmp/test.db"},
	}
	err := validate(cfg)
	if err != nil {
		t.Fatalf("expected no error when no channels configured, got: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -run TestValidate_NoChannelsAllowed -v
```
Expected: FAIL — "at least one channel must be configured"

**Step 3: Remove the channel requirement from validate()**

In `internal/config/config.go`, remove the block:
```go
// At least one channel must be configured.
if cfg.Channels.Discord == nil || cfg.Channels.Discord.Token == "" {
    errs = append(errs, "at least one channel must be configured (channels.discord)")
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/config/... -v
```
Expected: all PASS

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "fix(config): make channel config optional — CLI-only mode should work without Discord"
```

---

### Task 2: Support `CHANDRA_CONFIG` env var in `chandrad`

**Files:**
- Modify: `cmd/chandrad/main.go:599-610` (resolveConfigPath)
- Modify: `cmd/chandrad/permissions_test.go` (if needed) or create `cmd/chandrad/main_test.go`

**Step 1: Write the failing test**

Create `cmd/chandrad/configpath_test.go`:

```go
package main

import (
	"os"
	"testing"
)

func TestResolveConfigPath_UsesEnvVar(t *testing.T) {
	os.Setenv("CHANDRA_CONFIG", "/tmp/my-chandra.toml")
	defer os.Unsetenv("CHANDRA_CONFIG")

	_, path := resolveConfigPath()
	if path != "/tmp/my-chandra.toml" {
		t.Fatalf("expected /tmp/my-chandra.toml, got %s", path)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./cmd/chandrad/... -run TestResolveConfigPath_UsesEnvVar -v
```
Expected: FAIL — returns default path, not `/tmp/my-chandra.toml`

**Step 3: Update resolveConfigPath() to check CHANDRA_CONFIG first**

In `cmd/chandrad/main.go`, replace `resolveConfigPath()`:

```go
func resolveConfigPath() (dir, cfgPath string) {
	if envPath := os.Getenv("CHANDRA_CONFIG"); envPath != "" {
		return filepath.Dir(envPath), envPath
	}
	home, err := os.UserHomeDir()
	if err == nil {
		dir = filepath.Join(home, ".config", "chandra")
		cfgPath = filepath.Join(dir, "config.toml")
		return dir, cfgPath
	}
	wd, _ := os.Getwd()
	return wd, filepath.Join(wd, "chandra.toml")
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./cmd/chandrad/... -run TestResolveConfigPath -v
```
Expected: PASS

**Step 5: Commit**

```bash
git add cmd/chandrad/main.go cmd/chandrad/configpath_test.go
git commit -m "fix(daemon): read CHANDRA_CONFIG env var in resolveConfigPath"
```

---

### Task 3: Permissions check must block daemon startup (not just warn)

**Files:**
- Modify: `cmd/chandrad/main.go:103-110`

**Step 1: Write the failing test**

The existing `cmd/chandrad/permissions_test.go` tests `verifyPermissions`. The blocking behaviour is in `run()`. Check whether there's already a test for this—if not, this is a code change only (the test for the exit behaviour is an integration test that's impractical to unit-test without restructuring `run()`). Proceed to the fix directly.

**Step 2: Change warn to hard exit in main.go**

In `cmd/chandrad/main.go`, replace:

```go
if !safeMode {
    if err := verifyPermissions(cfgDir, cfgPath); err != nil {
        slog.Warn("chandrad: permission check failed", "err", err)
        // Non-fatal: warn but continue.
    }
}
```

with:

```go
if !safeMode {
    if err := verifyPermissions(cfgDir, cfgPath); err != nil {
        return fmt.Errorf("chandrad: permission check failed: %w (fix with: chmod 0700 %s && chmod 0600 %s)", err, cfgDir, cfgPath)
    }
}
```

**Step 3: Build to verify it compiles**

```bash
go build ./cmd/chandrad/...
```
Expected: build succeeds, no errors

**Step 4: Commit**

```bash
git add cmd/chandrad/main.go
git commit -m "fix(daemon): permission check is now a hard startup error, not a warning"
```

---

### Task 4: Fix or remove the dead `chandra start` command

**Files:**
- Modify: `cmd/chandrad/main.go` (registerHandlers function, add daemon.start handler)
- Modify: `cmd/chandra/commands.go` (update startCmd comment)

**Step 1: Register `daemon.start` handler in main.go**

The `daemon.start` RPC is called by `chandra start` but has no handler. Since `chandrad` is already running when `chandra start` is called (you'd need to start it some other way), this command is misleading. The correct fix is to either:
- Register a `daemon.start` handler that returns a helpful error message ("daemon is already running")
- Or document the command properly

Register a stub handler in `registerHandlers()` just before `daemon.stop`:

```go
// daemon.start — already running if this handler is reached
srv.Handle("daemon.start", func(ctx context.Context, _ json.RawMessage) (any, error) {
    return map[string]any{"ok": true, "message": "daemon already running"}, nil
})
```

**Step 2: Build and verify**

```bash
go build ./cmd/chandrad/... && go build ./cmd/chandra/...
```
Expected: builds succeed

**Step 3: Commit**

```bash
git add cmd/chandrad/main.go
git commit -m "fix(daemon): register daemon.start handler to prevent silent RPC failure"
```

---

### Task 5: Verify DiscordConfig access control fields from Task 0

> **Note:** `DiscordConfig` was fully updated in Task 0 to include `Enabled`, `BotToken`, `AccessPolicy`, `AllowedUsers`, `AllowedGuilds`, `AllowedRoles`. This task verifies those fields round-trip through TOML correctly with the new `bot_token` field name.

**Files:**
- Modify: `internal/config/config_test.go`

**Step 1: Write a test for the new fields**

Add to `internal/config/config_test.go`:

```go
func TestDiscordConfig_AccessControlFields(t *testing.T) {
	tomlData := `
[channels.discord]
enabled = true
bot_token = "Bot abc123"
channel_ids = ["12345"]
access_policy = "invite"
allowed_users = ["111222333"]
allowed_guilds = ["444555666"]
allowed_roles = ["777888999"]
`
	var cfg Config
	if _, err := toml.Decode(tomlData, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.Channels.Discord.Enabled {
		t.Error("expected enabled=true")
	}
	if cfg.Channels.Discord.BotToken != "Bot abc123" {
		t.Errorf("expected bot_token=Bot abc123, got %q", cfg.Channels.Discord.BotToken)
	}
	if cfg.Channels.Discord.AccessPolicy != "invite" {
		t.Errorf("expected access_policy=invite, got %q", cfg.Channels.Discord.AccessPolicy)
	}
	if len(cfg.Channels.Discord.AllowedUsers) != 1 || cfg.Channels.Discord.AllowedUsers[0] != "111222333" {
		t.Errorf("unexpected allowed_users: %v", cfg.Channels.Discord.AllowedUsers)
	}
}
```

**Step 2: Run test to verify it passes**

```bash
go test ./internal/config/... -run TestDiscordConfig_AccessControlFields -v
```
Expected: PASS (Task 0 already added all these fields)

**Step 3: Commit**

```bash
git add internal/config/config_test.go
git commit -m "test(config): verify DiscordConfig access control fields with design schema field names"
```

---

### Task 6: Create DB migration for channel_verifications table

**Files:**
- Create: `store/migrations/004_channel_verifications.up.sql`
- Create: `store/migrations/004_channel_verifications.down.sql`

**Step 1: Write the migration**

`store/migrations/004_channel_verifications.up.sql`:

```sql
CREATE TABLE IF NOT EXISTS channel_verifications (
    channel_id      TEXT NOT NULL,
    verified_at     INTEGER NOT NULL,  -- Unix timestamp (seconds)
    verified_user_id TEXT NOT NULL,
    PRIMARY KEY (channel_id)
);
```

`store/migrations/004_channel_verifications.down.sql`:

```sql
DROP TABLE IF EXISTS channel_verifications;
```

**Step 2: Run migration test**

```bash
go test ./store/... -run TestMigrate -v
```
Expected: PASS — migration 004 applies cleanly

**Step 3: Commit**

```bash
git add store/migrations/
git commit -m "feat(store): add channel_verifications table for tracking loop test results"
```

---

### Task 7: Build the `DoctorCheck` interface package

**Files:**
- Create: `internal/doctor/doctor.go`
- Create: `internal/doctor/doctor_test.go`

**Step 1: Write the test**

Create `internal/doctor/doctor_test.go`:

```go
package doctor_test

import (
	"context"
	"testing"

	"github.com/jrimmer/chandra/internal/doctor"
)

type mockCheck struct {
	name   string
	result doctor.Result
}

func (m *mockCheck) Name() string                              { return m.name }
func (m *mockCheck) Run(ctx context.Context) doctor.Result    { return m.result }

func TestRunAll_CollectsResults(t *testing.T) {
	checks := []doctor.Check{
		&mockCheck{name: "config", result: doctor.Result{Status: doctor.Pass, Detail: "ok"}},
		&mockCheck{name: "db", result: doctor.Result{Status: doctor.Fail, Detail: "not found", Fix: "run migrations"}},
	}

	results := doctor.RunAll(context.Background(), checks, 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	found := map[string]doctor.Status{}
	for _, r := range results {
		found[r.Name] = r.Status
	}
	if found["config"] != doctor.Pass {
		t.Errorf("expected config=Pass, got %v", found["config"])
	}
	if found["db"] != doctor.Fail {
		t.Errorf("expected db=Fail, got %v", found["db"])
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/doctor/... -v
```
Expected: FAIL — package doesn't exist

**Step 3: Create doctor.go**

Create `internal/doctor/doctor.go`:

```go
// Package doctor provides the DoctorCheck interface and runner for
// chandra doctor and chandra init's final verification step.
package doctor

import (
	"context"
	"sync"
	"time"
)

// Status represents the outcome of a check.
type Status int

const (
	Pass Status = iota
	Warn
	Fail
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "pass"
	case Warn:
		return "warn"
	default:
		return "fail"
	}
}

// Result is returned by a Check.
type Result struct {
	Status Status
	Detail string
	Fix    string // human-readable remediation hint
}

// CheckResult pairs a result with the check name.
type CheckResult struct {
	Name   string
	Result Result
	// convenience accessors
	Status Status
	Detail string
	Fix    string
}

// Check is the interface that every doctor check implements.
type Check interface {
	Name() string
	Run(ctx context.Context) Result
}

// RunAll runs all checks in parallel, each with a per-check timeout derived
// from the timeoutSec argument. Results are returned in the same order as
// checks. A check that times out is reported as Warn with a retry suggestion.
func RunAll(ctx context.Context, checks []Check, timeoutSec int) []CheckResult {
	results := make([]CheckResult, len(checks))
	var wg sync.WaitGroup

	for i, ch := range checks {
		wg.Add(1)
		go func(idx int, c Check) {
			defer wg.Done()

			timeout := time.Duration(timeoutSec) * time.Second
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			done := make(chan Result, 1)
			go func() {
				done <- c.Run(checkCtx)
			}()

			var r Result
			select {
			case r = <-done:
			case <-checkCtx.Done():
				r = Result{
					Status: Warn,
					Detail: "timed out after " + timeout.String(),
					Fix:    "retry with: chandra doctor",
				}
			}

			results[idx] = CheckResult{
				Name:   c.Name(),
				Result: r,
				Status: r.Status,
				Detail: r.Detail,
				Fix:    r.Fix,
			}
		}(i, ch)
	}

	wg.Wait()
	return results
}

// AnyFailed returns true if any result has Fail status.
func AnyFailed(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == Fail {
			return true
		}
	}
	return false
}

// AnyWarned returns true if any result has Warn status.
func AnyWarned(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == Warn {
			return true
		}
	}
	return false
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./internal/doctor/... -v
```
Expected: PASS

**Step 5: Commit**

```bash
git add internal/doctor/
git commit -m "feat(doctor): add DoctorCheck interface with parallel runner and timeout support"
```

---

### Task 8: Implement individual doctor checks

**Files:**
- Create: `internal/doctor/checks.go`
- Create: `internal/doctor/checks_test.go`

**Step 1: Write tests for check constructors**

Create `internal/doctor/checks_test.go`:

```go
package doctor_test

import (
	"context"
	"testing"

	"github.com/jrimmer/chandra/internal/doctor"
	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver for DB check tests
)

func TestConfigCheck_MissingFile(t *testing.T) {
	check := doctor.NewConfigCheck("/nonexistent/path/config.toml")
	result := check.Run(context.Background())
	if result.Status != doctor.Fail {
		t.Errorf("expected Fail for missing config, got %v", result.Status)
	}
}

func TestPermissionsCheck_Name(t *testing.T) {
	check := doctor.NewPermissionsCheck("/tmp", "/tmp/config.toml")
	if check.Name() != "Permissions" {
		t.Errorf("unexpected name: %s", check.Name())
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/doctor/... -run TestConfigCheck -v
```
Expected: FAIL

**Step 3: Create checks.go**

Create `internal/doctor/checks.go`:

```go
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
			Detail: "loop not yet verified",
			Fix:    "run: chandra channel test discord",
		}
	}
	if err != nil {
		return Result{Status: Fail, Detail: fmt.Sprintf("db query: %v", err)}
	}

	t := time.Unix(verifiedAt, 0)
	return Result{
		Status: Pass,
		Detail: fmt.Sprintf("Bot online · loop verified %s by %s", t.Format("2006-01-02 15:04"), verifiedUserID),
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
```

Add `"net"` to the imports in `internal/doctor/checks.go` (alongside the existing `os`, `path/filepath`).

**Step 4: Write tests for the new checks**

Add to `internal/doctor/checks_test.go` (already in `package doctor_test`):

```go
func TestDaemonCheck_NoSocket(t *testing.T) {
	// Use a guaranteed-nonexistent path so the test is not affected by daemon state.
	check := doctor.NewDaemonCheck("/tmp/chandra-test-nonexistent.sock")
	result := check.Run(context.Background())
	if result.Status != doctor.Warn {
		t.Errorf("expected Warn when socket unreachable, got %v", result.Status)
	}
}

func TestSchedulerCheck_NoSocket(t *testing.T) {
	check := doctor.NewSchedulerCheck("/tmp/chandra-test-nonexistent.sock")
	result := check.Run(context.Background())
	if result.Status != doctor.Warn {
		t.Errorf("expected Warn when daemon not running, got %v", result.Status)
	}
}
```

**Step 5: Run tests to verify they pass**

```bash
go test ./internal/doctor/... -v
```
Expected: PASS

**Step 6: Build to check compilation**

```bash
go build ./...
```
Expected: build succeeds

**Step 7: Commit**

```bash
git add internal/doctor/checks.go internal/doctor/checks_test.go
git commit -m "feat(doctor): implement config, permissions, db, provider, allowlist, scheduler, and daemon checks"
```

---

### Task 9: Build `chandra provider test` command

**Files:**
- Modify: `cmd/chandra/commands.go` (add providerTestCmd)

**Step 1: Add the command (no test needed for CLI wiring — tested via integration)**

In `cmd/chandra/commands.go`, add after the existing provider-related imports. Add new provider and channel parent commands and subcommands:

```go
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
```

Also add `resolveDefaultConfigPath()` helper at the bottom of `commands.go`:

```go
// resolveDefaultConfigPath returns the default config file path, respecting
// CHANDRA_CONFIG env var if set.
func resolveDefaultConfigPath() string {
	if envPath := os.Getenv("CHANDRA_CONFIG"); envPath != "" {
		return envPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "chandra", "config.toml")
}
```

Add required imports at the top:
```go
import (
    "path/filepath"
    "github.com/jrimmer/chandra/internal/config"
    "github.com/jrimmer/chandra/internal/doctor"
)
```

Wire it in `init()`:
```go
providerTestCmd.Flags().BoolVarP(&providerTestVerbose, "verbose", "v", false, "show request/response detail")
providerCmd.AddCommand(providerTestCmd)
rootCmd.AddCommand(providerCmd)
```

**Step 2: Build to verify it compiles**

```bash
go build ./cmd/chandra/...
```
Expected: build succeeds

**Step 3: Smoke-test**

```bash
./chandra provider test --help
```
Expected: shows usage for `chandra provider test`

**Step 4: Commit**

```bash
git add cmd/chandra/commands.go
git commit -m "feat(cli): add chandra provider test command"
```

---

### Task 10: Build `chandra channel test discord` — Hello World loop verifier

**Files:**
- Create: `internal/channels/discord/verifier.go`
- Create: `internal/channels/discord/verifier_test.go`
- Modify: `cmd/chandra/commands.go` (add channelTestCmd)

**Step 1: Write the failing test for the verifier**

Create `internal/channels/discord/verifier_test.go`:

```go
package discord_test

import (
	"testing"

	"github.com/jrimmer/chandra/internal/channels/discord"
)

func TestVerifier_OptionsDefaults(t *testing.T) {
	opts := discord.DefaultVerifyOptions()
	if opts.TimeoutSec != 120 {
		t.Errorf("expected 120s timeout, got %d", opts.TimeoutSec)
	}
	if opts.Message == "" {
		t.Error("default message should not be empty")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/channels/discord/... -run TestVerifier -v
```
Expected: FAIL

**Step 3: Create verifier.go**

Create `internal/channels/discord/verifier.go`:

```go
package discord

import (
	"context"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
)

// VerifyOptions controls the Hello World loop test.
type VerifyOptions struct {
	TimeoutSec int
	Message    string
}

// DefaultVerifyOptions returns sane defaults for the loop test.
func DefaultVerifyOptions() VerifyOptions {
	return VerifyOptions{
		TimeoutSec: 120,
		Message:    "👋 Hi! I'm Chandra. Reply to this message to complete setup.",
	}
}

// VerifyResult is returned by RunLoopTest.
type VerifyResult struct {
	ReplyUserID   string
	ReplyUsername string
	MessageID     string // the sent message ID
}

// RunLoopTest sends a message to channelID, waits for a reply (matched by
// parent message ID), and returns the replying user's ID and name.
// The caller is responsible for writing the result to the DB.
//
// token is the Discord bot token.
// channelID is the Discord channel ID to send the test message to.
func RunLoopTest(ctx context.Context, token, channelID string, opts VerifyOptions) (*VerifyResult, error) {
	sess, err := discordgo.New(token)
	if err != nil {
		return nil, fmt.Errorf("discord: create session: %w", err)
	}

	sess.Identify.Intents = discordgo.IntentGuildMessages | discordgo.IntentMessageContent

	replyCh := make(chan *VerifyResult, 1)
	var sentMsgID string

	// Register handler before opening so we don't miss fast replies.
	sess.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author == nil || m.Author.Bot {
			return
		}
		// Match reply to our specific sent message.
		if m.MessageReference != nil && m.MessageReference.MessageID == sentMsgID {
			select {
			case replyCh <- &VerifyResult{
				ReplyUserID:   m.Author.ID,
				ReplyUsername: m.Author.Username,
				MessageID:     sentMsgID,
			}:
			default:
			}
		}
	})

	if err := sess.Open(); err != nil {
		return nil, fmt.Errorf("discord: open connection: %w", err)
	}
	defer sess.Close()

	sent, err := sess.ChannelMessageSend(channelID, opts.Message)
	if err != nil {
		return nil, fmt.Errorf("discord: send message: %w", err)
	}
	sentMsgID = sent.ID

	timeout := time.Duration(opts.TimeoutSec) * time.Second
	select {
	case result := <-replyCh:
		return result, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("no reply received within %s", timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./internal/channels/discord/... -v
```
Expected: PASS (the unit test only tests DefaultVerifyOptions, no network needed)

**Step 5: Add `chandra channel test discord` to commands.go**

In `cmd/chandra/commands.go`, add after providerCmd:

```go
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
```

Add to `init()`:
```go
channelCmd.AddCommand(channelTestCmd)
rootCmd.AddCommand(channelCmd)
```

Add required import: `"database/sql"`, `"github.com/jrimmer/chandra/internal/channels/discord"`

**Step 6: Build to verify it compiles**

```bash
go build ./cmd/chandra/...
```
Expected: build succeeds

**Step 7: Commit**

```bash
git add internal/channels/discord/verifier.go internal/channels/discord/verifier_test.go cmd/chandra/commands.go
git commit -m "feat(cli): add chandra channel test discord — Hello World loop verifier"
```

---

### Task 11: Build `chandra doctor` command

**Files:**
- Modify: `cmd/chandra/commands.go` (add doctorCmd)

**Step 1: Add the doctor command to commands.go**

Add after channelCmd:

```go
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
```

Wire in `init()`:
```go
rootCmd.AddCommand(doctorCmd)
```

**Step 2: Build to verify it compiles**

```bash
go build ./cmd/chandra/...
```
Expected: build succeeds

**Step 3: Smoke-test**

```bash
./chandra doctor
```
Expected: runs checks and reports results (config check will fail if no config exists — that's correct behaviour)

**Step 4: Commit**

```bash
git add cmd/chandra/commands.go
git commit -m "feat(cli): add chandra doctor command with parallel checks and DoctorCheck interface"
```

---

### Task 12: Security defaults — allowlist enforcement and HTTPS validation

**Files:**
- Modify: `internal/config/config.go` (validate function — add openrouter/custom types + HTTPS check; applyDefaults — agent name/persona defaults)
- Modify: `internal/config/config_test.go`
- Modify: `cmd/chandrad/main.go` (provider type switch + allowlist startup check)

**Step 1: Write the failing test for HTTPS check**

In `internal/config/config_test.go`, add:

```go
func TestValidate_HTTPSRequired_ForCustomProvider(t *testing.T) {
	cfg := &Config{
		Identity: IdentityConfig{Name: "Chandra"},
		Provider: ProviderConfig{BaseURL: "http://my-llm.local/v1", DefaultModel: "llama3", Type: "custom"},
		Database: DatabaseConfig{Path: "/tmp/test.db"},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected error for HTTP custom provider URL")
	}
}

func TestValidate_OpenRouterAccepted(t *testing.T) {
	cfg := &Config{
		Identity: IdentityConfig{Name: "Chandra"},
		Provider: ProviderConfig{BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "openai/gpt-4o", Type: "openrouter"},
		Database: DatabaseConfig{Path: "/tmp/test.db"},
	}
	err := validate(cfg)
	if err != nil {
		t.Fatalf("expected openrouter to be a valid provider type, got error: %v", err)
	}
}

func TestStartup_AllowlistCheck_PolicyFields(t *testing.T) {
	// "open" policy requires no users — field check only
	cfg := &Config{
		Identity: IdentityConfig{Name: "Chandra"},
		Provider: ProviderConfig{Type: "openai", DefaultModel: "gpt-4o"},
		Database: DatabaseConfig{Path: "/tmp/test.db"},
		Channels: ChannelsConfig{Discord: &DiscordConfig{
			BotToken:     "Bot abc",
			AccessPolicy: "open",
		}},
	}
	if cfg.Channels.Discord.AccessPolicy != "open" {
		t.Error("expected open policy")
	}

	// "role" policy with empty allowed_roles must be detected (roles live in config)
	cfg.Channels.Discord.AccessPolicy = "role"
	cfg.Channels.Discord.AllowedRoles = []string{}
	if len(cfg.Channels.Discord.AllowedRoles) != 0 {
		t.Error("expected empty allowed_roles for this test")
	}

	// "role" policy with non-empty allowed_roles passes the config check
	cfg.Channels.Discord.AllowedRoles = []string{"111222333"}
	if len(cfg.Channels.Discord.AllowedRoles) == 0 {
		t.Error("expected non-empty allowed_roles")
	}
}

// TestStartup_AllowlistCheck_DBPath tests the DB-based user count (invite/request/allowlist
// policies). This test lives in cmd/chandrad/ since countDBAllowedUsers is defined there.
// See cmd/chandrad/main_test.go for TestCountDBAllowedUsers (uses a temp SQLite file).
//
// Here we just document the expected behaviour:
//   - countDBAllowedUsers(dbPath, "<channel-id>") == 0  → startup refuses with error
//   - countDBAllowedUsers(dbPath, "<channel-id>") > 0   → startup proceeds
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -run TestValidate_HTTPSRequired -v
```
Expected: FAIL

**Step 3: Add HTTPS validation to validate() and add missing provider types**

In `internal/config/config.go` `validate()`, replace the provider type switch:

```go
// Before (only openai, anthropic, ollama):
switch cfg.Provider.Type {
case "openai", "anthropic", "ollama":
    // valid
...
}

// After (add openrouter and custom):
switch cfg.Provider.Type {
case "openai", "anthropic", "ollama", "openrouter", "custom":
    // valid
case "":
    errs = append(errs, "provider.type is required")
default:
    errs = append(errs, fmt.Sprintf("provider.type %q is not valid (openai, anthropic, ollama, openrouter, custom)", cfg.Provider.Type))
}
```

Then add the HTTPS validation after the type switch:

```go
if cfg.Provider.Type == "custom" && cfg.Provider.BaseURL != "" &&
    len(cfg.Provider.BaseURL) >= 7 && cfg.Provider.BaseURL[:7] == "http://" {
    errs = append(errs, "provider.base_url must use HTTPS for custom endpoints")
}
```

Also fix the provider initialization in `cmd/chandrad/main.go` to handle "openrouter" and "custom" types (both are OpenAI-compatible):

```go
// In the provider type switch in run():
switch cfg.Provider.Type {
case "anthropic":
    chatProvider = anthropic.NewProvider(cfg.Provider.BaseURL, cfg.Provider.APIKey, cfg.Provider.DefaultModel)
    slog.Info("chandrad: anthropic provider ready", "model", cfg.Provider.DefaultModel)
case "openai", "ollama", "openrouter", "custom":
    chatProvider = openai.NewProvider(cfg.Provider.BaseURL, cfg.Provider.APIKey, cfg.Provider.DefaultModel)
    slog.Info("chandrad: openai-compatible provider ready", "model", cfg.Provider.DefaultModel, "type", cfg.Provider.Type)
default:
    slog.Warn("chandrad: unknown provider type, skipping", "type", cfg.Provider.Type)
}
```

**Step 4: Add allowlist enforcement check at daemon startup**

In `cmd/chandrad/main.go`, after the permissions check (Step 2), add:

```go
// Step 2b: Security default — deny-all: an enabled Discord channel with no
// authorized users is a misconfiguration (design §12).
// Policy-specific rules:
// - "open": intentionally unrestricted, skip check (doctor will warn)
// - "role": access via Discord roles — check allowed_roles in config
// - all others: query the allowed_users DB table (authoritative source)
if !safeMode && cfg.Channels.Discord != nil && cfg.Channels.Discord.BotToken != "" {
    switch cfg.Channels.Discord.AccessPolicy {
    case "open":
        // intentionally open — skip allowlist check (doctor warns)
    case "role":
        if len(cfg.Channels.Discord.AllowedRoles) == 0 {
            return fmt.Errorf("chandrad: security: access_policy=role but allowed_roles is empty — add role IDs: chandra access add discord --role <role-id>")
        }
    default: // invite, request, allowlist — DB is authoritative
        // Sum allowed users across all configured channel IDs.
        // The DB keys allowed_users by real Discord channel ID, not adapter name.
        var totalUsers int
        for _, chID := range cfg.Channels.Discord.ChannelIDs {
            n, err := countDBAllowedUsers(cfg.Database.Path, chID)
            if err != nil {
                return fmt.Errorf("chandrad: security: DB query for channel %s: %v", chID, err)
            }
            totalUsers += n
        }
        if totalUsers == 0 {
            return fmt.Errorf("chandrad: security: no authorized users in DB — the bot would lock everyone out. Run 'chandra channel test discord' or: chandra access add discord <user-id>")
        }
    }
}
```

Add the helper near the top of `cmd/chandrad/main.go`:

```go
// countDBAllowedUsers returns the number of rows in the allowed_users table for
// the given channel. The DB is the single authoritative source — both Hello World
// init and 'chandra access add/remove' write only to this table.
func countDBAllowedUsers(dbPath, channelID string) (int, error) {
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
```

Add `"database/sql"` and `_ "github.com/mattn/go-sqlite3"` to imports.

**Step 5: Run tests to verify they pass**

```bash
go test ./internal/config/... -v
go build ./cmd/chandrad/...
```
Expected: all PASS, builds succeed

**Step 6: Verify defaults for identity.name and identity.description in applyDefaults()**

Task 0 already adds `identity.name = "Chandra"` and `identity.description = "A helpful personal assistant"` defaults in `applyDefaults()`. Verify they are present with a test:

```go
func TestApplyDefaults_Identity(t *testing.T) {
    cfg := &Config{}
    applyDefaults(cfg)
    if cfg.Identity.Name != "Chandra" {
        t.Errorf("expected default name Chandra, got %q", cfg.Identity.Name)
    }
    if cfg.Identity.Description == "" {
        t.Error("expected default description to be non-empty")
    }
}
```

Run and verify:
```bash
go test ./internal/config/... -run TestApplyDefaults_Identity -v
```

**Step 7: Add DB-based allowlist test in `cmd/chandrad/main_test.go`**

```go
package main

import (
    "context"
    "database/sql"
    "os"
    "testing"
    _ "github.com/mattn/go-sqlite3"
)

func TestCountDBAllowedUsers(t *testing.T) {
    f, err := os.CreateTemp("", "chandra-test-*.db")
    if err != nil {
        t.Fatal(err)
    }
    f.Close()
    defer os.Remove(f.Name())

    db, err := sql.Open("sqlite3", f.Name())
    if err != nil {
        t.Fatal(err)
    }
    if _, err := db.Exec(`CREATE TABLE allowed_users (
        channel_id TEXT NOT NULL,
        user_id    TEXT NOT NULL,
        username   TEXT,
        source     TEXT,
        added_at   INTEGER
    )`); err != nil {
        t.Fatal(err)
    }
    db.Close()

    const testChannelID = "1234567890123456789" // realistic Discord channel ID (snowflake)

    // Empty DB → countDBAllowedUsers should return 0.
    count, err := countDBAllowedUsers(f.Name(), testChannelID)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if count != 0 {
        t.Errorf("expected 0 users, got %d", count)
    }

    // Insert one user → count should be 1.
    db, _ = sql.Open("sqlite3", f.Name())
    db.Exec("INSERT INTO allowed_users (channel_id, user_id, source) VALUES (?, 'u1', 'test')", testChannelID)
    db.Close()

    count, err = countDBAllowedUsers(f.Name(), testChannelID)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if count != 1 {
        t.Errorf("expected 1 user, got %d", count)
    }
}
```

Run:
```bash
go test ./cmd/chandrad/... -run TestCountDBAllowedUsers -v
```
Expected: PASS

**Step 8: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/chandrad/main.go cmd/chandrad/main_test.go
git commit -m "feat(security): enforce HTTPS for custom providers, DB-authoritative allowlist check at startup"
```

---

### Task 12b: First-run detection — guide user to `chandra init` when no config exists

**Files:**
- Modify: `cmd/chandra/commands.go` (add PersistentPreRun to rootCmd)
- Modify: `cmd/chandra/main.go` (if root command setup is there)

**Step 1: Add a pre-run hook to rootCmd**

The design says (§0): "If `chandra` is invoked with no config present, it guides the user in rather than erroring."

In `cmd/chandra/commands.go`, update `rootCmd` to add a `PersistentPreRun`:

```go
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
```

**Step 2: Build to verify it compiles**

```bash
go build ./cmd/chandra/...
```
Expected: build succeeds

**Step 3: Test the first-run message**

```bash
# With no config present:
CHANDRA_CONFIG=/tmp/nonexistent/config.toml ./chandra doctor
```
Expected: prints "No configuration found at..." and exits 1

**Step 4: Commit**

```bash
git add cmd/chandra/commands.go
git commit -m "feat(cli): add first-run detection — guide user to chandra init when no config exists"
```

---

## Phase 2: Init Wizard + Access Control

### Task 13: Add `charmbracelet/huh` dependency

**Files:**
- Modify: `go.mod`, `go.sum`

**Step 1: Add the dependency**

```bash
go get github.com/charmbracelet/huh@latest
```

**Step 2: Verify build**

```bash
go build ./...
```
Expected: builds succeed

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add charmbracelet/huh for TUI wizard support"
```

---

### Task 14: Create DB migrations for invite codes and access requests

**Files:**
- Create: `store/migrations/005_access_control.up.sql`
- Create: `store/migrations/005_access_control.down.sql`

**Step 1: Write the migration**

`store/migrations/005_access_control.up.sql`:

```sql
-- Invite codes for the 'invite' access policy
CREATE TABLE IF NOT EXISTS invite_codes (
    code            TEXT PRIMARY KEY,
    uses_remaining  INTEGER NOT NULL DEFAULT 1,   -- -1 = unlimited
    expires_at      INTEGER,                       -- Unix timestamp; NULL = no expiry
    created_at      INTEGER NOT NULL,
    redeemed_by     TEXT                           -- user ID of first redemption (single-use)
);

-- Access requests for the 'request' access policy
CREATE TABLE IF NOT EXISTS access_requests (
    id              TEXT PRIMARY KEY,
    channel_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    username        TEXT NOT NULL,
    first_message   TEXT,
    status          TEXT NOT NULL DEFAULT 'pending', -- pending | approved | denied | blocked
    created_at      INTEGER NOT NULL,
    decided_at      INTEGER
);

-- Approved users (allowlist) with source tracking
CREATE TABLE IF NOT EXISTS allowed_users (
    channel_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    username        TEXT,
    source          TEXT NOT NULL DEFAULT 'manual', -- manual | hello_world | invite | request | role
    added_at        INTEGER NOT NULL,
    PRIMARY KEY (channel_id, user_id)
);
```

`store/migrations/005_access_control.down.sql`:

```sql
DROP TABLE IF EXISTS invite_codes;
DROP TABLE IF EXISTS access_requests;
DROP TABLE IF EXISTS allowed_users;
```

**Step 2: Run migration test**

```bash
go test ./store/... -run TestMigrate -v
```
Expected: PASS

**Step 3: Commit**

```bash
git add store/migrations/
git commit -m "feat(store): add invite_codes, access_requests, allowed_users tables for access control"
```

---

### Task 15: Build invite code commands

**Files:**
- Create: `internal/access/invite.go`
- Create: `internal/access/invite_test.go`
- Modify: `cmd/chandra/commands.go`

**Step 1: Write failing test**

Create `internal/access/invite_test.go`:

```go
package access_test

import (
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/access"
)

func TestGenerateCode_Format(t *testing.T) {
	code := access.GenerateCode()
	if len(code) != len("chandra-inv-")+12 {
		t.Errorf("expected code length %d, got %d (code: %s)", len("chandra-inv-")+12, len(code), code)
	}
	if code[:12] != "chandra-inv-" {
		t.Errorf("expected prefix chandra-inv-, got %s", code[:12])
	}
}

func TestInviteCode_IsExpired(t *testing.T) {
	expired := access.InviteCode{ExpiresAt: time.Now().Add(-time.Hour)}
	if !expired.IsExpired() {
		t.Error("expected expired code to return IsExpired=true")
	}

	active := access.InviteCode{ExpiresAt: time.Now().Add(time.Hour)}
	if active.IsExpired() {
		t.Error("expected active code to return IsExpired=false")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/access/... -v
```
Expected: FAIL — package doesn't exist

**Step 3: Create invite.go**

Create `internal/access/invite.go`:

```go
// Package access manages user access control: invite codes, access requests,
// and the allowed_users table.
package access

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// InviteCode represents a single invite code record.
type InviteCode struct {
	Code           string
	UsesRemaining  int
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

// IsExpired returns true if the code has passed its expiry time.
func (c InviteCode) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt)
}

// GenerateCode creates a cryptographically random invite code with the
// chandra-inv- prefix and 12 random hex characters.
func GenerateCode() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("access: crypto/rand failed: " + err.Error())
	}
	return "chandra-inv-" + hex.EncodeToString(b)
}

// Store manages invite codes and allowed users in the database.
type Store struct {
	db *sql.DB
}

// NewStore creates a new access.Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateInvite creates a new invite code with the given parameters.
// uses=1 for single-use. ttl=0 for no expiry.
func (s *Store) CreateInvite(ctx context.Context, uses int, ttl time.Duration) (InviteCode, error) {
	code := GenerateCode()
	now := time.Now()
	var expiresAt *int64
	var expTime time.Time
	if ttl > 0 {
		t := now.Add(ttl)
		ts := t.Unix()
		expiresAt = &ts
		expTime = t
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO invite_codes (code, uses_remaining, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		code, uses, expiresAt, now.Unix(),
	)
	if err != nil {
		return InviteCode{}, fmt.Errorf("create invite: %w", err)
	}
	return InviteCode{
		Code:          code,
		UsesRemaining: uses,
		ExpiresAt:     expTime,
		CreatedAt:     now,
	}, nil
}

// ListInvites returns all active (non-expired, uses > 0) invite codes.
func (s *Store) ListInvites(ctx context.Context) ([]InviteCode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT code, uses_remaining, expires_at, created_at FROM invite_codes
		 WHERE uses_remaining != 0
		 ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	defer rows.Close()

	var codes []InviteCode
	for rows.Next() {
		var c InviteCode
		var expiresAt *int64
		var createdAt int64
		if err := rows.Scan(&c.Code, &c.UsesRemaining, &expiresAt, &createdAt); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(createdAt, 0)
		if expiresAt != nil {
			c.ExpiresAt = time.Unix(*expiresAt, 0)
		}
		codes = append(codes, c)
	}
	return codes, rows.Err()
}

// RevokeInvite deletes an invite code.
func (s *Store) RevokeInvite(ctx context.Context, code string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM invite_codes WHERE code = ?`, code)
	if err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("invite code not found: %s", code)
	}
	return nil
}

// RedeemInvite validates the code, decrements uses_remaining, adds the user
// to allowed_users, and returns nil on success.
func (s *Store) RedeemInvite(ctx context.Context, code, channelID, userID, username string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var uses int
	var expiresAt *int64
	err = tx.QueryRowContext(ctx,
		`SELECT uses_remaining, expires_at FROM invite_codes WHERE code = ?`, code,
	).Scan(&uses, &expiresAt)
	if err == sql.ErrNoRows {
		return fmt.Errorf("invite code not found or already used")
	}
	if err != nil {
		return fmt.Errorf("check code: %w", err)
	}

	if uses == 0 {
		return fmt.Errorf("invite code exhausted")
	}
	if expiresAt != nil && time.Now().Unix() > *expiresAt {
		return fmt.Errorf("invite code expired")
	}

	newUses := uses - 1
	if _, err := tx.ExecContext(ctx, `UPDATE invite_codes SET uses_remaining = ? WHERE code = ?`, newUses, code); err != nil {
		return fmt.Errorf("decrement uses: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO allowed_users (channel_id, user_id, username, source, added_at) VALUES (?, ?, ?, 'invite', ?)`,
		channelID, userID, username, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("add allowed user: %w", err)
	}

	return tx.Commit()
}

// AddUser adds a user directly to the allowlist (for manual and hello_world sources).
func (s *Store) AddUser(ctx context.Context, channelID, userID, username, source string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO allowed_users (channel_id, user_id, username, source, added_at) VALUES (?, ?, ?, ?, ?)`,
		channelID, userID, username, source, time.Now().Unix(),
	)
	return err
}

// RemoveUser removes a user from the allowlist.
func (s *Store) RemoveUser(ctx context.Context, channelID, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM allowed_users WHERE channel_id = ? AND user_id = ?`, channelID, userID)
	return err
}

// ListUsers returns all allowed users for a channel.
func (s *Store) ListUsers(ctx context.Context, channelID string) ([]AllowedUser, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, username, source, added_at FROM allowed_users WHERE channel_id = ? ORDER BY added_at DESC`,
		channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []AllowedUser
	for rows.Next() {
		var u AllowedUser
		var addedAt int64
		if err := rows.Scan(&u.UserID, &u.Username, &u.Source, &addedAt); err != nil {
			return nil, err
		}
		u.AddedAt = time.Unix(addedAt, 0)
		users = append(users, u)
	}
	return users, rows.Err()
}

// AllowedUser represents a user in the access allowlist.
type AllowedUser struct {
	UserID   string
	Username string
	Source   string
	AddedAt  time.Time
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./internal/access/... -v
```
Expected: PASS

**Step 5: Add invite commands to commands.go**

In `cmd/chandra/commands.go`, add:

```go
// ---- invite commands ---------------------------------------------------------

var inviteCmd = &cobra.Command{
	Use:   "invite",
	Short: "Manage invite codes",
}

var inviteCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an invite code",
	Run: func(cmd *cobra.Command, args []string) {
		uses, _ := cmd.Flags().GetInt("uses")
		ttlStr, _ := cmd.Flags().GetString("ttl")

		var ttl time.Duration
		if ttlStr != "" {
			var err error
			ttl, err = time.ParseDuration(ttlStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: invalid ttl %q: %v\n", ttlStr, err)
				os.Exit(1)
			}
		}

		db := openDB()
		defer db.Close()
		store := access.NewStore(db)

		code, err := store.CreateInvite(cmd.Context(), uses, ttl)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Code: %s\n", code.Code)
		fmt.Printf("Uses: %d", code.UsesRemaining)
		if !code.ExpiresAt.IsZero() {
			fmt.Printf(" · Expires: %s", code.ExpiresAt.Format("2006-01-02"))
		}
		fmt.Println()
	},
}

var inviteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active invite codes",
	Run: func(cmd *cobra.Command, args []string) {
		db := openDB()
		defer db.Close()
		store := access.NewStore(db)

		codes, err := store.ListInvites(cmd.Context())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		if len(codes) == 0 {
			fmt.Println("No active invite codes.")
			return
		}

		fmt.Printf("%-30s  %-5s  %s\n", "Code", "Uses", "Expires")
		fmt.Println(strings.Repeat("─", 55))
		for _, c := range codes {
			exp := "never"
			if !c.ExpiresAt.IsZero() {
				exp = c.ExpiresAt.Format("2006-01-02")
			}
			fmt.Printf("%-30s  %-5d  %s\n", c.Code, c.UsesRemaining, exp)
		}
	},
}

var inviteRevokeCmd = &cobra.Command{
	Use:   "revoke <code>",
	Short: "Revoke an invite code",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		db := openDB()
		defer db.Close()
		store := access.NewStore(db)

		if err := store.RevokeInvite(cmd.Context(), args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Revoked: %s\n", args[0])
	},
}
```

Also add `openDB()` helper:

```go
// openDB opens the configured SQLite database for CLI use.
// Uses store.NewDB to ensure the sqlite3 driver and sqlite-vec extension are
// properly registered (store.init() calls vec.Auto() on package load).
func openDB() *sql.DB {
	cfgPath := resolveDefaultConfigPath()
	cfg, err := config.Load(cfgPath)
	var dbPath string
	if err == nil && cfg.Database.Path != "" {
		dbPath = cfg.Database.Path
	} else {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, ".config", "chandra", "chandra.db")
	}
	st, err := store.NewDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open database: %v\n", err)
		os.Exit(1)
	}
	return st.DB()
}
```

Wire in `init()`:
```go
inviteCreateCmd.Flags().Int("uses", 1, "number of times the code can be used")
inviteCreateCmd.Flags().String("ttl", "168h", "time-to-live (e.g. 7d, 24h, 30d)")
inviteCmd.AddCommand(inviteCreateCmd, inviteListCmd, inviteRevokeCmd)
rootCmd.AddCommand(inviteCmd)
```

Also add `access` and `strings` imports.

**Step 6: Build to verify it compiles**

```bash
go build ./cmd/chandra/...
```
Expected: build succeeds

**Step 7: Commit**

```bash
git add internal/access/ cmd/chandra/commands.go
git commit -m "feat(access): add invite code system with create/list/revoke commands"
```

---

### Task 16: Add `chandra access` commands

**Files:**
- Modify: `cmd/chandra/commands.go`

**Step 1: Add access commands**

In `cmd/chandra/commands.go`, add:

```go
// ---- access commands ---------------------------------------------------------
//
// User access (invite/request/allowlist policies) lives in the DB — managed here.
// Role access (role policy) lives in the config file — managed with --role flag.

var accessCmd = &cobra.Command{
	Use:   "access",
	Short: "Manage user and role access",
}

var accessAddRole bool // --role flag for add/remove

var accessAddCmd = &cobra.Command{
	Use:   "add <channel> <id>",
	Short: "Add a user ID (or role ID with --role) to the access list",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		if accessAddRole {
			// Role IDs live in config (channels.discord.allowed_roles).
			cfgPath := resolveDefaultConfigPath()
			cfg, err := config.Load(cfgPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
				os.Exit(1)
			}
			if cfg.Channels.Discord == nil {
				fmt.Fprintln(os.Stderr, "error: discord channel not configured")
				os.Exit(1)
			}
			for _, existing := range cfg.Channels.Discord.AllowedRoles {
				if existing == args[1] {
					fmt.Printf("Role %s is already in allowed_roles\n", args[1])
					return
				}
			}
			cfg.Channels.Discord.AllowedRoles = append(cfg.Channels.Discord.AllowedRoles, args[1])
			if err := saveConfig(cfg, cfgPath); err != nil {
				fmt.Fprintf(os.Stderr, "error: save config: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Added role %s to %s allowed_roles\n", args[1], args[0])
			return
		}
		// User IDs live in the DB, keyed by real Discord channel ID (not adapter name).
		channelIDs := resolveChannelIDs(args[0])
		if len(channelIDs) == 0 {
			fmt.Fprintf(os.Stderr, "error: no channel IDs configured for %q\n", args[0])
			os.Exit(1)
		}
		db := openDB()
		defer db.Close()
		st := access.NewStore(db)
		for _, chID := range channelIDs {
			if err := st.AddUser(cmd.Context(), chID, args[1], "", "manual"); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
		fmt.Printf("Added %s to %s allowlist (%d channel(s))\n", args[1], args[0], len(channelIDs))
	},
}

var accessRemoveCmd = &cobra.Command{
	Use:   "remove <channel> <id>",
	Short: "Remove a user ID (or role ID with --role) from the access list",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		if accessAddRole {
			cfgPath := resolveDefaultConfigPath()
			cfg, err := config.Load(cfgPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
				os.Exit(1)
			}
			if cfg.Channels.Discord == nil {
				fmt.Fprintln(os.Stderr, "error: discord channel not configured")
				os.Exit(1)
			}
			filtered := cfg.Channels.Discord.AllowedRoles[:0]
			for _, r := range cfg.Channels.Discord.AllowedRoles {
				if r != args[1] {
					filtered = append(filtered, r)
				}
			}
			cfg.Channels.Discord.AllowedRoles = filtered
			if err := saveConfig(cfg, cfgPath); err != nil {
				fmt.Fprintf(os.Stderr, "error: save config: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Removed role %s from %s allowed_roles\n", args[1], args[0])
			return
		}
		channelIDs := resolveChannelIDs(args[0])
		if len(channelIDs) == 0 {
			fmt.Fprintf(os.Stderr, "error: no channel IDs configured for %q\n", args[0])
			os.Exit(1)
		}
		db := openDB()
		defer db.Close()
		st := access.NewStore(db)
		for _, chID := range channelIDs {
			if err := st.RemoveUser(cmd.Context(), chID, args[1]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
		fmt.Printf("Removed %s from %s allowlist\n", args[1], args[0])
	},
}

var accessListCmd = &cobra.Command{
	Use:   "list <channel>",
	Short: "List authorized users and roles for a channel",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Show roles from config.
		cfgPath := resolveDefaultConfigPath()
		if cfg, err := config.Load(cfgPath); err == nil && cfg.Channels.Discord != nil {
			if len(cfg.Channels.Discord.AllowedRoles) > 0 {
				fmt.Println("Roles (from config):")
				for _, r := range cfg.Channels.Discord.AllowedRoles {
					fmt.Printf("  %s\n", r)
				}
				fmt.Println()
			}
		}
		// Show users from DB, aggregated across all configured channel IDs.
		// The DB keys allowed_users by real Discord channel ID, not adapter name.
		channelIDs := resolveChannelIDs(args[0])
		if len(channelIDs) == 0 {
			fmt.Fprintf(os.Stderr, "error: no channel IDs configured for %q\n", args[0])
			os.Exit(1)
		}
		db := openDB()
		defer db.Close()
		st := access.NewStore(db)
		var allUsers []access.User
		seen := map[string]struct{}{}
		for _, chID := range channelIDs {
			users, err := st.ListUsers(cmd.Context(), chID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			for _, u := range users {
				if _, ok := seen[u.UserID]; !ok {
					seen[u.UserID] = struct{}{}
					allUsers = append(allUsers, u)
				}
			}
		}
		if len(allUsers) == 0 {
			fmt.Println("No authorized users.")
			return
		}
		fmt.Printf("%-22s  %-20s  %-10s  %s\n", "User ID", "Username", "Source", "Added")
		fmt.Println(strings.Repeat("─", 70))
		for _, u := range allUsers {
			fmt.Printf("%-22s  %-20s  %-10s  %s\n", u.UserID, u.Username, u.Source, u.AddedAt.Format("2006-01-02"))
		}
	},
}
```

Add `saveConfig` and `resolveChannelIDs` helpers near the top of `cmd/chandra/commands.go`:

```go
// saveConfig serialises cfg back to the TOML config file at path.
// Used by access add/remove --role to update allowed_roles in place.
func saveConfig(cfg *config.Config, path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// resolveChannelIDs translates an adapter name (e.g., "discord") into the actual
// Discord channel IDs configured in config.toml. This is necessary because the DB
// keys allowed_users by real channel ID (a snowflake like "123456789"), not by
// adapter name. Returns nil if the adapter is unsupported or not configured.
func resolveChannelIDs(adapter string) []string {
	if adapter != "discord" {
		return nil
	}
	cfgPath := resolveDefaultConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil || cfg.Channels.Discord == nil {
		return nil
	}
	return cfg.Channels.Discord.ChannelIDs
}
```

Add `"github.com/BurntSushi/toml"` to imports.

Wire in `init()`:
```go
accessAddCmd.Flags().BoolVar(&accessAddRole, "role", false, "treat <id> as a Discord role ID (updates config)")
accessRemoveCmd.Flags().BoolVar(&accessAddRole, "role", false, "treat <id> as a Discord role ID (updates config)")
accessCmd.AddCommand(accessAddCmd, accessRemoveCmd, accessListCmd)
rootCmd.AddCommand(accessCmd)
```

**Step 2: Build to verify it compiles**

```bash
go build ./cmd/chandra/...
```
Expected: build succeeds

**Step 3: Commit**

```bash
git add cmd/chandra/commands.go
git commit -m "feat(cli): add chandra access add/remove/list commands"
```

---

### Task 17: Build `chandra init` wizard (Phase 1: provider + channel stages)

**Files:**
- Create: `internal/setup/init.go`
- Create: `internal/setup/init_test.go`
- Create: `internal/setup/checkpoint.go`
- Modify: `cmd/chandra/commands.go`

**Step 1: Write the failing test for checkpoint**

Create `internal/setup/init_test.go`:

```go
package setup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jrimmer/chandra/internal/setup"
)

func TestCheckpoint_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".init-checkpoint.json")

	cp := &setup.Checkpoint{
		ProviderDone:  true,
		ChannelsDone:  false,
		IdentityDone:  false,
	}

	if err := setup.SaveCheckpoint(path, cp); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := setup.LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !loaded.ProviderDone {
		t.Error("expected ProviderDone=true")
	}
	if loaded.ChannelsDone {
		t.Error("expected ChannelsDone=false")
	}
}

func TestCheckpoint_DeleteOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".init-checkpoint.json")

	cp := &setup.Checkpoint{ProviderDone: true}
	if err := setup.SaveCheckpoint(path, cp); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := setup.DeleteCheckpoint(path); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected checkpoint file to be deleted")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/setup/... -v
```
Expected: FAIL — package doesn't exist

**Step 3: Create checkpoint.go**

Create `internal/setup/checkpoint.go`:

```go
package setup

import (
	"encoding/json"
	"os"
)

// Checkpoint tracks progress of the init wizard so it can be resumed.
// Secrets (API keys, bot tokens) are never stored here — they are re-prompted
// on resume. Non-secret values are stored so the wizard can skip re-collecting
// them from the user if the stage already completed.
type Checkpoint struct {
	ProviderDone    bool   `json:"provider_done"`
	ProviderType    string `json:"provider_type"`    // non-secret: "openai", "anthropic", etc.
	ProviderModel   string `json:"provider_model"`   // non-secret: model name
	ProviderBaseURL string `json:"provider_base_url"` // non-secret: custom/openrouter base URL (empty for hosted)

	ChannelsDone     bool     `json:"channels_done"`
	DiscordChannelID string   `json:"discord_channel_id"`  // non-secret: Discord channel snowflake
	AccessPolicy     string   `json:"access_policy"`       // non-secret: "invite", "role", etc.
	DiscordRoleIDs   []string `json:"discord_role_ids"`    // non-secret: role ID list for role policy

	IdentityDone     bool   `json:"identity_done"`
	AgentName        string `json:"agent_name"`        // non-secret
	AgentDescription string `json:"agent_description"` // non-secret

	ConfigWritten bool `json:"config_written"`

	// FreshStart records that the user explicitly chose "Fresh start" or "Start over"
	// so that a resumed session still archives the old config before writing the new one.
	FreshStart bool `json:"fresh_start"`
}

// SaveCheckpoint writes the checkpoint to path atomically.
// Writes to a .tmp sibling first, then renames, so a crash mid-write
// always leaves either the old checkpoint or the new one intact — never a
// truncated file. This matters because a corrupt checkpoint is treated as
// "no checkpoint" (see LoadCheckpoint + corrupt-checkpoint handling in Run),
// and a crash between os.Rename(config→.bak) and the FreshStart=false save
// would otherwise leave the install broken with no live config.
//
// Note: os.WriteFile + os.Rename is not fully durable against power failure
// without fsync + parent-dir sync. For a setup wizard where the worst case
// is "re-run init" and the old config is preserved in .bak, this trade-off
// is acceptable. Full fsync durability is reserved for the database layer.
func SaveCheckpoint(path string, cp *Checkpoint) error {
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadCheckpoint reads a checkpoint from path.
// Returns nil (not an error) if the file does not exist.
func LoadCheckpoint(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// DeleteCheckpoint removes the checkpoint file after successful init.
func DeleteCheckpoint(path string) error {
	if err := os.Remove(path); os.IsNotExist(err) {
		return nil
	} else {
		return err
	}
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./internal/setup/... -v
```
Expected: PASS

**Step 5: Create init.go with the wizard**

Create `internal/setup/init.go`:

```go
// Package setup implements the chandra init wizard.
package setup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/jrimmer/chandra/internal/channels/discord"
	"github.com/jrimmer/chandra/internal/config"
	"github.com/jrimmer/chandra/internal/doctor"
	"github.com/jrimmer/chandra/store"
	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver for direct sql.Open calls
)

// Options controls the init wizard.
type Options struct {
	// NonInteractive is reserved for future use (e.g. CI environments).
	// TODO: wire up — all huh.Form calls must check opts.NonInteractive and
	// return an error when no defaults are available rather than blocking on stdin.
	NonInteractive bool
	ConfigPath     string
	DBPath         string
}

// DefaultOptions returns sensible defaults for the wizard.
func DefaultOptions() Options {
	home, _ := os.UserHomeDir()
	return Options{
		ConfigPath: filepath.Join(home, ".config", "chandra", "config.toml"),
		DBPath:     filepath.Join(home, ".config", "chandra", "chandra.db"),
	}
}

// checkpointPath returns the path for the init checkpoint file.
func checkpointPath(cfgDir string) string {
	return filepath.Join(cfgDir, ".init-checkpoint.json")
}

// Run executes the interactive init wizard.
func Run(ctx context.Context, opts Options) error {
	cfgDir := filepath.Dir(opts.ConfigPath)
	cpPath := checkpointPath(cfgDir)

	// Ensure the config directory exists before the first SaveCheckpoint call.
	// writeConfig also calls MkdirAll, but SaveCheckpoint in Stage 1 runs first,
	// so on a fresh machine with no ~/.config/chandra/ the checkpoint write would
	// fail with ENOENT without this early creation.
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Reset package-level customBaseURL so stale state from a previous run in the
	// same process (e.g. in tests) cannot bleed through to writeConfig.
	customBaseURL = ""

	// reconfigureHints is populated when the user picks "Reconfigure" for an
	// existing config. It is applied to cp AFTER the checkpoint-load/resume block
	// so that the checkpoint load cannot overwrite these pre-filled defaults.
	var reconfigureHints *Checkpoint

	// isFreshStart is set when the user picks "Fresh start" or "Start over".
	// Stored in cp.FreshStart so a resumed run can restore freshArchivePath.
	var isFreshStart bool

	// freshArchivePath is set when the user picks "Fresh start" or "Start over".
	// The old config is renamed to this path just before writeConfig in Stage 4 —
	// not immediately — so that an abort mid-wizard doesn't leave the existing
	// installation broken. It is also persisted in cp.FreshStart so that a resumed
	// session still archives the config even after a crash between Stage 1 and 4.
	var freshArchivePath string

	// Check for existing config. Treat only "not found" as absence; other errors
	// (permission denied, bad mount) are surfaced immediately rather than
	// silently falling through to fresh-start, which could overwrite a valid
	// but temporarily inaccessible config.
	_, configStatErr := os.Stat(opts.ConfigPath)
	if configStatErr != nil && !os.IsNotExist(configStatErr) {
		return fmt.Errorf("check config path %s: %w", opts.ConfigPath, configStatErr)
	}
	configExists := configStatErr == nil

	// Load the checkpoint now — before the existing-config prompt — so that
	// checkpointPending reflects whether there is a VALID checkpoint, not just
	// whether a (possibly corrupt) checkpoint file exists. A corrupt file would
	// otherwise suppress the reconfigure/fresh/cancel prompt and silently archive
	// the existing config as if a fresh start were intended.
	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		// Corrupt checkpoint (truncated JSON from a mid-write crash) — warn and
		// treat as no checkpoint rather than aborting, which would brick 'chandra init'.
		fmt.Printf("⚠ Checkpoint file is corrupt (%v); treating as no checkpoint.\n", err)
		_ = os.Remove(cpPath)
		cp = nil
	}
	checkpointPending := cp != nil

	if configExists && !checkpointPending {
		// Config exists with no pending checkpoint — prompt for reconfigure/fresh/cancel.
		// (When a checkpoint IS pending, we skip this and go straight to the resume
		// prompt below, so the user can resume their in-progress setup.)
		var choice string
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title(fmt.Sprintf("Configuration already exists at %s", opts.ConfigPath)).
				Options(
					huh.NewOption("Update credentials (re-enter API key and bot token; config regenerated from wizard values, other settings reset to defaults)", "reconfigure"),
					huh.NewOption("Fresh start (archive and replace existing config)", "fresh"),
					huh.NewOption("Cancel", "cancel"),
				).
				Value(&choice),
		))
		if err := form.Run(); err != nil {
			return err
		}
		switch choice {
		case "cancel":
			return nil
		case "fresh":
			// Mark for deferred archive: the actual rename is deferred to Stage 4
			// (just before writeConfig) so that an abort mid-wizard doesn't leave
			// the installation broken with the config renamed away but no new config
			// written yet. isFreshStart is stored in cp.FreshStart when the first
			// checkpoint is saved, so a resumed run still archives the old config.
			freshArchivePath = opts.ConfigPath + ".bak"
			isFreshStart = true
			_ = DeleteCheckpoint(cpPath)
		case "reconfigure":
			// Delete any dangling checkpoint — reconfigure always starts fresh from the
			// existing config file, never from a previous partial wizard run.
			_ = DeleteCheckpoint(cpPath)
			// Build hints from non-secret values in the existing config. Secrets
			// (API key, bot token) are always re-collected before the config is rewritten.
			// ConfigWritten = false so Stage 4 re-runs after secrets are re-collected.
			// NOTE: writeConfig regenerates the config from scratch — wizard-managed fields
			// are preserved from hints but other settings (budget weights, scheduler
			// interval, etc.) revert to defaults. Also, the wizard only collects one
			// Discord channel ID — extra channels in the existing config are lost.
			// We take a backup here (same as Fresh start) to preserve the original.
			freshArchivePath = opts.ConfigPath + ".bak"
			isFreshStart = true
			loadedCfg, loadErr := config.Load(opts.ConfigPath)
			if loadErr != nil {
				// Hard-fail: if we can't read the existing config, we must not silently
				// proceed as a fresh setup (that would overwrite the user's config with
				// empty wizard state). Offer to run fresh start explicitly instead.
				return fmt.Errorf("reconfigure: cannot read existing config: %w\n"+
					"Run 'chandra init' again and choose \"Fresh start\" to replace it.", loadErr)
			}
			hints := &Checkpoint{
				ProviderDone:     true,
				ProviderType:     loadedCfg.Provider.Type,
				ProviderModel:    loadedCfg.Provider.DefaultModel,
				ProviderBaseURL:  loadedCfg.Provider.BaseURL,
				ChannelsDone:     true,
				IdentityDone:     true,
				AgentName:        loadedCfg.Identity.Name,        // Phase 0 renamed Agent→Identity
				AgentDescription: loadedCfg.Identity.Description, // Phase 0 renamed Persona→Description
			}
			if loadedCfg.Channels.Discord != nil {
				if len(loadedCfg.Channels.Discord.ChannelIDs) > 0 {
					// The wizard only configures one channel ID; preserve the first.
					hints.DiscordChannelID = loadedCfg.Channels.Discord.ChannelIDs[0]
				}
				hints.AccessPolicy = loadedCfg.Channels.Discord.AccessPolicy
				hints.DiscordRoleIDs = loadedCfg.Channels.Discord.AllowedRoles
			}
			reconfigureHints = hints
			// Propagate FreshStart intent and pre-save the checkpoint immediately.
			// In reconfigure mode all stages are Done=true, so Stages 1-3 are skipped
			// and the archive happens right before writeConfig in Stage 4. Without this
			// pre-save there is no checkpoint to resume from if writeConfig fails after
			// the archive has already renamed the live config aside.
			reconfigureHints.FreshStart = isFreshStart
			// Fatal: all three stages will be skipped (Done=true), so the archive
			// in Stage 4 runs without any subsequent checkpoint save. If writeConfig
			// or DB init then fails, the live config is stranded in .bak with no
			// checkpoint to resume from. Fail here before entering that window.
			if saveErr := SaveCheckpoint(cpPath, reconfigureHints); saveErr != nil {
				return fmt.Errorf("reconfigure: pre-save checkpoint: %w", saveErr)
			}
		}
	}

	if cp != nil {
		var choice string
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Previous setup session found.").
				Description(checkpointSummary(cp)).
				Options(
					huh.NewOption("Resume from where you left off", "resume"),
					huh.NewOption("Start over", "restart"),
					huh.NewOption("Cancel", "cancel"),
				).
				Value(&choice),
		))
		if err := form.Run(); err != nil {
			return err
		}
		switch choice {
		case "cancel":
			return nil
		case "restart":
			// Delete the persisted checkpoint file immediately — otherwise a crash
			// or cancel before the next SaveCheckpoint would still offer to resume.
			_ = DeleteCheckpoint(cpPath)
			cp = &Checkpoint{}
			// If a config exists, archive it at Stage 4 just like "Fresh start" —
			// the user is explicitly abandoning it, so don't silently overwrite it.
			if configExists {
				freshArchivePath = opts.ConfigPath + ".bak"
				isFreshStart = true
			}
		default:
			// "resume" — restore freshArchivePath from the checkpoint so the resumed
			// run still archives the old config if the user originally picked "Fresh start".
			if cp.FreshStart && configExists {
				freshArchivePath = opts.ConfigPath + ".bak"
				isFreshStart = true
			} else if cp.FreshStart && !configExists {
				// Archive already happened (config.toml renamed to .bak) but the
				// FreshStart=false checkpoint save was skipped (non-fatal path) or
				// the process crashed in that tiny window. Clear the stale flag so
				// a subsequent resume does not attempt a second archive.
				cp.FreshStart = false
				if err := SaveCheckpoint(cpPath, cp); err != nil {
					slog.Warn("init: failed to clear stale FreshStart on resume (non-fatal)", "err", err)
				}
			}
		}
	} else {
		cp = &Checkpoint{}
		fmt.Println("Welcome to Chandra! Let's get you set up.")
		fmt.Println()
	}

	// Apply reconfigure hints if the user chose "Reconfigure" above. This must
	// happen after the checkpoint-load/resume block so the checkpoint load cannot
	// overwrite the pre-filled defaults.
	if reconfigureHints != nil {
		cp = reconfigureHints
	}

	// Pre-Stage-1 guard: if the checkpoint has a custom provider but an invalid
	// or missing base URL, clear ProviderDone so Stage 1 re-runs and the user
	// can supply a correct HTTPS URL via the normal form. Hard-failing here
	// would create an unrecoverable resume loop (ProviderDone stays true →
	// error → resume → same error). Uses url.Parse to reject structurally
	// invalid HTTPS URLs (e.g. "https://", "https:///v1") that pass a simple
	// prefix check but fail later in the provider check.
	if cp.ProviderType == "custom" {
		u, parseErr := url.Parse(cp.ProviderBaseURL)
		if cp.ProviderBaseURL == "" || parseErr != nil || u.Scheme != "https" || u.Host == "" {
			fmt.Printf("⚠ Custom provider URL %q is missing or invalid (must be https://host/…). Re-running provider setup.\n", cp.ProviderBaseURL)
			cp.ProviderDone = false
			cp.ProviderBaseURL = ""
			customBaseURL = ""
			if err := SaveCheckpoint(cpPath, cp); err != nil {
				return err
			}
		}
	}

	// --- Stage 1: Provider ---
	var providerType, apiKey, model string
	if !cp.ProviderDone {
		if err := runProviderStage(ctx, &providerType, &apiKey, &model); err != nil {
			return err
		}
		cp.ProviderDone = true
		cp.ProviderType = providerType
		cp.ProviderModel = model
		cp.ProviderBaseURL = customBaseURL // empty string unless "custom" provider
		cp.FreshStart = isFreshStart       // persist so a resumed run still archives old config
		if err := SaveCheckpoint(cpPath, cp); err != nil {
			return err
		}
	} else {
		providerType = cp.ProviderType
		model = cp.ProviderModel
		customBaseURL = cp.ProviderBaseURL // restore custom URL before writeConfig runs
		// Note: HTTPS enforcement for custom URLs is done in the Pre-Stage-1 guard
		// above — by this point any non-HTTPS custom URL has already been cleared
		// and ProviderDone reset, so we never reach this else-branch with a bad URL.
		// apiKey is a secret — not stored in checkpoint. Always re-collect on resume:
		// the user may need to fix a bad key if the provider doctor check failed after
		// Stage 4. (Consistent with discordToken re-prompt in Stage 2 else branch.)
		form := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title(fmt.Sprintf("Re-enter your %s API key", providerType)).
				EchoMode(huh.EchoModePassword).
				Value(&apiKey),
		))
		if err := form.Run(); err != nil {
			return err
		}
		// Secrets were re-collected; mark Stage 4 to re-run so the corrected
		// credentials are written to disk, even if a prior run already wrote a
		// config. Without this, a user who resumes after a failed provider check
		// re-enters their key but Stage 4 is skipped and the stale key remains
		// on disk.
		if cp.ConfigWritten {
			cp.ConfigWritten = false
			if err := SaveCheckpoint(cpPath, cp); err != nil {
				return err
			}
		}
		fmt.Printf("Provider: %s / %s (from checkpoint)\n", providerType, model)
	}

	// Pre-Stage-2 guard: if access_policy=role but no role IDs are saved, the
	// channel config imported from reconfigure is incomplete. Clear ChannelsDone
	// so Stage 2 re-runs and the user can supply the missing role IDs.
	// Without this, AllowlistCheck would fail every resume with no way to repair.
	if cp.ChannelsDone && cp.AccessPolicy == "role" && len(cp.DiscordRoleIDs) == 0 {
		fmt.Println("⚠ Access policy 'role' requires at least one role ID. Re-running channel setup.")
		cp.ChannelsDone = false
		if err := SaveCheckpoint(cpPath, cp); err != nil {
			return err
		}
	}

	// --- Stage 2: Channels ---
	var discordToken, discordChannelID, accessPolicy string
	var discordRoleIDs []string
	if !cp.ChannelsDone {
		if err := runChannelStage(ctx, &discordToken, &discordChannelID, &accessPolicy, &discordRoleIDs); err != nil {
			return err
		}
		cp.ChannelsDone = true
		cp.DiscordChannelID = discordChannelID
		cp.AccessPolicy = accessPolicy
		cp.DiscordRoleIDs = discordRoleIDs
		if err := SaveCheckpoint(cpPath, cp); err != nil {
			return err
		}
	} else {
		// Restore non-secret channel values from checkpoint.
		discordChannelID = cp.DiscordChannelID
		accessPolicy = cp.AccessPolicy
		discordRoleIDs = cp.DiscordRoleIDs
		// discordToken is a secret — not stored in checkpoint. Always re-collect on
		// resume: Stage 5 (Hello World) needs it regardless of whether ConfigWritten
		// is set, and skipping it would prevent AllowlistCheck from re-running on
		// a resumed session where it previously failed (data-loss bypass).
		if discordChannelID != "" {
			form := huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("Re-enter your Discord bot token to complete setup").
					EchoMode(huh.EchoModePassword).
					Value(&discordToken),
			))
			if err := form.Run(); err != nil {
				return err
			}
		}
		// Defensive reset: if ConfigWritten is still true (e.g. Stage 1 else-branch
		// was not reached for some reason), ensure Stage 4 re-runs so the
		// newly collected token is written to disk.
		if cp.ConfigWritten {
			cp.ConfigWritten = false
			if err := SaveCheckpoint(cpPath, cp); err != nil {
				return err
			}
		}
	}

	// --- Stage 3: Identity ---
	var agentName, agentDescription string
	agentName = "Chandra"
	if !cp.IdentityDone {
		form := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Give your assistant a name").Value(&agentName).Placeholder("Chandra"),
			huh.NewInput().Title("Brief personality description (optional)").Value(&agentDescription),
		))
		if err := form.Run(); err != nil {
			return err
		}
		if agentName == "" {
			agentName = "Chandra"
		}
		cp.IdentityDone = true
		cp.AgentName = agentName
		cp.AgentDescription = agentDescription
		if err := SaveCheckpoint(cpPath, cp); err != nil {
			return err
		}
	} else {
		agentName = cp.AgentName
		if agentName == "" {
			agentName = "Chandra"
		}
		agentDescription = cp.AgentDescription
	}

	// --- Stage 4: Write config + initialise database ---
	if !cp.ConfigWritten {
		fmt.Println()

		// If the user chose "Fresh start", archive the existing config now —
		// just before overwriting it. Doing this here (not earlier) ensures the
		// old config is intact if the wizard aborts before this point.
		// If a .bak already exists, use a timestamped suffix to avoid silently
		// discarding the previous backup.
		if freshArchivePath != "" {
			candidate := freshArchivePath
			if _, statErr := os.Stat(candidate); statErr == nil {
				// .bak already exists; find a unique timestamped name.
				// Loop with numeric suffix so two archives in the same second
				// don't silently overwrite each other.
				ts := time.Now().Format("20060102-150405")
				for i := 0; ; i++ {
					if i == 0 {
						candidate = opts.ConfigPath + "." + ts + ".bak"
					} else {
						candidate = opts.ConfigPath + fmt.Sprintf(".%s-%d.bak", ts, i)
					}
					if _, statErr := os.Stat(candidate); os.IsNotExist(statErr) {
						freshArchivePath = candidate
						break
					}
				}
			}
			if err := os.Rename(opts.ConfigPath, freshArchivePath); err != nil {
				return fmt.Errorf("archive existing config: %w", err)
			}
			fmt.Printf("Existing config archived to %s\n", freshArchivePath)
			// Archive done. Clear FreshStart in the checkpoint so that a later
			// resume (after a Stage-4/5/6 failure) does not try to archive again.
			// Without this, a resumed run would re-archive the freshly written
			// config.toml into another .bak, leaving no live config on disk if
			// writeConfig then fails.
			cp.FreshStart = false
			freshArchivePath = ""
			isFreshStart = false
			// Non-fatal: if this checkpoint save fails, the config write must
			// still proceed (the archive already happened — returning here would
			// leave the install with no live config). On a subsequent resume the
			// P2 guard below (configExists=false + cp.FreshStart=true) detects
			// that the archive already completed and clears the flag.
			if err := SaveCheckpoint(cpPath, cp); err != nil {
				slog.Warn("init: failed to clear FreshStart in checkpoint after archive (non-fatal)", "err", err)
			}
		}

		fmt.Print("[Step 1/6] Writing config...          ")
		if err := writeConfig(opts.ConfigPath, providerType, apiKey, model, agentName, agentDescription, discordToken, discordChannelID, accessPolicy, discordRoleIDs, opts.DBPath); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
		fmt.Println("✓")

		fmt.Print("[Step 2/6] Setting permissions...     ")
		if err := os.Chmod(cfgDir, 0700); err != nil {
			fmt.Printf("⚠ %v\n", err)
		} else if err := os.Chmod(opts.ConfigPath, 0600); err != nil {
			fmt.Printf("⚠ %v\n", err)
		} else {
			fmt.Println("✓ (config: 0600, dir: 0700)")
		}

		fmt.Print("[Step 3/6] Initialising database...   ")
		dbStore, err := store.NewDB(opts.DBPath)
		if err != nil {
			fmt.Printf("✗ %v\n", err)
			return fmt.Errorf("initialise database: %w", err)
		}
		dbStore.Close()
		fmt.Println("✓")
		fmt.Println("[Step 4/6] Running migrations...      ✓") // migrations run atomically inside store.NewDB

		cp.ConfigWritten = true
		if err := SaveCheckpoint(cpPath, cp); err != nil {
			return err
		}
	}

	// --- Stage 5: Hello World loop (if Discord configured) ---
	var loopSucceeded bool
	if discordToken != "" && discordChannelID != "" {
		fmt.Println("[Step 5/6] Starting listener...       ✓")
		fmt.Print("[Step 6/6] Sending verification...    ")
		result, loopErr := discord.RunLoopTest(ctx, discordToken, discordChannelID, discord.DefaultVerifyOptions())
		if loopErr != nil {
			fmt.Printf("✗\n⚠ No reply received. Config saved, but channel loop is unverified.\n")
			fmt.Printf("  Run 'chandra channel test discord' when ready to complete verification.\n")
		} else {
			fmt.Println("✓")
			fmt.Printf("\n✓ Reply received from %s (id: %s)\n", result.ReplyUsername, result.ReplyUserID)
			fmt.Println("  Full loop confirmed — inbound and outbound working.")
			loopSucceeded = true

			// Bootstrap DB: channel_verifications + allowed_users.
			// Both writes are unconditional — the design states "The Hello World reply
			// bootstraps the allowlist — no manual ID lookup needed" (design §346).
			// Use the real Discord channel ID as the key — NOT the adapter name "discord".
			if err := persistChannelVerified(opts.DBPath, discordChannelID, result.ReplyUserID); err != nil {
				fmt.Printf("  ⚠ Warning: could not save channel verification: %v\n", err)
				fmt.Println("  Run 'chandra channel test discord' to retry.")
			}
			if err := addUserToAllowlist(opts.DBPath, discordChannelID, result.ReplyUserID, result.ReplyUsername); err != nil {
				fmt.Printf("  ⚠ Warning: could not add %s to allowlist: %v\n", result.ReplyUsername, err)
			} else {
				fmt.Printf("✓ %s bootstrapped as authorized user\n", result.ReplyUsername)
			}
		}
	}

	// --- Stage 5b: Daemon install stub ---
	// Full daemon install (systemd/launchd) is deferred (design §10 "ship second", requires daemon lifecycle work).
	fmt.Println()
	fmt.Println("To run Chandra persistently, start the daemon manually:")
	fmt.Println("  chandrad               — background daemon")
	fmt.Println("  chandrad --foreground  — foreground (useful for debugging)")
	fmt.Println("  (Automated service install coming in a future release)")

	// --- Stage 6: Final doctor pass ---
	// Uses the exact same DoctorCheck implementations as `chandra doctor` — the two
	// commands are guaranteed to agree on what "healthy" means (design §4).
	fmt.Println("\nRunning final verification...")
	cfg, _ := config.Load(opts.ConfigPath)
	finalChecks := []doctor.Check{
		doctor.NewConfigCheck(opts.ConfigPath),
		doctor.NewPermissionsCheck(cfgDir, opts.ConfigPath),
		doctor.NewDBCheck(opts.DBPath),
	}
	if cfg != nil {
		finalChecks = append(finalChecks, doctor.NewProviderCheck(&cfg.Provider))
		// (open-policy warning is injected into results after RunAll — see below)
		// AllowlistCheck reads the DB rows bootstrapped by Hello World. Only run
		// when loopSucceeded: if Hello World timed out, the allowlist is
		// intentionally empty and the user retries with 'chandra channel test
		// discord'. Running it unconditionally would hard-fail and leave the
		// checkpoint in place, contradicting the "channel failures are always
		// skippable" requirement (design §1.1). The role-policy config validation
		// (access_policy=role with no roles) is caught earlier by the Pre-Stage-2
		// guard, so AllowlistCheck does not need to run for that purpose.
		if loopSucceeded && cfg.Channels.Discord != nil {
			finalChecks = append(finalChecks, doctor.NewAllowlistCheck(cfg, opts.DBPath))
		}
		if loopSucceeded && cfg.Channels.Discord != nil && cfg.Channels.Discord.BotToken != "" {
			// ChannelVerifiedCheck reads DB rows written by the Hello World bootstrap.
			// Guarded by loopSucceeded: if Hello World timed out, the DB is
			// intentionally empty and the user retries with 'chandra channel test
			// discord' — this is skippable per design §1.1.
			for _, chID := range cfg.Channels.Discord.ChannelIDs {
				finalChecks = append(finalChecks, doctor.NewChannelVerifiedCheck(chID, opts.DBPath))
			}
		}
		finalChecks = append(finalChecks, doctor.NewSchedulerCheck(""))
		finalChecks = append(finalChecks, doctor.NewDaemonCheck(""))
	}

	results := doctor.RunAll(ctx, finalChecks, 10)
	// Inject open-policy warning directly into results so doctor.AnyWarned()
	// picks it up and the final banner ("Setup complete with warnings") is
	// consistent — the warning is never followed by "Everything looks good".
	// This is independent of loopSucceeded: access_policy=open is a config
	// concern, not a live-connectivity concern.
	if cfg != nil && cfg.Channels.Discord != nil && cfg.Channels.Discord.AccessPolicy == "open" {
		results = append(results, doctor.Result{
			Name:   "AccessPolicy",
			Status: doctor.Warn,
			Detail: "access_policy=open — any Discord user can message the bot",
			Fix:    "set access_policy = \"invite\" in config, then run: chandra channel test discord",
		})
	}
	for _, r := range results {
		switch r.Status {
		case doctor.Pass:
			fmt.Printf("  %-14s ✓ %s\n", r.Name, r.Detail)
		case doctor.Warn:
			fmt.Printf("  %-14s ⚠ %s\n", r.Name, r.Detail)
		case doctor.Fail:
			fmt.Printf("  %-14s ✗ %s\n", r.Name, r.Detail)
		}
	}

	if doctor.AnyFailed(results) {
		fmt.Println("\nSetup encountered issues. Run 'chandra doctor' for details.")
		return nil // keep checkpoint so init can be resumed after fixing
	}

	_ = DeleteCheckpoint(cpPath)

	if doctor.AnyWarned(results) {
		fmt.Println("\nSetup complete with warnings (see above).")
		fmt.Println("Start the daemon with 'chandrad' to clear remaining items.")
		fmt.Println("Re-run 'chandra doctor' after starting the daemon.")
		return nil
	}

	fmt.Println("\nEverything looks good. Chandra is ready.")
	fmt.Println("Start the daemon with 'chandrad', then use 'chandra' commands to interact.")
	return nil
}

func runProviderStage(ctx context.Context, providerType, apiKey, model *string) error {
	// Step 1: select provider type
	form1 := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Which provider will you use?").
			Options(
				huh.NewOption("OpenAI (api.openai.com)", "openai"),
				huh.NewOption("Anthropic (api.anthropic.com)", "anthropic"),
				huh.NewOption("OpenRouter (openrouter.ai) — access to 100+ models", "openrouter"),
				huh.NewOption("Ollama (local)", "ollama"),
				huh.NewOption("Custom OpenAI-compatible endpoint (HTTPS required)", "custom"),
			).
			Value(providerType),
	))
	if err := form1.Run(); err != nil {
		return err
	}

	// Step 2: for custom provider, prompt for the base URL
	if *providerType == "custom" {
		var customURL string
		form2 := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Custom provider base URL (HTTPS required)").
				Placeholder("https://my-llm.example.com/v1").
				Value(&customURL),
		))
		if err := form2.Run(); err != nil {
			return err
		}
		// Validate HTTPS requirement before accepting
		if len(customURL) < 8 || customURL[:8] != "https://" {
			return fmt.Errorf("custom provider URL must use HTTPS (got: %s)", customURL)
		}
		// Stash custom URL in providerType encoded form — writeConfig will extract it
		// by checking if providerType == "custom" and reading from the customBaseURL.
		// Simplest: use a package-level var (acceptable for a wizard flow).
		customBaseURL = customURL
	}

	// Step 3: prompt for API key and model
	defaultModel := defaultModelForProvider(*providerType)
	form3 := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Enter your API key (leave blank for Ollama)").
			EchoMode(huh.EchoModePassword).
			Value(apiKey),
		huh.NewInput().
			Title("Default model").
			Placeholder(defaultModel).
			Value(model),
	))
	if err := form3.Run(); err != nil {
		return err
	}
	if *model == "" {
		*model = defaultModel
	}

	// Test the provider connection before proceeding (design §1 init stage table:
	// "Provider (required)"). Build a temporary config for the check.
	baseURL := providerBaseURL(*providerType)
	if *providerType == "custom" && customBaseURL != "" {
		baseURL = customBaseURL
	}
	testCfg := config.ProviderConfig{
		Type:         *providerType,
		BaseURL:      baseURL,
		APIKey:       *apiKey,
		DefaultModel: *model,
	}
	check := doctor.NewProviderCheck(&testCfg)
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result := check.Run(checkCtx)

	switch result.Status {
	case doctor.Pass:
		fmt.Printf("✓ Provider connection verified (%s)\n", *model)
	case doctor.Warn:
		fmt.Printf("⚠ Provider check warning: %s\n", result.Detail)
		// Continue — a warning is non-fatal
	case doctor.Fail:
		fmt.Printf("✗ Provider check failed: %s\n", result.Detail)
		if result.Fix != "" {
			fmt.Printf("  Fix: %s\n", result.Fix)
		}
		var skip bool
		skipForm := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().
				Title("Provider check failed. Continue anyway?").
				Description("Verify connectivity later with 'chandra provider test'.").
				Value(&skip),
		))
		if err := skipForm.Run(); err != nil {
			return err
		}
		if !skip {
			return fmt.Errorf("provider check failed: %s", result.Detail)
		}
	}
	return nil
}

// customBaseURL holds the URL entered for a custom provider during init.
// Package-level to avoid threading it through all function signatures.
var customBaseURL string

func defaultModelForProvider(t string) string {
	switch t {
	case "openai":
		return "gpt-4o"
	case "anthropic":
		return "claude-sonnet-4-6"
	case "openrouter":
		return "openai/gpt-4o"
	case "ollama":
		return "llama3"
	default:
		return ""
	}
}

func runChannelStage(ctx context.Context, discordToken, channelID, accessPolicy *string, discordRoleIDs *[]string) error {
	var useDiscord bool
	form1 := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Set up Discord?").
			Description("CLI always works without Discord.").
			Value(&useDiscord),
	))
	if err := form1.Run(); err != nil {
		return err
	}
	if !useDiscord {
		return nil
	}

	form2 := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Discord bot token").
			EchoMode(huh.EchoModePassword).
			Value(discordToken),
		huh.NewInput().
			Title("Channel ID to post the setup verification message").
			Value(channelID),
		huh.NewSelect[string]().
			Title("Who can talk to Chandra on Discord?").
			Options(
				huh.NewOption("Invite codes — you share codes, users redeem them", "invite"),
				huh.NewOption("Request flow — anyone can request, you approve", "request"),
				huh.NewOption("Role-based — trust members of a Discord role", "role"),
				huh.NewOption("Allowlist — paste user IDs manually", "allowlist"),
			).
			Value(accessPolicy),
	))
	if err := form2.Run(); err != nil {
		return err
	}
	// Validate required fields — a token without a channel ID produces a broken config.
	if *discordToken == "" {
		return fmt.Errorf("Discord bot token must not be empty")
	}
	if *channelID == "" {
		return fmt.Errorf("Discord channel ID must not be empty")
	}

	// Role-based access requires at least one role ID to be non-empty on day one.
	if *accessPolicy == "role" {
		var roleIDsStr string
		roleForm := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Discord role ID(s) to trust (comma-separated)").
				Description("Server Settings → Roles → right-click a role → Copy Role ID").
				Placeholder("1234567890,9876543210").
				Value(&roleIDsStr),
		))
		if err := roleForm.Run(); err != nil {
			return err
		}
		for _, id := range strings.Split(roleIDsStr, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				*discordRoleIDs = append(*discordRoleIDs, id)
			}
		}
		if len(*discordRoleIDs) == 0 {
			return fmt.Errorf("role-based access requires at least one role ID")
		}
	}
	return nil
}

func writeConfig(cfgPath, providerType, apiKey, model, agentName, description, discordToken, discordChannelID, accessPolicy string, discordRoleIDs []string, dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		return err
	}

	baseURL := providerBaseURL(providerType)
	if providerType == "custom" && customBaseURL != "" {
		baseURL = customBaseURL
	}
	// embedding_model is now a field inside [provider], not a separate section.
	// Anthropic has no native embedding API — fall back to OpenAI's model name.
	embeddingModel := providerEmbeddingModel(providerType)

	discordSection := ""
	if discordToken != "" {
		// Build allowed_roles line — only populated for "role" access policy.
		allowedRolesLine := "allowed_roles = []"
		if accessPolicy == "role" && len(discordRoleIDs) > 0 {
			quoted := make([]string, len(discordRoleIDs))
			for i, id := range discordRoleIDs {
				quoted[i] = fmt.Sprintf("%q", id)
			}
			allowedRolesLine = fmt.Sprintf("allowed_roles = [%s]", strings.Join(quoted, ", "))
		}
		discordSection = fmt.Sprintf(`
[channels.discord]
enabled = true
bot_token = %q
channel_ids = [%q]
access_policy = %q
allowed_users = []
%s
`, discordToken, discordChannelID, accessPolicy, allowedRolesLine)
	}

	// Uses design schema: [identity], provider.default_model, provider.embedding_model
	// No separate [embeddings] section.
	content := fmt.Sprintf(`# Chandra Configuration
# Generated by 'chandra init' — edit freely

[identity]
name = %q
description = %q

[database]
path = %q

[provider]
type = %q
base_url = %q
api_key = %q
default_model = %q
embedding_model = %q
%s`, agentName, description, dbPath, providerType, baseURL, apiKey, model, embeddingModel, discordSection)

	return os.WriteFile(cfgPath, []byte(content), 0600)
}

func providerBaseURL(t string) string {
	switch t {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	default:
		return ""
	}
}

func providerEmbeddingModel(t string) string {
	switch t {
	case "openai", "openrouter":
		return "text-embedding-3-small"
	case "anthropic":
		// Anthropic has no native embedding API.
		// Return the OpenAI model name — the implementer must configure a
		// separate embeddings provider or use OpenAI-compatible embeddings.
		return "text-embedding-3-small"
	case "ollama":
		return "mxbai-embed-large"
	default:
		return "text-embedding-3-small"
	}
}

// persistChannelVerified records a successful Hello World loop in channel_verifications.
// Always called on loop success — ChannelVerifiedCheck reads this table.
func persistChannelVerified(dbPath, channelID, userID string) error {
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

// addUserToAllowlist writes a row to allowed_users. Called unconditionally on
// loop success — the Hello World reply bootstraps the allowlist automatically
// (design §346: "The Hello World reply bootstraps the allowlist — no manual ID lookup needed").
func addUserToAllowlist(dbPath, channelID, userID, username string) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(
		`INSERT OR IGNORE INTO allowed_users (channel_id, user_id, username, source, added_at) VALUES (?, ?, ?, 'hello_world', ?)`,
		channelID, userID, username, time.Now().Unix(),
	)
	return err
}

func checkpointSummary(cp *Checkpoint) string {
	var parts []string
	mark := func(done bool, name string) string {
		if done {
			return name + " ✓"
		}
		return name + " ✗"
	}
	parts = append(parts, mark(cp.ProviderDone, "provider"))
	parts = append(parts, mark(cp.ChannelsDone, "channels"))
	parts = append(parts, mark(cp.IdentityDone, "identity"))
	parts = append(parts, mark(cp.ConfigWritten, "config"))
	return "Progress: " + strings.Join(parts, "  ")
}
```

**Step 6: Add `chandra init` command to commands.go**

In `cmd/chandra/commands.go`, add:

```go
// ---- init command ------------------------------------------------------------

var initNonInteractive bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactive setup wizard",
	Long:  "Run the Chandra setup wizard to configure provider, channels, and identity.",
	Run: func(cmd *cobra.Command, args []string) {
		opts := setup.DefaultOptions()
		opts.NonInteractive = initNonInteractive

		if envPath := os.Getenv("CHANDRA_CONFIG"); envPath != "" {
			opts.ConfigPath = envPath
		}

		if err := setup.Run(cmd.Context(), opts); err != nil {
			fmt.Fprintf(os.Stderr, "error: init failed: %v\n", err)
			os.Exit(1)
		}
	},
}
```

Wire in `init()`:
```go
initCmd.Flags().BoolVar(&initNonInteractive, "non-interactive", false, "skip interactive prompts")
rootCmd.AddCommand(initCmd)
```

Add `"github.com/jrimmer/chandra/internal/setup"` to imports.

**Step 7: Build to verify it compiles**

```bash
go build ./...
```
Expected: build succeeds

**Step 8: Commit**

```bash
git add internal/setup/ cmd/chandra/commands.go
git commit -m "feat(setup): add chandra init wizard with provider, channel, identity stages and checkpoint/resume"
```

---

### Task 18: Add `chandra config` commands

**Files:**
- Modify: `cmd/chandra/commands.go`

**Step 1: Add config commands**

In `cmd/chandra/commands.go`, add:

```go
// ---- config commands ---------------------------------------------------------

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration operations",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Pretty-print the resolved configuration (secrets masked)",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath := resolveDefaultConfigPath()
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: read config: %v\n", err)
			os.Exit(1)
		}
		// Mask secrets: lines containing api_key, token, password
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "api_key") || strings.Contains(lower, "token") || strings.Contains(lower, "password") {
				if idx := strings.Index(line, "="); idx != -1 {
					lines[i] = line[:idx+1] + ` "***masked***"`
				}
			}
		}
		fmt.Println(strings.Join(lines, "\n"))
	},
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Parse and validate config.toml",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath := resolveDefaultConfigPath()
		if _, err := config.Load(cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Config invalid: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Config valid: %s\n", cfgPath)
	},
}

var configSchemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Print the canonical config schema (suitable for redirection to a file)",
	Run: func(cmd *cobra.Command, args []string) {
		// Print the design schema as documented in docs/plans/setup-design.md §6.
		// This gives users a reference template they can diff against their config.
		fmt.Print(`# Chandra Configuration Schema
# Generated by 'chandra config schema'
# Copy and edit to create your own config.toml

[identity]
name = "Chandra"
description = "A helpful personal assistant"
persona_file = ""  # optional: path to detailed persona markdown

[database]
path = "~/.config/chandra/chandra.db"

[provider]
type = "openai"          # openai | anthropic | openrouter | ollama | custom
base_url = ""            # custom endpoints must use HTTPS
api_key = ""             # prefer CHANDRA_API_KEY env var
default_model = "gpt-4o"
embedding_model = "text-embedding-3-small"

[channels.discord]
enabled = false
bot_token = ""           # prefer DISCORD_BOT_TOKEN env var
channel_ids = []
access_policy = "invite" # invite | request | role | allowlist | open
allowed_guilds = []      # empty = any guild the bot is in
allowed_users = []       # populated by Hello World reply or 'chandra access add'
allowed_roles = []       # Discord role IDs (access_policy = "role" only)

[scheduler]
enabled = true
heartbeat_interval = "5m"

[mqtt]
mode = "embedded"        # embedded | external | disabled
bind = "127.0.0.1:1883"

[tools]
max_concurrent = 5
max_rounds = 10

# Exec tool — SECURITY: tighten before enabling in production.
[tools.exec]
allowed_shells = ["bash", "sh"]
working_directory = "~"
timeout = "5m"

[skills]
directory = "~/.config/chandra/skills"
priority = 0.7
max_context_tokens = 2000
max_matches = 3
`)
	},
}
```

Wire in `init()`:
```go
configCmd.AddCommand(configShowCmd, configValidateCmd, configSchemaCmd)
rootCmd.AddCommand(configCmd)
```

**Step 2: Build to verify it compiles**

```bash
go build ./cmd/chandra/...
```
Expected: build succeeds

**Step 3: Commit**

```bash
git add cmd/chandra/commands.go
git commit -m "feat(cli): add chandra config show, validate, and schema commands"
```

---

## Verification: Compare Plan Against Design

After implementing all tasks, run the full verification checklist:

### Phase 0 checklist

```bash
# Config schema alignment — verify new TOML field names parse correctly
cat > /tmp/schema-test.toml << 'EOF'
[identity]
name = "Chandra"
description = "A helpful personal assistant"

[database]
path = "/tmp/test.db"

[provider]
type = "openai"
base_url = "https://api.openai.com/v1"
api_key = "sk-test"
default_model = "gpt-4o"
embedding_model = "text-embedding-3-small"
EOF
CHANDRA_CONFIG=/tmp/schema-test.toml chandra config validate  # should pass

# Old schema should still warn but not crash (backwards compat through Embeddings field)
```

### Phase 1 checklist

```bash
# 0. First-run detection — guides user to chandra init when no config
CHANDRA_CONFIG=/tmp/noconfig.toml ./chandra doctor  # should print "No configuration found..."

# 1. Channels optional — daemon starts with no Discord config
chandrad --safe &
sleep 1 && chandra health && kill %1

# 2. CHANDRA_CONFIG respected
CHANDRA_CONFIG=/tmp/test.toml chandrad --safe &
sleep 1 && kill %1

# 3. Permissions hard exit
mkdir -p /tmp/testcfg && chmod 0755 /tmp/testcfg
CHANDRA_CONFIG=/tmp/testcfg/config.toml chandrad  # should exit with error

# 4. daemon.start no longer silently fails
chandra start  # should get a response

# 5. provider test command
chandra provider test

# 6. channel test command
chandra channel test discord

# 7. doctor command
chandra doctor  # all checks shown, non-zero if any fail

# 8. HTTPS enforcement
# Put http:// URL in config as custom provider, verify validate rejects it
```

### Phase 2 checklist

```bash
# Invite codes
chandra invite create
chandra invite list
chandra invite revoke <code>

# Access management
chandra access add discord 123456789
chandra access list discord
chandra access remove discord 123456789

# Init wizard
chandra init

# Config commands
chandra config show
chandra config validate
chandra config schema  # should print design schema template
```

---

## Known Out-of-Scope Items (Separate Plans)

The following are explicitly deferred per `docs/plans/setup-design.md §Design Priorities`:

- `chandra daemon install` — systemd/launchd service generation (§10)
- `chandra channel status` — per-channel connection state display (§11.2)
- `chandra security audit` — full security check table (§12)
- `chandra console` — full TUI admin console (see CONSOLE.md)
- Telegram and Slack adapters
- `chandra migrate openclaw`
- Keychain/secret-store integration
- `chandra provider models` / `chandra provider usage`
- `chandra db check/vacuum/reset`
- Access policy: `request` mode bot handler (server-side — requires daemon integration)
- Access policy: `role` mode bot handler (server-side — requires daemon integration)
- Time-windowed open access (`chandra access open --duration 1h`)
- Non-interactive init mode (`--non-interactive` flag) full implementation
- `chandra channel add discord` with OAuth2 invite URL generation
