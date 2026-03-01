package embeddings_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/internal/provider/embeddings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbed_SingleText(t *testing.T) {
	// Mock server returning a fake embedding
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"object": "embedding", "embedding": []float32{0.1, 0.2, 0.3}, "index": 0},
			},
			"model": "text-embedding-3-small",
			"usage": map[string]any{"prompt_tokens": 5, "total_tokens": 5},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := embeddings.NewProvider(srv.URL, "test-key", "text-embedding-3-small", 1536)
	result, err := p.Embed(context.Background(), provider.EmbeddingRequest{
		Texts: []string{"hello world"},
	})
	require.NoError(t, err)
	assert.Len(t, result.Embeddings, 1)
	assert.Equal(t, 1536, p.Dimensions())
}

func TestEmbed_Batch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 2 embeddings for 2 inputs
		resp := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"object": "embedding", "embedding": []float32{0.1, 0.2}, "index": 0},
				{"object": "embedding", "embedding": []float32{0.3, 0.4}, "index": 1},
			},
			"model": "text-embedding-3-small",
			"usage": map[string]any{"prompt_tokens": 10, "total_tokens": 10},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := embeddings.NewProvider(srv.URL, "test-key", "text-embedding-3-small", 1536)
	result, err := p.Embed(context.Background(), provider.EmbeddingRequest{
		Texts: []string{"first", "second"},
	})
	require.NoError(t, err)
	assert.Len(t, result.Embeddings, 2)
}
