package skills

import (
	"regexp"
	"strings"
)

// secretPatterns matches common secret/token formats for automatic redaction.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[a-zA-Z0-9_-]{10,}`),        // Anthropic/OpenAI keys
	regexp.MustCompile(`ghp_[a-zA-Z0-9]{10,}`),          // GitHub PATs
	regexp.MustCompile(`ghs_[a-zA-Z0-9]{10,}`),          // GitHub App tokens
	regexp.MustCompile(`github_pat_[a-zA-Z0-9_]{10,}`),  // GitHub fine-grained PATs
	regexp.MustCompile(`Bearer\s+[a-zA-Z0-9._-]{20,}`),  // Bearer tokens
	regexp.MustCompile(`[a-zA-Z0-9+/]{40,}={0,2}`),      // Base64-encoded keys (long)
}

// SanitizeForLog redacts secret values from a command string for safe logging.
// Named secrets in secretNames are redacted by matching KEY=VALUE patterns.
// Common secret patterns (API keys, tokens) are also automatically redacted.
func SanitizeForLog(command string, secretNames []string) string {
	result := command

	// Redact named secrets: KEY=VALUE patterns
	for _, name := range secretNames {
		pattern := regexp.MustCompile(name + `=\S+`)
		result = pattern.ReplaceAllString(result, name+"=[REDACTED]")
	}

	// Redact common secret patterns
	for _, p := range secretPatterns {
		result = p.ReplaceAllString(result, "[REDACTED]")
	}

	return result
}

// BuildExecEnv builds an environment variable slice for command execution,
// injecting secrets via env vars rather than command-line arguments.
func BuildExecEnv(secretNames []string, secrets map[string]string) []string {
	env := make([]string, 0, len(secretNames))
	for _, name := range secretNames {
		if val, ok := secrets[name]; ok {
			env = append(env, name+"="+val)
		}
	}
	return env
}

// ContainsSecret checks if a string contains any known secret patterns.
func ContainsSecret(s string) bool {
	for _, p := range secretPatterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

// ClassifyOutputKey returns true if the key name suggests ephemeral/secret content.
func ClassifyOutputKey(key string) bool {
	lower := strings.ToLower(key)
	secretKeywords := []string{"token", "password", "secret", "key", "credential", "auth", "api_key", "apikey"}
	for _, kw := range secretKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
