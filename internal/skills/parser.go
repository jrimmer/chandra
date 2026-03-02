package skills

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// frontmatter is the YAML header of a SKILL.md file.
type frontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	Version       string            `yaml:"version"`
	Triggers      []string          `yaml:"triggers"`
	Requires      SkillRequirements `yaml:"requires"`
	Config        map[string]any    `yaml:"config"`
	DependsOn     []string          `yaml:"depends_on"`
	RequiresShell bool              `yaml:"requires_shell"`
	Generated     *GeneratedMeta    `yaml:"generated"`
}

// ParseSkillMD parses a SKILL.md file into a Skill.
// The file must contain YAML frontmatter delimited by "---" lines,
// followed by the markdown body.
func ParseSkillMD(data []byte, path string) (Skill, error) {
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return Skill{}, fmt.Errorf("parse %s: %w", path, err)
	}

	var meta frontmatter
	if err := yaml.Unmarshal(fm, &meta); err != nil {
		return Skill{}, fmt.Errorf("parse %s frontmatter: %w", path, err)
	}

	if meta.Name == "" {
		return Skill{}, fmt.Errorf("parse %s: name is required", path)
	}

	summary := extractSummary(string(body))

	return Skill{
		Name:          meta.Name,
		Description:   meta.Description,
		Summary:       summary,
		Version:       meta.Version,
		Triggers:      meta.Triggers,
		Requires:      meta.Requires,
		Config:        meta.Config,
		Content:       string(body),
		Path:          path,
		DependsOn:     meta.DependsOn,
		RequiresShell: meta.RequiresShell,
		Generated:     meta.Generated,
	}, nil
}

// extractSummary returns the first sentence from the body content.
// It strips markdown headers and returns up to the first period-space
// or period-newline boundary.
func extractSummary(body string) string {
	// Skip blank lines and markdown headers at the start.
	lines := strings.Split(body, "\n")
	var text string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		text = trimmed
		break
	}
	if text == "" {
		return ""
	}
	// Find the first sentence boundary.
	for i, r := range text {
		if r == '.' && i+1 < len(text) {
			next := text[i+1]
			if next == ' ' || next == '\n' || next == '\r' {
				return text[:i+1]
			}
		}
	}
	// If no sentence boundary, check for trailing period.
	if strings.HasSuffix(text, ".") {
		return text
	}
	// Return the whole first line if no period found.
	return text
}

// splitFrontmatter separates YAML frontmatter from the markdown body.
func splitFrontmatter(data []byte) (yamlBlock []byte, body []byte, err error) {
	const delimiter = "---"

	trimmed := bytes.TrimSpace(data)
	if !bytes.HasPrefix(trimmed, []byte(delimiter)) {
		return nil, nil, errors.New("missing opening --- delimiter")
	}

	// Find end of frontmatter (second "---" line).
	rest := trimmed[len(delimiter):]
	rest = bytes.TrimLeft(rest, "\r\n")

	idx := bytes.Index(rest, []byte("\n"+delimiter))
	if idx < 0 {
		return nil, nil, errors.New("missing closing --- delimiter")
	}

	yamlBlock = rest[:idx]
	bodyBlock := rest[idx+len("\n"+delimiter):]
	bodyBlock = bytes.TrimLeft(bodyBlock, "\r\n")

	return yamlBlock, bodyBlock, nil
}
