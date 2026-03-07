package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// hotKeys lists config keys that can be updated without a daemon restart.
// All other settable keys are "cold" and require the Windows-confirmation restart pattern.
// Keys use dot-notation matching the TOML path, e.g. "identity.max_tool_rounds".
var hotKeys = map[string]bool{
	"identity.max_tool_rounds":           true,
	"identity.persona_file":              true,
	"channels.discord.require_mention":   true,
	"channels.discord.allow_bots":        true,
	"channels.discord.reaction_status":   true,
	"channels.discord.edit_in_place":     true,
	"operator.config_confirm_timeout_secs": true,
}

// IsHotKey returns true if the given dotted config key can be changed
// without restarting the daemon.
func IsHotKey(key string) bool {
	return hotKeys[key]
}

// ValidateValue type-checks a proposed config value against the known field type
// and any additional constraints (HTTPS enforcement, integer bounds, etc.).
// Returns a non-nil error if the value is invalid.
func ValidateValue(key, value string) error {
	switch key {
	// ── integer fields ────────────────────────────────────────────────────────
	case "identity.max_tool_rounds",
		"operator.config_confirm_timeout_secs":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s: must be an integer, got %q", key, value)
		}
		if n <= 0 {
			return fmt.Errorf("%s: must be a positive integer, got %d", key, n)
		}
		if key == "operator.config_confirm_timeout_secs" && (n < 15 || n > 120) {
			return fmt.Errorf("%s: must be between 15 and 120 seconds, got %d", key, n)
		}
		if key == "identity.max_tool_rounds" && n > 50 {
			return fmt.Errorf("%s: capped at 50 rounds, got %d", key, n)
		}

	// ── boolean fields ────────────────────────────────────────────────────────
	case "channels.discord.require_mention",
		"channels.discord.allow_bots",
		"channels.discord.reaction_status",
		"channels.discord.edit_in_place":
		lower := strings.ToLower(value)
		if lower != "true" && lower != "false" {
			return fmt.Errorf("%s: must be true or false, got %q", key, value)
		}

	// ── string fields with HTTPS enforcement ──────────────────────────────────
	case "provider.base_url":
		if !strings.HasPrefix(value, "https://") {
			// Check if it's localhost/loopback (exempt from HTTPS).
			u, err := url.Parse(value)
			if err != nil {
				return fmt.Errorf("%s: invalid URL: %v", key, err)
			}
			h := u.Hostname()
			isLocal := h == "localhost" || strings.HasPrefix(h, "127.") || h == "::1"
			if !isLocal {
				return fmt.Errorf("%s: must use HTTPS for non-local endpoints", key)
			}
		}

	// ── string fields (non-empty required) ────────────────────────────────────
	case "provider.api_key",
		"provider.default_model",
		"provider.embedding_model",
		"identity.name",
		"identity.description",
		"identity.persona_file":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s: must not be empty", key)
		}

	// ── provider type ─────────────────────────────────────────────────────────
	case "provider.type":
		valid := map[string]bool{
			"openai": true, "anthropic": true, "openrouter": true,
			"ollama": true, "custom": true,
		}
		if !valid[value] {
			return fmt.Errorf("%s: must be one of openai, anthropic, openrouter, ollama, custom; got %q", key, value)
		}

	default:
		// Unknown keys are rejected — prevents typos from silently no-oping.
		return fmt.Errorf("unknown config key %q — use 'chandra config list' to see settable keys", key)
	}
	return nil
}

// SettableKeys returns all keys that can be changed via set_config, grouped
// by whether they are hot (no restart) or cold (restart required).
func SettableKeys() (hot, cold []string) {
	all := []string{
		"identity.name",
		"identity.description",
		"identity.persona_file",
		"identity.max_tool_rounds",
		"provider.type",
		"provider.base_url",
		"provider.api_key",
		"provider.default_model",
		"provider.embedding_model",
		"channels.discord.require_mention",
		"channels.discord.allow_bots",
		"channels.discord.reaction_status",
		"channels.discord.edit_in_place",
		"operator.config_confirm_timeout_secs",
	}
	for _, k := range all {
		if hotKeys[k] {
			hot = append(hot, k)
		} else {
			cold = append(cold, k)
		}
	}
	return
}
