package skills

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Registry loads, stores, and matches skills.
type Registry struct {
	mu              sync.RWMutex
	skills          map[string]Skill
	unmet           []UnmetSkill
	skillsDir       string
	registeredTools map[string]bool
}

// NewRegistry creates an empty skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]Skill),
	}
}

// Load scans skillsDir for SKILL.md files, parses them, validates requirements,
// and registers valid skills. registeredTools maps tool names to presence.
func (r *Registry) Load(ctx context.Context, skillsDir string, registeredTools map[string]bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.skillsDir = skillsDir
	r.registeredTools = registeredTools
	r.skills = make(map[string]Skill)
	r.unmet = nil

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("skills: directory does not exist, skipping", "dir", skillsDir)
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Warn if tools.go exists in user skill directory (ignored; Go tools must be compiled in).
		if _, statErr := os.Stat(filepath.Join(skillsDir, entry.Name(), "tools.go")); statErr == nil {
			slog.Warn("skills: tools.go in user skill directory is ignored (Go tools must be compiled in)",
				"skill", entry.Name())
		}

		skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue // No SKILL.md in this directory.
		}

		skill, err := ParseSkillMD(data, skillPath)
		if err != nil {
			slog.Warn("skills: failed to parse", "path", skillPath, "error", err)
			continue
		}

		// Gate on Generated.Status: skip unapproved generated skills.
		if skill.Generated != nil && skill.Generated.Status != SkillApproved {
			slog.Info("skills: skipping unapproved generated skill", "skill", skill.Name, "status", skill.Generated.Status)
			continue
		}

		missing := ValidateRequirements(skill.Requires, registeredTools)
		if len(missing) > 0 {
			slog.Info("skills: unmet requirements", "skill", skill.Name, "missing", missing)
			r.unmet = append(r.unmet, UnmetSkill{
				Name:    skill.Name,
				Path:    skillPath,
				Missing: missing,
			})
			continue
		}

		r.skills[skill.Name] = skill
		slog.Info("skills: loaded", "skill", skill.Name, "triggers", skill.Triggers)
	}

	return nil
}

// Reload rescans the skills directory using the same config as the last Load.
func (r *Registry) Reload(ctx context.Context) error {
	r.mu.RLock()
	dir := r.skillsDir
	tools := r.registeredTools
	r.mu.RUnlock()
	return r.Load(ctx, dir, tools)
}

// Get returns a skill by name.
func (r *Registry) Get(name string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

// All returns all loaded skills.
func (r *Registry) All() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	return out
}

// Unmet returns skills that could not load due to missing requirements.
func (r *Registry) Unmet() []UnmetSkill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]UnmetSkill, len(r.unmet))
	copy(out, r.unmet)
	return out
}

// Match returns skills whose triggers match words in the message.
// Matching is case-insensitive and checks for substring presence.
func (r *Registry) Match(message string) []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(message)
	var matches []Skill

	for _, skill := range r.skills {
		for _, trigger := range skill.Triggers {
			if strings.Contains(lower, strings.ToLower(trigger)) {
				matches = append(matches, skill)
				break
			}
		}
	}

	return matches
}
