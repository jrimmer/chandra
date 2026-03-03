package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

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
// TOML section: [embeddings] -- still parsed but EmbeddingModel takes precedence.
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
	Enabled       bool     `toml:"enabled"`
	BotToken      string   `toml:"bot_token"`
	ChannelIDs    []string `toml:"channel_ids"`
	AccessPolicy  string   `toml:"access_policy"` // invite | request | role | allowlist | open
	AllowedUsers  []string `toml:"allowed_users"`
	AllowedGuilds []string `toml:"allowed_guilds"`
	AllowedRoles  []string `toml:"allowed_roles"`
}

// ChannelsConfig holds all inbound channel configurations.
type ChannelsConfig struct {
	Discord *DiscordConfig `toml:"discord"`
}

// ConfirmationRuleConfig holds a single confirmation rule from TOML config.
type ConfirmationRuleConfig struct {
	Pattern     string   `toml:"pattern"`
	Categories  []string `toml:"categories"`
	Description string   `toml:"description"`
}

// ToolsConfig holds tool execution settings.
type ToolsConfig struct {
	ConfirmationTimeout string                   `toml:"confirmation_timeout"`
	DefaultToolTimeout  string                   `toml:"default_tool_timeout"`
	ConfirmationRules   []ConfirmationRuleConfig `toml:"confirmation_rules"`
}

// ActionLogConfig holds action log settings.
type ActionLogConfig struct {
	LLMSummaries bool `toml:"llm_summaries"`
}

// SkillGeneratorConfig holds skill generation settings.
type SkillGeneratorConfig struct {
	MaxConcurrentGenerations int    `toml:"max_concurrent_generations"`
	GenerationTimeout        string `toml:"generation_timeout"`
	MaxPendingReview         int    `toml:"max_pending_review"`
}

// SkillsConfig holds skill loading and matching settings.
type SkillsConfig struct {
	Path              string               `toml:"path"`
	Priority          float64              `toml:"priority"`
	MaxContextTokens  int                  `toml:"max_context_tokens"`
	MaxMatches        int                  `toml:"max_matches"`
	RequireValidation bool                 `toml:"require_validation"`
	AutoReload        bool                 `toml:"auto_reload"`
	Generator         SkillGeneratorConfig `toml:"generator"`
}

// ExecutorConfig holds executor settings (maps to [executor] in TOML).
type ExecutorConfig struct {
	ParallelSteps      bool   `toml:"parallel_steps"`
	RollbackOnFailure  bool   `toml:"rollback_on_failure"`
	MaxConcurrentPlans int    `toml:"max_concurrent_plans"`
	MaxConcurrentSteps int    `toml:"max_concurrent_steps"`
	StepTimeout        string `toml:"step_timeout"`
}

// PlansConfig holds plan execution settings.
type PlansConfig struct {
	AutoRollbackIdempotent bool   `toml:"auto_rollback_idempotent"`
	NotificationRetention  string `toml:"notification_retention"`
}

// PlannerConfig holds planner settings.
type PlannerConfig struct {
	MaxSteps             int    `toml:"max_steps"`
	CheckpointTimeout    string `toml:"checkpoint_timeout"`
	AllowInfraCreation   bool   `toml:"allow_infra_creation"`
	AllowSoftwareInstall bool   `toml:"allow_software_install"`
}

// InfrastructureConfig holds infrastructure awareness settings.
type InfrastructureConfig struct {
	DiscoveryInterval  string `toml:"discovery_interval"`
	MaxConcurrentHosts int    `toml:"max_concurrent_hosts"`
	HostTimeout        string `toml:"host_timeout"`
	CacheTTL           string `toml:"cache_ttl"`
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

// Load reads and parses the TOML config file at path, performs env var
// interpolation on all string values, applies defaults, and validates
// required fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Env var interpolation: replace ${VAR} with environment values.
	var unresolved []string
	expanded := os.Expand(string(data), func(key string) string {
		val := os.Getenv(key)
		if val == "" {
			unresolved = append(unresolved, key)
		}
		return val
	})
	if len(unresolved) > 0 {
		slog.Warn("config: unresolved environment variables", "vars", unresolved)
	}

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
	if cfg.Identity.MaxToolRounds == 0 {
		cfg.Identity.MaxToolRounds = 5
	}
	if cfg.Identity.Name == "" {
		cfg.Identity.Name = "Chandra"
	}
	if cfg.Identity.Description == "" {
		cfg.Identity.Description = "A helpful personal assistant"
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
	// Note: zero values here mean "not set in config" since TOML unmarshals
	// missing fields and explicit zero fields the same way. A user cannot
	// intentionally set these to 0.0; use a small positive value instead.
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
	if cfg.Provider.EmbeddingModel == "" {
		cfg.Provider.EmbeddingModel = "text-embedding-3-small"
	}
	if cfg.Provider.DefaultModel == "" && cfg.Provider.Type == "openai" {
		cfg.Provider.DefaultModel = "gpt-4o"
	}
	if cfg.Channels.Discord != nil && cfg.Channels.Discord.AccessPolicy == "" {
		cfg.Channels.Discord.AccessPolicy = "invite"
	}
	if cfg.Skills.Path == "" {
		cfg.Skills.Path = "~/.config/chandra/skills"
	}
	if cfg.Skills.Priority == 0 {
		cfg.Skills.Priority = 0.7
	}
	if cfg.Skills.MaxContextTokens == 0 {
		cfg.Skills.MaxContextTokens = 2000
	}
	if cfg.Skills.MaxMatches == 0 {
		cfg.Skills.MaxMatches = 3
	}
	if !cfg.Skills.AutoReload {
		cfg.Skills.AutoReload = true
	}
	if cfg.Skills.Generator.MaxConcurrentGenerations == 0 {
		cfg.Skills.Generator.MaxConcurrentGenerations = 1
	}
	if cfg.Skills.Generator.GenerationTimeout == "" {
		cfg.Skills.Generator.GenerationTimeout = "5m"
	}
	if cfg.Skills.Generator.MaxPendingReview == 0 {
		cfg.Skills.Generator.MaxPendingReview = 10
	}
	if cfg.Planner.MaxSteps == 0 {
		cfg.Planner.MaxSteps = 20
	}
	if cfg.Planner.CheckpointTimeout == "" {
		cfg.Planner.CheckpointTimeout = "24h"
	}
	if !cfg.Planner.AllowInfraCreation {
		cfg.Planner.AllowInfraCreation = true
	}
	if !cfg.Planner.AllowSoftwareInstall {
		cfg.Planner.AllowSoftwareInstall = true
	}
	if cfg.Executor.MaxConcurrentPlans == 0 {
		cfg.Executor.MaxConcurrentPlans = 2
	}
	if cfg.Executor.MaxConcurrentSteps == 0 {
		cfg.Executor.MaxConcurrentSteps = 3
	}
	if cfg.Executor.StepTimeout == "" {
		cfg.Executor.StepTimeout = "10m"
	}
	if cfg.Plans.NotificationRetention == "" {
		cfg.Plans.NotificationRetention = "168h"
	}
	if cfg.Infrastructure.HostTimeout == "" {
		cfg.Infrastructure.HostTimeout = "30s"
	}
	if cfg.Infrastructure.CacheTTL == "" {
		cfg.Infrastructure.CacheTTL = "5m"
	}
}

// validate checks that all required config fields are present.
func validate(cfg *Config) error {
	var errs []string

	// identity.name is optional -- defaults applied above
	if cfg.Provider.DefaultModel == "" {
		errs = append(errs, "provider.default_model is required")
	}
	if cfg.Provider.BaseURL == "" {
		errs = append(errs, "provider.base_url is required")
	}
	switch cfg.Provider.Type {
	case "openai", "anthropic", "openrouter", "ollama", "custom":
		// valid
	case "":
		errs = append(errs, "provider.type is required")
	default:
		errs = append(errs, fmt.Sprintf("provider.type %q is not valid (openai, anthropic, openrouter, ollama, custom)", cfg.Provider.Type))
	}
	if cfg.Database.Path == "" {
		errs = append(errs, "database.path is required")
	}

	// At least one channel must be configured.
	if cfg.Channels.Discord == nil || cfg.Channels.Discord.BotToken == "" {
		errs = append(errs, "at least one channel must be configured (channels.discord)")
	}

	switch cfg.MQTT.Mode {
	case "embedded", "external", "disabled":
		// valid
	case "":
		// mode was defaulted to "embedded", so this shouldn't happen, but handle it
	default:
		errs = append(errs, fmt.Sprintf("mqtt.mode %q is not valid (embedded, external, disabled)", cfg.MQTT.Mode))
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}
