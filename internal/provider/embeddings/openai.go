package embeddings

import (
	"context"
	"fmt"

	goopenai "github.com/sashabaranov/go-openai"

	"github.com/jrimmer/chandra/internal/provider"
)

type Provider struct {
	client     *goopenai.Client
	model      string
	dimensions int
}

func NewProvider(baseURL, apiKey, model string, dimensions int) *Provider {
	cfg := goopenai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	return &Provider{
		client:     goopenai.NewClientWithConfig(cfg),
		model:      model,
		dimensions: dimensions,
	}
}

func (p *Provider) Dimensions() int { return p.dimensions }

func (p *Provider) Embed(ctx context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	model := p.model
	if req.Model != "" {
		model = req.Model
	}

	if len(req.Texts) == 0 {
		return provider.EmbeddingResponse{Model: model, Dimensions: p.dimensions}, nil
	}

	resp, err := p.client.CreateEmbeddings(ctx, goopenai.EmbeddingRequestStrings{
		Input: req.Texts,
		Model: goopenai.EmbeddingModel(model),
	})
	if err != nil {
		return provider.EmbeddingResponse{}, fmt.Errorf("embed: %w", err)
	}

	result := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		result[i] = d.Embedding
	}

	return provider.EmbeddingResponse{
		Embeddings: result,
		Model:      string(resp.Model),
		Dimensions: p.dimensions,
	}, nil
}
