package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_MinimalConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(`
[agent]
name = "Chandra"
persona = "Test persona"

[provider]
base_url = "http://localhost:11434"
api_key = "test"
model = "llama3"
type = "openai"

[embeddings]
base_url = "http://localhost:11434/v1"
api_key = "test"
model = "nomic-embed-text"

[database]
path = "/tmp/test-chandra.db"

[channels.discord]
token = "test-token"
channel_ids = ["123"]
`), 0600)
	require.NoError(t, err)

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "Chandra", cfg.Agent.Name)
	assert.Equal(t, "openai", cfg.Provider.Type)
	assert.Equal(t, 5, cfg.Agent.MaxToolRounds, "should default to 5")
	assert.Equal(t, "60s", cfg.Scheduler.TickInterval, "should default to 60s")
	assert.False(t, cfg.ActionLog.LLMSummaries, "should default to false")
}

func TestLoad_EnvVarInterpolation(t *testing.T) {
	t.Setenv("TEST_API_KEY", "secret-key-123")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(`
[agent]
name = "Chandra"
persona = "Test"

[provider]
base_url = "http://localhost"
api_key = "${TEST_API_KEY}"
model = "test"
type = "openai"

[embeddings]
base_url = "http://localhost"
api_key = "${TEST_API_KEY}"
model = "test"

[database]
path = "/tmp/test.db"

[channels.discord]
token = "test"
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
[agent]
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
	if cfg.Skills.Directory != "~/.config/chandra/skills" {
		t.Errorf("expected default skills directory, got %q", cfg.Skills.Directory)
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
	if cfg.Plans.AutoRollback != true {
		t.Error("expected plans.auto_rollback default true")
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
	if cfg.Plans.ParallelSteps != false {
		t.Error("expected parallel_steps default false")
	}
	// AutoRollback already exists and defaults to true — verify it still works
	if cfg.Plans.AutoRollback != true {
		t.Error("expected auto_rollback default true")
	}
}

func TestLoad_MissingChannel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte(`
[agent]
name = "Chandra"
persona = "Test"

[provider]
base_url = "http://localhost"
api_key = "test"
model = "test"
type = "openai"

[embeddings]
base_url = "http://localhost"
api_key = "test"
model = "test"

[database]
path = "/tmp/test.db"
`), 0600)
	require.NoError(t, err)

	_, err = Load(path)
	assert.Error(t, err, "should fail when no channel configured")
}
