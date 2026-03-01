package anthropic_test

import (
	"testing"

	"github.com/jrimmer/chandra/internal/provider"
	anth "github.com/jrimmer/chandra/internal/provider/anthropic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProvider_ValidConfig(t *testing.T) {
	p := anth.NewProvider("https://api.anthropic.com", "test-key", "claude-3-5-sonnet-20241022")
	require.NotNil(t, p)
	assert.Equal(t, "claude-3-5-sonnet-20241022", p.ModelID())
}

func TestCountTokens_Approximate(t *testing.T) {
	p := anth.NewProvider("https://api.anthropic.com", "test-key", "claude-3-5-sonnet-20241022")
	messages := []provider.Message{
		{Role: "user", Content: "Hello, this is a test message for token counting."},
	}
	count, err := p.CountTokens(messages, nil)
	require.NoError(t, err)
	assert.Greater(t, count, 0, "should return non-zero token count")
}
