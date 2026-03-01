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

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	tool := newStubTool("homeassistant.get_state")
	require.NoError(t, reg.Register(tool))

	got, ok := reg.Get("homeassistant.get_state")
	require.True(t, ok)
	assert.Equal(t, "homeassistant.get_state", got.Definition().Name)

	_, ok = reg.Get("nonexistent")
	assert.False(t, ok)
}

func TestRegistry_Register_DuplicateReturnsError(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	tool := newStubTool("my.tool")
	require.NoError(t, reg.Register(tool))

	err = reg.Register(newStubTool("my.tool"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegistry_All_ReturnsDefinitions(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	require.NoError(t, reg.Register(newStubTool("tool.a")))
	require.NoError(t, reg.Register(newStubTool("tool.b")))
	require.NoError(t, reg.Register(newStubTool("tool.c")))

	defs := reg.All()
	assert.Len(t, defs, 3)

	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Name] = true
	}
	assert.True(t, names["tool.a"])
	assert.True(t, names["tool.b"])
	assert.True(t, names["tool.c"])
}

func TestRegistry_EnforceCapabilities_AllowsDeclared(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	tool := newStubTool("ha.read", pkg.CapMemoryRead, pkg.CapNetworkOut)
	require.NoError(t, reg.Register(tool))

	call := pkg.ToolCall{ID: "c1", Name: "ha.read"}
	granted := []pkg.Capability{pkg.CapMemoryRead, pkg.CapNetworkOut, pkg.CapFileRead}

	err = reg.EnforceCapabilities(call, granted)
	assert.NoError(t, err)
}

func TestRegistry_EnforceCapabilities_RejectsUndeclared(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	// Tool requires file:write but only memory:read is granted.
	tool := newStubTool("ha.write", pkg.CapFileWrite)
	require.NoError(t, reg.Register(tool))

	call := pkg.ToolCall{ID: "c2", Name: "ha.write"}
	granted := []pkg.Capability{pkg.CapMemoryRead}

	err = reg.EnforceCapabilities(call, granted)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file:write")
}

func TestRegistry_EnforceCapabilities_UnknownToolReturnsError(t *testing.T) {
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	call := pkg.ToolCall{ID: "c3", Name: "unknown.tool"}
	err = reg.EnforceCapabilities(call, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool")
}

func TestRegistry_RequiresConfirmation_MatchesPattern(t *testing.T) {
	rules := []tools.ConfirmationRule{
		{Pattern: ".*delete.*"},
	}
	reg, err := tools.NewRegistry(rules)
	require.NoError(t, err)

	require.NoError(t, reg.Register(newStubTool("homeassistant.delete_entity")))

	assert.True(t, reg.RequiresConfirmation("homeassistant.delete_entity"))
}

func TestRegistry_RequiresConfirmation_NoMatch(t *testing.T) {
	rules := []tools.ConfirmationRule{
		{Pattern: ".*delete.*"},
	}
	reg, err := tools.NewRegistry(rules)
	require.NoError(t, err)

	require.NoError(t, reg.Register(newStubTool("homeassistant.get_state")))

	assert.False(t, reg.RequiresConfirmation("homeassistant.get_state"))
}

func TestRegistry_NewRegistry_InvalidPatternReturnsError(t *testing.T) {
	rules := []tools.ConfirmationRule{
		{Pattern: "[invalid("},
	}
	_, err := tools.NewRegistry(rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compile pattern")
}
