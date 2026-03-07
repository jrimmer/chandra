// Package stub provides lightweight test doubles for provider.Provider and
// provider.EmbeddingProvider. Import in integration and unit tests to avoid
// duplicating mock implementations in each test file.
package stub

import (
	"context"
	"sync"

	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/pkg"
)

// ─── Provider ─────────────────────────────────────────────────────────────────

// Provider is a deterministic stub for provider.Provider.
//
// Responses are returned in order; the last response is repeated when the list
// is exhausted. If Err is non-nil it is returned on every call regardless of
// Responses. All CompletionRequests are recorded in Calls.
type Provider struct {
	mu        sync.Mutex
	Responses []provider.CompletionResponse
	Err       error
	Calls     []provider.CompletionRequest
	idx       int
	ModelName string
}

// NewProvider returns a stub that returns a single plain-text assistant response.
func NewProvider(text string) *Provider {
	return &Provider{
		ModelName: "stub",
		Responses: []provider.CompletionResponse{
			{
				Message:      provider.Message{Role: "assistant", Content: text},
				StopReason:   "stop",
				InputTokens:  5,
				OutputTokens: 10,
			},
		},
	}
}

// NewSequenceProvider returns a stub that cycles through multiple responses.
func NewSequenceProvider(texts ...string) *Provider {
	p := &Provider{ModelName: "stub-seq"}
	for _, t := range texts {
		p.Responses = append(p.Responses, provider.CompletionResponse{
			Message:    provider.Message{Role: "assistant", Content: t},
			StopReason: "stop",
		})
	}
	return p
}

// NewToolProvider returns a stub whose first call emits a tool call and whose
// second call returns the final text (simulating a single tool-call round).
func NewToolProvider(toolName string, params []byte, finalText string) *Provider {
	return &Provider{
		ModelName: "stub-tool",
		Responses: []provider.CompletionResponse{
			{
				Message:    provider.Message{Role: "assistant"},
				StopReason: "tool_calls",
				ToolCalls:  []pkg.ToolCall{{ID: "tc-001", Name: toolName, Parameters: params}},
			},
			{
				Message:    provider.Message{Role: "assistant", Content: finalText},
				StopReason: "stop",
			},
		},
	}
}

func (p *Provider) Complete(_ context.Context, req provider.CompletionRequest) (provider.CompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls = append(p.Calls, req)
	if p.Err != nil {
		return provider.CompletionResponse{}, p.Err
	}
	if len(p.Responses) == 0 {
		return provider.CompletionResponse{
			Message:    provider.Message{Role: "assistant", Content: "(stub: no responses)"},
			StopReason: "stop",
		}, nil
	}
	idx := p.idx
	if idx >= len(p.Responses) {
		idx = len(p.Responses) - 1
	} else {
		p.idx++
	}
	return p.Responses[idx], nil
}

func (p *Provider) CountTokens(_ []provider.Message, _ []pkg.ToolDef) (int, error) {
	return 10, nil
}

func (p *Provider) ModelID() string { return p.ModelName }

// CallCount returns how many times Complete was called.
func (p *Provider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.Calls)
}

// LastRequest returns the most recent CompletionRequest. Panics if no calls.
func (p *Provider) LastRequest() provider.CompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.Calls) == 0 {
		panic("stub.Provider: no calls recorded")
	}
	return p.Calls[len(p.Calls)-1]
}

var _ provider.Provider = (*Provider)(nil)

// ─── EmbeddingProvider ────────────────────────────────────────────────────────

// EmbeddingProvider returns a fixed non-zero vector of the configured dimension.
type EmbeddingProvider struct {
	Dim   int     // embedding dimension (default 768 if zero)
	Value float32 // per-element value (default 0.1 if zero)
}

// NewEmbeddingProvider returns a fixed-vector embedder. Pass 0 for default dims (768).
func NewEmbeddingProvider(dim int) *EmbeddingProvider {
	if dim == 0 {
		dim = 768
	}
	return &EmbeddingProvider{Dim: dim, Value: 0.1}
}

func (e *EmbeddingProvider) Embed(_ context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	val := e.Value
	if val == 0 {
		val = 0.1
	}
	dim := e.Dim
	if dim == 0 {
		dim = 768
	}
	embs := make([][]float32, len(req.Texts))
	for i := range embs {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = val
		}
		embs[i] = vec
	}
	return provider.EmbeddingResponse{Embeddings: embs, Model: "stub-embedder", Dimensions: dim}, nil
}

func (e *EmbeddingProvider) Dimensions() int {
	if e.Dim == 0 {
		return 768
	}
	return e.Dim
}

var _ provider.EmbeddingProvider = (*EmbeddingProvider)(nil)
