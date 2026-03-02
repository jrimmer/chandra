package skills

import "testing"

func TestExtractCommandTemplate(t *testing.T) {
	tests := []struct {
		command  string
		expected string
	}{
		{"gh pr list --state open", "gh pr list *"},
		{"gh pr merge 123", "gh pr merge *"},
		{"docker ps -a", "docker ps *"},
		{"kubectl get pods -n default", "kubectl get pods *"},
		{"curl -s https://example.com", "curl *"},
		{"ls -la", "ls *"},
		{"git status", "git status *"},
		{"echo hello", "echo hello *"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			result := ExtractCommandTemplate(tt.command)
			if result != tt.expected {
				t.Errorf("ExtractCommandTemplate(%q) = %q, want %q", tt.command, result, tt.expected)
			}
		})
	}
}

func TestExtractCommandTemplate_Isolation(t *testing.T) {
	// "gh pr list" should NOT unlock "gh pr merge".
	list := ExtractCommandTemplate("gh pr list --state open")
	merge := ExtractCommandTemplate("gh pr merge 123")
	if list == merge {
		t.Errorf("list and merge should have different templates: %q vs %q", list, merge)
	}
}

func TestHasApprovedCommandTemplate(t *testing.T) {
	templates := []ApprovedTemplate{
		{SkillName: "github", Template: "gh pr list *"},
		{SkillName: "github", Template: "gh repo list *"},
	}

	if !HasApprovedTemplate(templates, "gh pr list --state open") {
		t.Error("expected approved for gh pr list variant")
	}
	if HasApprovedTemplate(templates, "gh pr merge 123") {
		t.Error("expected NOT approved for gh pr merge")
	}
}
