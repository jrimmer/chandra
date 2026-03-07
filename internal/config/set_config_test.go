package config_test

import (
	"testing"

	"github.com/jrimmer/chandra/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsHotKey verifies hot/cold classification for all settable keys.
func TestIsHotKey(t *testing.T) {
	hot, cold := config.SettableKeys()
	for _, k := range hot {
		assert.True(t, config.IsHotKey(k), "expected %q to be hot", k)
	}
	for _, k := range cold {
		assert.False(t, config.IsHotKey(k), "expected %q to be cold", k)
	}
}

// TestSettableKeys_NonEmpty verifies both hot and cold lists are populated.
func TestSettableKeys_NonEmpty(t *testing.T) {
	hot, cold := config.SettableKeys()
	require.NotEmpty(t, hot, "should have hot keys")
	require.NotEmpty(t, cold, "should have cold keys")
}

// TestValidateValue_Integers tests integer field validation.
func TestValidateValue_Integers(t *testing.T) {
	cases := []struct {
		key   string
		value string
		valid bool
	}{
		// max_tool_rounds
		{"identity.max_tool_rounds", "10", true},
		{"identity.max_tool_rounds", "1", true},
		{"identity.max_tool_rounds", "50", true},
		{"identity.max_tool_rounds", "51", false},  // cap exceeded
		{"identity.max_tool_rounds", "0", false},   // must be positive
		{"identity.max_tool_rounds", "-1", false},
		{"identity.max_tool_rounds", "abc", false},
		// config_confirm_timeout_secs
		{"operator.config_confirm_timeout_secs", "30", true},
		{"operator.config_confirm_timeout_secs", "15", true},  // min
		{"operator.config_confirm_timeout_secs", "120", true}, // max
		{"operator.config_confirm_timeout_secs", "14", false}, // below min
		{"operator.config_confirm_timeout_secs", "121", false}, // above max
		{"operator.config_confirm_timeout_secs", "0", false},
	}
	for _, tc := range cases {
		err := config.ValidateValue(tc.key, tc.value)
		if tc.valid {
			assert.NoError(t, err, "key=%q value=%q should be valid", tc.key, tc.value)
		} else {
			assert.Error(t, err, "key=%q value=%q should be invalid", tc.key, tc.value)
		}
	}
}

// TestValidateValue_Booleans tests boolean field validation.
func TestValidateValue_Booleans(t *testing.T) {
	boolKeys := []string{
		"channels.discord.require_mention",
		"channels.discord.allow_bots",
		"channels.discord.reaction_status",
		"channels.discord.edit_in_place",
	}
	for _, k := range boolKeys {
		assert.NoError(t, config.ValidateValue(k, "true"), "key=%q: true should be valid", k)
		assert.NoError(t, config.ValidateValue(k, "false"), "key=%q: false should be valid", k)
		assert.NoError(t, config.ValidateValue(k, "True"), "key=%q: True should be valid", k)
		assert.NoError(t, config.ValidateValue(k, "False"), "key=%q: False should be valid", k)
		assert.Error(t, config.ValidateValue(k, "yes"), "key=%q: 'yes' should be invalid", k)
		assert.Error(t, config.ValidateValue(k, "1"), "key=%q: '1' should be invalid", k)
		assert.Error(t, config.ValidateValue(k, ""), "key=%q: empty should be invalid", k)
	}
}

// TestValidateValue_HTTPS enforces HTTPS for non-local provider URLs.
func TestValidateValue_HTTPS(t *testing.T) {
	assert.NoError(t, config.ValidateValue("provider.base_url", "https://api.openai.com/v1"))
	assert.NoError(t, config.ValidateValue("provider.base_url", "https://openrouter.ai/api/v1"))
	assert.NoError(t, config.ValidateValue("provider.base_url", "http://localhost:11434/v1"))   // local exempt
	assert.NoError(t, config.ValidateValue("provider.base_url", "http://127.0.0.1:8080/v1"))   // loopback exempt
	assert.Error(t, config.ValidateValue("provider.base_url", "http://api.openai.com/v1"))     // HTTP non-local blocked
	assert.Error(t, config.ValidateValue("provider.base_url", "http://openrouter.ai/api/v1"))  // HTTP non-local blocked
	assert.Error(t, config.ValidateValue("provider.base_url", "not-a-url"))
}

// TestValidateValue_ProviderType validates provider type enum.
func TestValidateValue_ProviderType(t *testing.T) {
	valid := []string{"openai", "anthropic", "openrouter", "ollama", "custom"}
	invalid := []string{"gemini", "gpt", "", "OPENAI"}
	for _, v := range valid {
		assert.NoError(t, config.ValidateValue("provider.type", v), "type=%q should be valid", v)
	}
	for _, v := range invalid {
		assert.Error(t, config.ValidateValue("provider.type", v), "type=%q should be invalid", v)
	}
}

// TestValidateValue_NonEmptyStrings validates that string fields reject empty values.
func TestValidateValue_NonEmptyStrings(t *testing.T) {
	strKeys := []string{
		"provider.api_key",
		"provider.default_model",
		"provider.embedding_model",
		"identity.name",
		"identity.description",
		"identity.persona_file",
	}
	for _, k := range strKeys {
		assert.NoError(t, config.ValidateValue(k, "some-value"), "key=%q: non-empty should be valid", k)
		assert.Error(t, config.ValidateValue(k, ""), "key=%q: empty should be invalid", k)
		assert.Error(t, config.ValidateValue(k, "   "), "key=%q: whitespace-only should be invalid", k)
	}
}

// TestValidateValue_UnknownKey rejects unknown keys.
func TestValidateValue_UnknownKey(t *testing.T) {
	assert.Error(t, config.ValidateValue("unknown.key", "value"))
	assert.Error(t, config.ValidateValue("identity.typo_field", "value"))
	assert.Error(t, config.ValidateValue("", "value"))
}

// TestValidateValue_ConfigConfirmTimeout_SelfModificationLoophole verifies that
// config_confirm_timeout_secs cannot be set above 120 even via set_config,
// closing the self-modification loophole where Chandra could disable her own
// safety timeout.
func TestValidateValue_ConfigConfirmTimeout_SelfModificationLoophole(t *testing.T) {
	// At the boundary
	assert.NoError(t, config.ValidateValue("operator.config_confirm_timeout_secs", "120"))
	// One over — must be rejected
	assert.Error(t, config.ValidateValue("operator.config_confirm_timeout_secs", "121"))
	assert.Error(t, config.ValidateValue("operator.config_confirm_timeout_secs", "3600"))
	assert.Error(t, config.ValidateValue("operator.config_confirm_timeout_secs", "99999"))
}
