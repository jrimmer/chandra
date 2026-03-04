package tools_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/tools"
	"github.com/jrimmer/chandra/pkg"
)

// stubTool is a minimal pkg.Tool for use in registry tests.
type stubTool struct {
	def    pkg.ToolDef
	result pkg.ToolResult
	err    error
}

func (s *stubTool) Definition() pkg.ToolDef { return s.def }
func (s *stubTool) Execute(_ context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	s.result.ID = call.ID
	return s.result, s.err
}

func newStubTool(name string, caps ...pkg.Capability) *stubTool {
	return &stubTool{
		def: pkg.ToolDef{
			Name:         name,
			Description:  "stub tool " + name,
			Tier:         pkg.TierBuiltin,
			Capabilities: caps,
		},
	}
}

// stubTrustedTool implements pkg.TrustedTool for capability enforcement tests.
type stubTrustedTool struct {
	def  pkg.ToolDef
	caps []pkg.Capability
}

func (s *stubTrustedTool) Definition() pkg.ToolDef { return s.def }
func (s *stubTrustedTool) Execute(_ context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	return pkg.ToolResult{ID: call.ID}, nil
}
func (s *stubTrustedTool) DeclaredCapabilities() []pkg.Capability { return s.caps }

func newStubTrustedTool(name string, caps ...pkg.Capability) *stubTrustedTool {
	return &stubTrustedTool{
		def: pkg.ToolDef{
			Name:        name,
			Description: "trusted stub tool " + name,
			Tier:        pkg.TierTrusted,
		},
		caps: caps,
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	tool := newStubTool("homeassistant_get_state")
	require.NoError(t, reg.Register(tool))

	got, ok := reg.Get("homeassistant_get_state")
	require.True(t, ok)
	assert.Equal(t, "homeassistant_get_state", got.Definition().Name)

	_, ok = reg.Get("nonexistent")
	assert.False(t, ok)
}

func TestRegistry_Register_DuplicateReturnsError(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	tool := newStubTool("my_tool")
	require.NoError(t, reg.Register(tool))

	err = reg.Register(newStubTool("my_tool"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegistry_All_ReturnsDefinitions(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	require.NoError(t, reg.Register(newStubTool("tool_a")))
	require.NoError(t, reg.Register(newStubTool("tool_b")))
	require.NoError(t, reg.Register(newStubTool("tool_c")))

	defs := reg.All()
	assert.Len(t, defs, 3)

	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Name] = true
	}
	assert.True(t, names["tool_a"])
	assert.True(t, names["tool_b"])
	assert.True(t, names["tool_c"])
}

func TestRegistry_EnforceCapabilities_AllowsDeclared(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	tool := newStubTool("ha_read", pkg.CapMemoryRead, pkg.CapNetworkOut)
	require.NoError(t, reg.Register(tool))

	call := pkg.ToolCall{ID: "c1", Name: "ha_read"}

	err = reg.EnforceCapabilities(call)
	assert.NoError(t, err)
}

func TestRegistry_EnforceCapabilities_UnknownToolReturnsError(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	call := pkg.ToolCall{ID: "c3", Name: "unknown_tool"}
	err = reg.EnforceCapabilities(call)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool")
}

func TestRegistry_RequiresConfirmation_MatchesPattern(t *testing.T) {
	rules := []tools.ConfirmationRule{
		{Pattern: ".*delete.*", Categories: []string{"destructive"}, Description: "Deletes an entity"},
	}
	reg, err := tools.NewRegistry(rules)
	require.NoError(t, err)

	require.NoError(t, reg.Register(newStubTool("homeassistant_delete_entity")))

	call := pkg.ToolCall{ID: "c4", Name: "homeassistant_delete_entity"}
	matched, rule := reg.RequiresConfirmation(call)
	assert.True(t, matched)
	assert.Equal(t, ".*delete.*", rule.Pattern)
	assert.Equal(t, []string{"destructive"}, rule.Categories)
	assert.Equal(t, "Deletes an entity", rule.Description)
}

func TestRegistry_RequiresConfirmation_NoMatch(t *testing.T) {
	rules := []tools.ConfirmationRule{
		{Pattern: ".*delete.*"},
	}
	reg, err := tools.NewRegistry(rules)
	require.NoError(t, err)

	require.NoError(t, reg.Register(newStubTool("homeassistant_get_state")))

	call := pkg.ToolCall{ID: "c5", Name: "homeassistant_get_state"}
	matched, rule := reg.RequiresConfirmation(call)
	assert.False(t, matched)
	assert.Equal(t, tools.ConfirmationRule{}, rule)
}

func TestRegistry_NewRegistry_InvalidPatternReturnsError(t *testing.T) {
	rules := []tools.ConfirmationRule{
		{Pattern: "[invalid("},
	}
	_, err := tools.NewRegistry(rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compile pattern")
}

func TestRegistry_EnforceCapabilities_TrustedTool_WithCaps(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	tool := newStubTrustedTool("trusted_read", pkg.CapMemoryRead)
	require.NoError(t, reg.Register(tool))

	call := pkg.ToolCall{ID: "c10", Name: "trusted_read"}
	err = reg.EnforceCapabilities(call)
	assert.NoError(t, err, "TrustedTool with declared capabilities should pass")
}

func TestRegistry_EnforceCapabilities_TrustedTool_EmptyCaps(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	// Register a TrustedTool with no declared capabilities — should fail enforcement.
	tool := newStubTrustedTool("trusted_empty")
	require.NoError(t, reg.Register(tool))

	call := pkg.ToolCall{ID: "c11", Name: "trusted_empty"}
	err = reg.EnforceCapabilities(call)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no declared capabilities")
}
