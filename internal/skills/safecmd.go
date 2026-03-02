package skills

import (
	"errors"
	"strings"
)

// dangerousTokens contains shell operators that should never appear in
// untrusted skill commands.
var dangerousTokens = []string{
	";", "&&", "||", "|", ">", ">>", "<", "<<",
	"$(", "`",
}

// ParseCommandSafe splits a command into parts and rejects commands containing
// shell operators. This provides defense-in-depth for untrusted skill commands.
func ParseCommandSafe(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, errors.New("safecmd: empty command")
	}

	// Check for dangerous shell operators before parsing.
	for _, tok := range dangerousTokens {
		if strings.Contains(command, tok) {
			return nil, errors.New("safecmd: shell operator " + tok + " not allowed in untrusted commands")
		}
	}

	// Simple shell-like splitting that respects quoted strings.
	parts, err := splitCommand(command)
	if err != nil {
		return nil, err
	}

	if len(parts) == 0 {
		return nil, errors.New("safecmd: no command tokens found")
	}

	return parts, nil
}

// splitCommand performs simple shell-like word splitting respecting double
// and single quotes. It does not support escape sequences beyond basic quoting.
func splitCommand(s string) ([]string, error) {
	var parts []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == ' ' && !inSingle && !inDouble:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}

	if inSingle || inDouble {
		return nil, errors.New("safecmd: unclosed quote")
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts, nil
}
