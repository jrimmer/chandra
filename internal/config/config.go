package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// AgentConfig holds agent identity and behaviour settings.
type AgentConfig struct {
	Name          string `toml:"name"`
	Persona       string `toml:"persona"`
	MaxToolRounds int    `toml:"max_tool_rounds"`
}

// ProviderConfig holds LLM chat provider settings.
type ProviderConfig struct {
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
	Model   string `toml:"model"`
	Type    string `toml:"type"` // openai | anthropic | ollama
}

// EmbeddingsConfig holds embedding provider settings.
type EmbeddingsConfig struct {
	BaseURL    string `toml:"base_url"`
	APIKey     string `toml:"api_key"`
	Model      string `toml:"model"`
	Dimensions int    `toml:"dimensions"`
}

// DatabaseConfig holds database path settings.
type DatabaseConfig struct {
	Path string `toml:"path"`
}

// BudgetConfig holds Context Budget Manager weighting settings.
type BudgetConfig struct {
	SemanticWeight    float64 `toml:"semantic_weight"`
	RecencyWeight     float64 `toml:"recency_weight"`
	ImportanceWeight  float64 `toml:"importance_weight"`
	RecencyDecayHours int     `toml:"recency_decay_hours"`
}

// SchedulerConfig holds scheduler tick settings.
type SchedulerConfig struct {
	TickInterval string `toml:"tick_interval"`
}

// MQTTConfig holds MQTT broker settings.
type MQTTConfig struct {
	Mode     string   `toml:"mode"`     // embedded | external | disabled
	Bind     string   `toml:"bind"`     // for embedded mode
	Broker   string   `toml:"broker"`   // for external mode
	Topics   []string `toml:"topics"`
	Username string   `toml:"username"`
	Password string   `toml:"password"`
	TLSCert  string   `toml:"tls_cert"`
	TLSKey   string   `toml:"tls_key"`
}

// DiscordConfig holds Discord channel settings.
type DiscordConfig struct {
	Token      string   `toml:"token"`
	ChannelIDs []string `toml:"channel_ids"`
}

// ChannelsConfig holds all inbound channel configurations.
type ChannelsConfig struct {
	Discord *DiscordConfig `toml:"discord"`
}

// ToolsConfig holds tool execution settings.
type ToolsConfig struct {
	ConfirmationTimeout  string   `toml:"confirmation_timeout"`
	DefaultToolTimeout   string   `toml:"default_tool_timeout"`
	ConfirmationPatterns []string `toml:"confirmation_patterns"`
}

// ActionLogConfig holds action log settings.
type ActionLogConfig struct {
	LLMSummaries bool `toml:"llm_summaries"`
}

// Config is the top-level configuration struct.
type Config struct {
	Agent      AgentConfig      `toml:"agent"`
	Provider   ProviderConfig   `toml:"provider"`
	Embeddings EmbeddingsConfig `toml:"embeddings"`
	Database   DatabaseConfig   `toml:"database"`
	Budget     BudgetConfig     `toml:"budget"`
	Scheduler  SchedulerConfig  `toml:"scheduler"`
	MQTT       MQTTConfig       `toml:"mqtt"`
	Channels   ChannelsConfig   `toml:"channels"`
	Tools      ToolsConfig      `toml:"tools"`
	ActionLog  ActionLogConfig  `toml:"actionlog"`
}

// Load reads and parses the TOML config file at path, performs env var
// interpolation on all string values, applies defaults, and validates
// required fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Env var interpolation: replace ${VAR} with environment values.
	expanded := os.Expand(string(data), func(key string) string {
		return os.Getenv(key)
	})

	var cfg Config
	if _, err := toml.Decode(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// applyDefaults sets default values for optional config fields.
func applyDefaults(cfg *Config) {
	if cfg.Agent.MaxToolRounds == 0 {
		cfg.Agent.MaxToolRounds = 5
	}
	if cfg.Scheduler.TickInterval == "" {
		cfg.Scheduler.TickInterval = "60s"
	}
	if cfg.MQTT.Mode == "" {
		cfg.MQTT.Mode = "embedded"
	}
	if cfg.MQTT.Bind == "" {
		cfg.MQTT.Bind = "127.0.0.1:1883"
	}
	if cfg.Budget.SemanticWeight == 0 {
		cfg.Budget.SemanticWeight = 0.4
	}
	if cfg.Budget.RecencyWeight == 0 {
		cfg.Budget.RecencyWeight = 0.3
	}
	if cfg.Budget.ImportanceWeight == 0 {
		cfg.Budget.ImportanceWeight = 0.3
	}
	if cfg.Budget.RecencyDecayHours == 0 {
		cfg.Budget.RecencyDecayHours = 168
	}
	if cfg.Tools.ConfirmationTimeout == "" {
		cfg.Tools.ConfirmationTimeout = "24h"
	}
	if cfg.Tools.DefaultToolTimeout == "" {
		cfg.Tools.DefaultToolTimeout = "30s"
	}
	if cfg.Embeddings.Dimensions == 0 {
		cfg.Embeddings.Dimensions = 1536
	}
}

// validate checks that all required config fields are present.
func validate(cfg *Config) error {
	var errs []string

	if cfg.Agent.Name == "" {
		errs = append(errs, "agent.name is required")
	}
	if cfg.Agent.Persona == "" {
		errs = append(errs, "agent.persona is required")
	}
	if cfg.Provider.BaseURL == "" {
		errs = append(errs, "provider.base_url is required")
	}
	if cfg.Provider.Model == "" {
		errs = append(errs, "provider.model is required")
	}
	if cfg.Provider.Type == "" {
		errs = append(errs, "provider.type is required")
	}
	if cfg.Embeddings.BaseURL == "" {
		errs = append(errs, "embeddings.base_url is required")
	}
	if cfg.Embeddings.Model == "" {
		errs = append(errs, "embeddings.model is required")
	}
	if cfg.Database.Path == "" {
		errs = append(errs, "database.path is required")
	}

	// At least one channel must be configured.
	if cfg.Channels.Discord == nil || cfg.Channels.Discord.Token == "" {
		errs = append(errs, "at least one channel must be configured (channels.discord)")
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}
