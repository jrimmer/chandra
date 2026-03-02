package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

var injectionPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"ignore_instructions", regexp.MustCompile(`(?i)ignore\s+(all\s+)?previous\s+(instructions|prompts?)`)},
	{"override_role", regexp.MustCompile(`(?i)(you\s+are\s+now|act\s+as|new\s+instructions)`)},
	{"system_prompt", regexp.MustCompile(`(?i)(system\s+prompt|disregard|override\s+instructions|bypass)`)},
	{"model_tokens", regexp.MustCompile(`<\|endoftext\|>|<\|im_start\|>|<\|im_end\|>`)},
	{"inst_tags", regexp.MustCompile(`\[INST\]|\[/INST\]|<<SYS>>|<</SYS>>|</s>`)},
	{"excessive_control_chars", regexp.MustCompile(`[\x00-\x08\x0e-\x1f]{3,}`)},
}

// SanitizeContent scans generated skill content for potential prompt injection patterns.
// Returns a list of flags (pattern names) that matched. Empty means clean.
func SanitizeContent(content string) []string {
	var flags []string
	lower := strings.ToLower(content)
	for _, p := range injectionPatterns {
		if p.pattern.MatchString(lower) || p.pattern.MatchString(content) {
			flags = append(flags, p.name)
		}
	}
	return flags
}

// WrapWithBoundary wraps generated skill content with SHA256-keyed delimiters
// to prevent forged boundary injection.
func WrapWithBoundary(content string) string {
	hash := sha256.Sum256([]byte(content))
	marker := hex.EncodeToString(hash[:8])
	return fmt.Sprintf("<<<SKILL_CONTENT:sha256:%s>>>\n%s\n<<<END_SKILL:sha256:%s>>>", marker, content, marker)
}
