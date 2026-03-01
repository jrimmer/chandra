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
