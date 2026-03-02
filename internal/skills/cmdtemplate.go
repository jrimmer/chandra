package skills

import "strings"

// ApprovedTemplate represents an approved command template for a skill.
type ApprovedTemplate struct {
	SkillName string
	Template  string
}

// ExtractCommandTemplate extracts a verb-aware command template from a full command.
// It identifies the verb prefix (binary + subcommands) and replaces all flags
// and arguments with a single wildcard.
//
// The algorithm collects non-flag tokens as the "verb prefix" until it encounters
// a token that looks like an argument (a number, path, URL, etc.) or a flag.
// Everything after the verb prefix becomes "*".
//
// Examples:
//   - "gh pr list --state open" → "gh pr list *"
//   - "docker ps -a" → "docker ps *"
//   - "kubectl get pods -n default" → "kubectl get pods *"
//   - "curl -s https://example.com" → "curl *"
func ExtractCommandTemplate(command string) string {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ""
	}

	var verb []string

	for _, p := range parts {
		// Flag means we've left the verb prefix
		if strings.HasPrefix(p, "-") {
			break
		}

		// Argument-like token (number, path, URL) also ends the verb prefix
		if isArgumentToken(p) && len(verb) > 0 {
			break
		}

		verb = append(verb, p)
	}

	return strings.Join(verb, " ") + " *"
}

// isArgumentToken returns true if the token looks like a command argument
// (number, path, URL, UUID) rather than a subcommand name.
func isArgumentToken(token string) bool {
	if len(token) == 0 {
		return false
	}
	// Starts with a digit — likely a number, port, or ID
	if token[0] >= '0' && token[0] <= '9' {
		return true
	}
	// Contains path separators, dots, or colons — likely a path, URL, or host:port
	if strings.ContainsAny(token, "/.:@=") {
		return true
	}
	return false
}

// HasApprovedTemplate checks if a command matches any approved template.
func HasApprovedTemplate(templates []ApprovedTemplate, command string) bool {
	template := ExtractCommandTemplate(command)
	for _, t := range templates {
		if t.Template == template {
			return true
		}
	}
	return false
}
