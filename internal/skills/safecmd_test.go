package skills

import "testing"

func TestParseCommandSafe_SimpleCommand(t *testing.T) {
	parts, err := ParseCommandSafe("gh pr list --state open")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 5 || parts[0] != "gh" {
		t.Errorf("unexpected parts (expected 5): %v", parts)
	}
}

func TestParseCommandSafe_RejectsShellOperators(t *testing.T) {
	dangerous := []string{
		"gh pr list; cat /etc/passwd",
		"gh pr list && rm -rf /",
		"gh pr list | grep foo",
		"echo $(whoami)",
		"gh pr list > /tmp/out",
		"gh pr list `id`",
	}
	for _, cmd := range dangerous {
		t.Run(cmd, func(t *testing.T) {
			_, err := ParseCommandSafe(cmd)
			if err == nil {
				t.Errorf("expected error for dangerous command: %q", cmd)
			}
		})
	}
}

func TestParseCommandSafe_QuotedStrings(t *testing.T) {
	parts, err := ParseCommandSafe(`echo "hello world"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 2 {
		t.Errorf("expected 2 parts, got %d: %v", len(parts), parts)
	}
}

func TestParseCommandSafe_EmptyCommand(t *testing.T) {
	_, err := ParseCommandSafe("")
	if err == nil {
		t.Error("expected error for empty command")
	}
}
