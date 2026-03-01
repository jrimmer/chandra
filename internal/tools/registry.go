package tools

import (
	"fmt"
	"regexp"
	"sync"

	"github.com/jrimmer/chandra/pkg"
)

// ConfirmationRule describes a regex pattern; calls whose names match the
// pattern require human confirmation before execution.
type ConfirmationRule struct {
	Pattern  string         // raw regex pattern
	compiled *regexp.Regexp // compiled at construction time
}

// Registry manages tool registration, capability enforcement, and
// confirmation-gate matching.
type Registry interface {
	Register(tool pkg.Tool) error
	Get(name string) (pkg.Tool, bool)
	All() []pkg.ToolDef
	EnforceCapabilities(call pkg.ToolCall, granted []pkg.Capability) error
	RequiresConfirmation(callName string) bool
}

// Compile-time assertion that *registry satisfies Registry.
var _ Registry = (*registry)(nil)

type registry struct {
	tools         sync.Map // map[string]pkg.Tool
	confirmRules  []ConfirmationRule
}

// NewRegistry creates a new registry. All regex patterns in confirmRules are
// compiled immediately; an error is returned if any pattern is invalid.
func NewRegistry(confirmRules []ConfirmationRule) (*registry, error) {
	compiled := make([]ConfirmationRule, len(confirmRules))
	for i, rule := range confirmRules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, fmt.Errorf("tools/registry: compile pattern %q: %w", rule.Pattern, err)
		}
		compiled[i] = ConfirmationRule{
			Pattern:  rule.Pattern,
			compiled: re,
		}
	}
	return &registry{confirmRules: compiled}, nil
}

// Register adds a tool to the registry. Returns an error if a tool with the
// same name is already registered.
func (r *registry) Register(tool pkg.Tool) error {
	def := tool.Definition()
	if def.Name == "" {
		return fmt.Errorf("tools/registry: tool has empty name")
	}
	if _, loaded := r.tools.LoadOrStore(def.Name, tool); loaded {
		return fmt.Errorf("tools/registry: tool %q already registered", def.Name)
	}
	return nil
}

// Get retrieves a registered tool by name.
func (r *registry) Get(name string) (pkg.Tool, bool) {
	v, ok := r.tools.Load(name)
	if !ok {
		return nil, false
	}
	return v.(pkg.Tool), true
}

// All returns the ToolDef of every registered tool. Order is non-deterministic.
func (r *registry) All() []pkg.ToolDef {
	var defs []pkg.ToolDef
	r.tools.Range(func(_, v any) bool {
		defs = append(defs, v.(pkg.Tool).Definition())
		return true
	})
	return defs
}

// EnforceCapabilities checks that every capability declared by the tool named
// in call.Name is present in granted. Returns an error listing any missing
// capabilities. Returns an error if the tool is not registered.
func (r *registry) EnforceCapabilities(call pkg.ToolCall, granted []pkg.Capability) error {
	tool, ok := r.Get(call.Name)
	if !ok {
		return fmt.Errorf("tools/registry: unknown tool %q", call.Name)
	}

	grantedSet := make(map[pkg.Capability]struct{}, len(granted))
	for _, c := range granted {
		grantedSet[c] = struct{}{}
	}

	var missing []pkg.Capability
	for _, declared := range tool.Definition().Capabilities {
		if _, ok := grantedSet[declared]; !ok {
			missing = append(missing, declared)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("tools/registry: tool %q requires capabilities not granted: %v", call.Name, missing)
	}
	return nil
}

// RequiresConfirmation returns true if callName matches any registered
// confirmation rule pattern.
func (r *registry) RequiresConfirmation(callName string) bool {
	for _, rule := range r.confirmRules {
		if rule.compiled.MatchString(callName) {
			return true
		}
	}
	return false
}
