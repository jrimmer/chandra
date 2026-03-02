package skills

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GenerationLock tracks a per-skill lock with heartbeat for abandonment detection.
type GenerationLock struct {
	heartbeatAt time.Time
	done        chan struct{}
}

// Registry loads, stores, and matches skills.
type Registry struct {
	mu              sync.RWMutex
	skills          map[string]Skill
	unmet           []UnmetSkill
	skillsDir       string
	registeredTools map[string]bool
	genLocks        map[string]*GenerationLock
	maxPendingReview int
}

// NewRegistry creates an empty skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills:   make(map[string]Skill),
		genLocks: make(map[string]*GenerationLock),
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
// Unapproved generated skills are excluded from results.
func (r *Registry) Match(message string) []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(message)
	var matches []Skill

	for _, skill := range r.skills {
		// Skip unapproved generated skills.
		if skill.Generated != nil && skill.Generated.Status != SkillApproved {
			continue
		}
		for _, trigger := range skill.Triggers {
			if strings.Contains(lower, strings.ToLower(trigger)) {
				matches = append(matches, skill)
				break
			}
		}
	}

	return matches
}

// Register adds a skill to the registry (used for generated skills).
func (r *Registry) Register(skill Skill) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[skill.Name] = skill
	return nil
}

// Approve marks a generated skill as approved.
func (r *Registry) Approve(name, reviewer string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.skills[name]
	if !ok {
		return fmt.Errorf("skill not found: %s", name)
	}
	if s.Generated == nil {
		return fmt.Errorf("skill %s is not generated", name)
	}
	s.Generated.Status = SkillApproved
	s.Generated.Reviewer = reviewer
	s.Generated.ReviewedAt = time.Now()
	r.skills[name] = s
	return nil
}

// Reject marks a generated skill as rejected.
func (r *Registry) Reject(name, reviewer string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.skills[name]
	if !ok {
		return fmt.Errorf("skill not found: %s", name)
	}
	if s.Generated == nil {
		return fmt.Errorf("skill %s is not generated", name)
	}
	s.Generated.Status = SkillRejected
	s.Generated.Reviewer = reviewer
	s.Generated.ReviewedAt = time.Now()
	r.skills[name] = s
	return nil
}

// PendingReview returns all skills pending review.
func (r *Registry) PendingReview() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Skill
	for _, s := range r.skills {
		if s.Generated != nil && s.Generated.Status == SkillPendingReview {
			out = append(out, s)
		}
	}
	return out
}

// Approved returns all skills that are either non-generated or approved.
func (r *Registry) Approved() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Skill
	for _, s := range r.skills {
		if s.Generated == nil || s.Generated.Status == SkillApproved {
			out = append(out, s)
		}
	}
	return out
}

// AcquireGenerationLock attempts to acquire a generation lock for the given skill name.
// Returns (true, releaseFunc) on success, (false, nil) if already locked.
// The lock is considered abandoned if heartbeat hasn't been updated in 60 seconds.
// The release function closes the heartbeat goroutine.
func (r *Registry) AcquireGenerationLock(skillName string) (acquired bool, release func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if lock, held := r.genLocks[skillName]; held {
		if time.Since(lock.heartbeatAt) < 60*time.Second {
			return false, nil
		}
		// Abandoned lock — clean it up.
		close(lock.done)
	}

	done := make(chan struct{})
	lock := &GenerationLock{
		heartbeatAt: time.Now(),
		done:        done,
	}
	r.genLocks[skillName] = lock

	// Start heartbeat goroutine.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.mu.Lock()
				if l, ok := r.genLocks[skillName]; ok && l == lock {
					l.heartbeatAt = time.Now()
				}
				r.mu.Unlock()
			case <-done:
				return
			}
		}
	}()

	return true, func() {
		r.mu.Lock()
		if l, ok := r.genLocks[skillName]; ok && l == lock {
			delete(r.genLocks, skillName)
		}
		r.mu.Unlock()
		close(done)
	}
}

// HeartbeatGenerationLock manually updates the heartbeat for a generation lock.
func (r *Registry) HeartbeatGenerationLock(skillName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if lock, ok := r.genLocks[skillName]; ok {
		lock.heartbeatAt = time.Now()
	}
}

// SetMaxPendingReview sets the maximum number of pending review skills allowed.
func (r *Registry) SetMaxPendingReview(max int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxPendingReview = max
}

// MaxPendingReviewReached returns true if the number of pending review skills
// is at or above the configured maximum.
func (r *Registry) MaxPendingReviewReached() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.maxPendingReview <= 0 {
		return false
	}
	count := 0
	for _, s := range r.skills {
		if s.Generated != nil && s.Generated.Status == SkillPendingReview {
			count++
		}
	}
	return count >= r.maxPendingReview
}
