package openai_test

import (
	"testing"

	"github.com/jrimmer/chandra/internal/provider"
	oai "github.com/jrimmer/chandra/internal/provider/openai"
	"github.com/jrimmer/chandra/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProvider_ValidConfig(t *testing.T) {
	p := oai.NewProvider("https://api.openai.com/v1", "test-key", "gpt-4o")
	require.NotNil(t, p)
	assert.Equal(t, "gpt-4o", p.ModelID())
}

func TestCountTokens_ReturnsNonZero(t *testing.T) {
	p := oai.NewProvider("https://api.openai.com/v1", "test-key", "gpt-4o")
	messages := []provider.Message{
		{Role: "user", Content: "Hello, world!"},
		{Role: "assistant", Content: "Hi there! How can I help you?"},
	}
	count, err := p.CountTokens(messages, nil)
	require.NoError(t, err)
	assert.Greater(t, count, 0)
}

func TestCountTokens_IncludesToolTokens(t *testing.T) {
	p := oai.NewProvider("https://api.openai.com/v1", "test-key", "gpt-4o")
	messages := []provider.Message{
		{Role: "user", Content: "Check the weather"},
	}
	tools := []pkg.ToolDef{
		{
			Name:        "weather.get",
			Description: "Get the current weather for a location",
			Parameters:  []byte(`{"type":"object","properties":{"location":{"type":"string"}}}`),
		},
	}
	countWithTools, err := p.CountTokens(messages, tools)
	require.NoError(t, err)

	countWithoutTools, err := p.CountTokens(messages, nil)
	require.NoError(t, err)

	assert.Greater(t, countWithTools, countWithoutTools, "tool definitions should add tokens")
}
