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
	Pattern     string         // raw regex pattern
	Categories  []string       // e.g. ["destructive", "external"]
	Description string         // shown to user when confirmation is requested
	compiled    *regexp.Regexp // compiled at construction time
}

// Registry manages tool registration, capability enforcement, and
// confirmation-gate matching.
type Registry interface {
	Register(tool pkg.Tool) error
	Get(name string) (pkg.Tool, bool)
	All() []pkg.ToolDef
	EnforceCapabilities(call pkg.ToolCall) error
	RequiresConfirmation(call pkg.ToolCall) (bool, ConfirmationRule)
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
			Pattern:     rule.Pattern,
			Categories:  rule.Categories,
			Description: rule.Description,
			compiled:    re,
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

// EnforceCapabilities checks that the tool named in call.Name is registered
// and that its declared capabilities are valid. Returns an error if the tool
// is not registered.
func (r *registry) EnforceCapabilities(call pkg.ToolCall) error {
	_, ok := r.Get(call.Name)
	if !ok {
		return fmt.Errorf("tools/registry: unknown tool %q", call.Name)
	}
	return nil
}

// RequiresConfirmation returns true and the matching ConfirmationRule if
// call.Name matches any registered confirmation rule pattern. Returns false
// and a zero ConfirmationRule if no rule matches.
func (r *registry) RequiresConfirmation(call pkg.ToolCall) (bool, ConfirmationRule) {
	for _, rule := range r.confirmRules {
		if rule.compiled.MatchString(call.Name) {
			return true, rule
		}
	}
	return false, ConfirmationRule{}
}
