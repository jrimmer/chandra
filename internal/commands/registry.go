// Package commands implements the Chandra ! command system.
// Commands are platform-agnostic control operations that execute without
// an LLM call (instant) or by force-activating a skill (skill-delegated).
package commands

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jrimmer/chandra/internal/agent"
	"github.com/jrimmer/chandra/internal/config"
	"github.com/jrimmer/chandra/internal/scheduler"
	"github.com/jrimmer/chandra/internal/skills"
)

// Command carries the parsed command and its call context.
type Command struct {
	Name      string // lower-cased command name, e.g. "reset"
	Args      string // everything after "!name ", trimmed
	UserID    string
	ChannelID string
	SessionID string

	// lastUserMsg is the last non-command user message in this conversation.
	// Populated by the conv worker; used by !retry.
	LastUserMsg string
}

// Result is returned by every command handler.
type Result struct {
	Content string // response text to send to the user
	Rerun   bool   // if true, conv worker re-runs LastUserMsg through LLM
}

// HandlerFunc is a command implementation.
type HandlerFunc func(ctx context.Context, cmd Command, env *Env) Result

// CommandDef describes one registered command.
type CommandDef struct {
	Handler     HandlerFunc
	Description string
	Usage       string
	Source      string // "builtin" or skill name
}

// Env carries dependencies available to all command handlers.
type Env struct {
	DB        *sql.DB
	Sessions  agent.Manager
	Scheduler scheduler.Scheduler
	Skills    *skills.Registry
	Config    *config.Config
	StartedAt time.Time
}

// Registry maps command names to their definitions.
type Registry struct {
	handlers map[string]CommandDef
}

// NewRegistry creates a Registry pre-loaded with all built-in commands.
func NewRegistry(env *Env) *Registry {
	r := &Registry{handlers: make(map[string]CommandDef)}
	registerBuiltins(r, env)
	return r
}

// Register adds or replaces a command. Built-in commands cannot be overridden
// by skills — RegisterSkill uses a different path that checks for conflicts.
func (r *Registry) Register(name string, def CommandDef) {
	r.handlers[strings.ToLower(name)] = def
}

// RegisterSkill adds a skill-delegated command. If a built-in or earlier
// skill already owns the name, logs a warning and skips.
func (r *Registry) RegisterSkill(name, skillName, description, usage string) (ok bool) {
	lower := strings.ToLower(name)
	if existing, exists := r.handlers[lower]; exists {
		// Built-in commands always win; skill conflicts: first loaded wins.
		_ = existing
		return false
	}
	r.handlers[lower] = CommandDef{
		Handler:     nil, // nil = skill-delegated; conv worker handles routing
		Description: description,
		Usage:       usage,
		Source:      skillName,
	}
	return true
}

// RemoveSkillCommands removes all commands registered by a given skill.
// Called when a skill is unloaded/deleted.
func (r *Registry) RemoveSkillCommands(skillName string) {
	for name, def := range r.handlers {
		if def.Source == skillName {
			delete(r.handlers, name)
		}
	}
}

// SyncSkillCommands updates skill commands from the current skill registry.
// Removes commands for unloaded skills; adds commands for new skills.
func (r *Registry) SyncSkillCommands(reg *skills.Registry) {
	// Remove stale skill commands.
	loadedSkills := make(map[string]bool)
	for _, s := range reg.All() {
		loadedSkills[s.Name] = true
	}
	for name, def := range r.handlers {
		if def.Source != "builtin" && !loadedSkills[def.Source] {
			delete(r.handlers, name)
		}
	}
	// Add new skill commands.
	for _, s := range reg.All() {
		for _, sc := range s.Commands {
			r.RegisterSkill(sc.Name, s.Name, sc.Description, sc.Usage)
		}
	}
}

// Lookup returns the CommandDef for a name, or false if not found.
func (r *Registry) Lookup(name string) (CommandDef, bool) {
	def, ok := r.handlers[strings.ToLower(name)]
	return def, ok
}

// IsSkillDelegate returns true if this command routes to a skill (no Go handler).
func (r *Registry) IsSkillDelegate(name string) (skillName string, ok bool) {
	def, found := r.handlers[strings.ToLower(name)]
	if !found || def.Handler != nil {
		return "", false
	}
	return def.Source, true
}

// Dispatch parses content as a !command and dispatches it.
// Returns (result, true) if handled, or ("", false) if not a known command.
func (r *Registry) Dispatch(ctx context.Context, content string, cmd Command, env *Env) (Result, bool) {
	name, args := parseCommand(content)
	if name == "" {
		return Result{}, false
	}
	def, ok := r.handlers[name]
	if !ok {
		return Result{Content: fmt.Sprintf("Unknown command `!%s`. Try `!help`.", name)}, true
	}
	if def.Handler == nil {
		// Skill-delegated: caller handles routing.
		return Result{}, false
	}
	cmd.Name = name
	cmd.Args = args
	return def.Handler(ctx, cmd, env), true
}

// All returns all registered commands sorted by name.
func (r *Registry) All() []CommandDef {
	var defs []CommandDef
	for name, def := range r.handlers {
		d := def
		d.Usage = "!" + name // ensure usage has the name prefix if not set
		if def.Usage != "" {
			d.Usage = def.Usage
		}
		defs = append(defs, d)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Usage < defs[j].Usage })
	return defs
}

// AllBySource returns commands grouped: builtins first, then by skill name.
func (r *Registry) AllBySource() (builtin []CommandDef, bySkill map[string][]CommandDef) {
	bySkill = make(map[string][]CommandDef)
	for name, def := range r.handlers {
		d := def
		if d.Usage == "" {
			d.Usage = "!" + name
		}
		if def.Source == "builtin" {
			builtin = append(builtin, d)
		} else {
			bySkill[def.Source] = append(bySkill[def.Source], d)
		}
	}
	sort.Slice(builtin, func(i, j int) bool { return builtin[i].Usage < builtin[j].Usage })
	return
}

// Parse parses "!name [args]" from a message. Returns empty name if not a command.
func Parse(content string) (name, args string) {
	return parseCommand(content)
}

func parseCommand(content string) (name, args string) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "!") {
		return "", ""
	}
	rest := trimmed[1:]
	parts := strings.SplitN(rest, " ", 2)
	name = strings.ToLower(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		args = strings.TrimSpace(parts[1])
	}
	// Exclude !join — handled separately before the command interceptor.
	if name == "join" {
		return "", ""
	}
	return name, args
}
