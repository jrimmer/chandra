package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestLoad_MinimalConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(`
[identity]
name = "Chandra"
description = "Test persona"

[provider]
base_url = "http://localhost:11434"
api_key = "test"
default_model = "llama3"
type = "openai"

[database]
path = "/tmp/test-chandra.db"

[channels.discord]
bot_token = "test-token"
channel_ids = ["123"]
`), 0600)
	require.NoError(t, err)

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "Chandra", cfg.Identity.Name)
	assert.Equal(t, "openai", cfg.Provider.Type)
	assert.Equal(t, 5, cfg.Identity.MaxToolRounds, "should default to 5")
	assert.Equal(t, "60s", cfg.Scheduler.TickInterval, "should default to 60s")
	assert.False(t, cfg.ActionLog.LLMSummaries, "should default to false")
}

func TestLoad_EnvVarInterpolation(t *testing.T) {
	t.Setenv("TEST_API_KEY", "secret-key-123")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(`
[identity]
name = "Chandra"
description = "Test"

[provider]
base_url = "http://localhost"
api_key = "${TEST_API_KEY}"
default_model = "test"
type = "openai"

[database]
path = "/tmp/test.db"

[channels.discord]
bot_token = "test"
channel_ids = ["123"]
`), 0600)
	require.NoError(t, err)

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "secret-key-123", cfg.Provider.APIKey)
}

func TestLoad_MissingRequiredFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte(`
[identity]
name = "Chandra"
`), 0600)
	require.NoError(t, err)

	_, err = Load(path)
	assert.Error(t, err, "should fail with missing required fields")
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.toml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read config")
}

func TestLoad_MalformedTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	err := os.WriteFile(path, []byte(`[broken`), 0600)
	require.NoError(t, err)
	_, err = Load(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse config")
}

func TestConfig_SkillsDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.Skills.Path != "~/.config/chandra/skills" {
		t.Errorf("expected default skills path, got %q", cfg.Skills.Path)
	}
	if cfg.Skills.Priority != 0.7 {
		t.Errorf("expected default priority 0.7, got %f", cfg.Skills.Priority)
	}
	if cfg.Skills.MaxContextTokens != 2000 {
		t.Errorf("expected default max_context_tokens 2000, got %d", cfg.Skills.MaxContextTokens)
	}
	if cfg.Skills.MaxMatches != 3 {
		t.Errorf("expected default max_matches 3, got %d", cfg.Skills.MaxMatches)
	}
	if cfg.Skills.AutoReload != true {
		t.Error("expected auto_reload default true")
	}
	if cfg.Skills.RequireValidation != false {
		t.Error("expected require_validation default false")
	}
	if cfg.Skills.Generator.MaxConcurrentGenerations != 1 {
		t.Errorf("expected default max_concurrent_generations 1, got %d", cfg.Skills.Generator.MaxConcurrentGenerations)
	}
	if cfg.Skills.Generator.GenerationTimeout != "5m" {
		t.Errorf("expected default generation_timeout 5m, got %q", cfg.Skills.Generator.GenerationTimeout)
	}
	if cfg.Skills.Generator.MaxPendingReview != 10 {
		t.Errorf("expected default max_pending_review 10, got %d", cfg.Skills.Generator.MaxPendingReview)
	}
	if cfg.Plans.AutoRollbackIdempotent != false {
		t.Error("expected plans.auto_rollback_idempotent default false")
	}
	if cfg.Plans.NotificationRetention != "168h" {
		t.Errorf("expected plans.notification_retention 168h, got %q", cfg.Plans.NotificationRetention)
	}
}

func TestConfig_PlannerDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.Planner.MaxSteps != 20 {
		t.Errorf("expected default max_steps 20, got %d", cfg.Planner.MaxSteps)
	}
	if cfg.Planner.CheckpointTimeout != "24h" {
		t.Errorf("expected default checkpoint_timeout 24h, got %q", cfg.Planner.CheckpointTimeout)
	}
	if cfg.Planner.AllowInfraCreation != true {
		t.Error("expected allow_infra_creation default true")
	}
	if cfg.Planner.AllowSoftwareInstall != true {
		t.Error("expected allow_software_install default true")
	}
}

func TestConfig_ExecutorDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.Executor.ParallelSteps != false {
		t.Error("expected parallel_steps default false")
	}
	if cfg.Executor.RollbackOnFailure != false {
		t.Error("expected rollback_on_failure default false")
	}
	if cfg.Executor.MaxConcurrentPlans != 2 {
		t.Errorf("expected max_concurrent_plans 2, got %d", cfg.Executor.MaxConcurrentPlans)
	}
	if cfg.Executor.MaxConcurrentSteps != 3 {
		t.Errorf("expected max_concurrent_steps 3, got %d", cfg.Executor.MaxConcurrentSteps)
	}
	if cfg.Executor.StepTimeout != "10m" {
		t.Errorf("expected step_timeout 10m, got %q", cfg.Executor.StepTimeout)
	}
}

func TestConfig_InfrastructureCacheTTL(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.Infrastructure.CacheTTL != "5m" {
		t.Errorf("expected default cache_ttl 5m, got %q", cfg.Infrastructure.CacheTTL)
	}
}

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

func TestLoad_NoChannelAllowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte(`
[identity]
name = "Chandra"
description = "Test"

[provider]
base_url = "http://localhost"
api_key = "test"
default_model = "test"
type = "openai"

[database]
path = "/tmp/test.db"
`), 0600)
	require.NoError(t, err)

	_, err = Load(path)
	assert.NoError(t, err, "should pass when no channel configured (CLI-only mode)")
}

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
