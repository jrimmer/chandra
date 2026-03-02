package skills

import (
	"strings"
	"testing"
)

func TestSanitizeForLog_RedactsSecrets(t *testing.T) {
	command := "GH_TOKEN=ghp_abc123 gh pr list"
	result := SanitizeForLog(command, []string{"GH_TOKEN"})
	if strings.Contains(result, "ghp_abc123") {
		t.Errorf("expected secret to be redacted, got: %q", result)
	}
	if !strings.Contains(result, "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker, got: %q", result)
	}
}

func TestSanitizeForLog_NoSecrets(t *testing.T) {
	command := "gh pr list --state open"
	result := SanitizeForLog(command, nil)
	if result != command {
		t.Errorf("expected unchanged command, got: %q", result)
	}
}

func TestSanitizeForLog_MultipleSecrets(t *testing.T) {
	command := "API_KEY=sk-abc123 TOKEN=ghp_xyz gh api test"
	result := SanitizeForLog(command, []string{"API_KEY", "TOKEN"})
	if strings.Contains(result, "sk-abc123") {
		t.Errorf("expected API_KEY redacted, got: %q", result)
	}
	if strings.Contains(result, "ghp_xyz") {
		t.Errorf("expected TOKEN redacted, got: %q", result)
	}
}

func TestSanitizeForLog_PatternBased(t *testing.T) {
	command := "curl -H 'Authorization: Bearer sk-ant-abc123' https://api.example.com"
	result := SanitizeForLog(command, nil)
	if strings.Contains(result, "sk-ant-abc123") {
		t.Errorf("expected API key pattern redacted, got: %q", result)
	}
}

func TestBuildExecEnv(t *testing.T) {
	secrets := map[string]string{
		"GH_TOKEN": "ghp_abc123",
		"API_KEY":  "sk-test",
	}
	env := BuildExecEnv([]string{"GH_TOKEN", "API_KEY"}, secrets)

	found := make(map[string]bool)
	for _, e := range env {
		if strings.HasPrefix(e, "GH_TOKEN=") {
			found["GH_TOKEN"] = true
		}
		if strings.HasPrefix(e, "API_KEY=") {
			found["API_KEY"] = true
		}
	}
	if !found["GH_TOKEN"] {
		t.Error("expected GH_TOKEN in env")
	}
	if !found["API_KEY"] {
		t.Error("expected API_KEY in env")
	}
}
