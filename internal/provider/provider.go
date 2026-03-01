package provider

import (
	"context"

	"github.com/jrimmer/chandra/pkg"
)

type Message struct {
	Role       string         // "system" | "user" | "assistant" | "tool"
	Content    string
	ToolCallID string         // populated for role="tool" responses
	ToolCalls  []pkg.ToolCall // populated when model requests tool execution
}

type CompletionRequest struct {
	Messages    []Message
	Tools       []pkg.ToolDef // available tools for this turn
	MaxTokens   int
	Temperature float32
	Stream      bool
}

type CompletionResponse struct {
	Message      Message
	ToolCalls    []pkg.ToolCall // non-empty when model wants to call tools
	InputTokens  int
	OutputTokens int
	StopReason   string // "stop" | "tool_calls" | "max_tokens"
}

type Provider interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
	CountTokens(messages []Message, tools []pkg.ToolDef) (int, error)
	ModelID() string
}

// EmbeddingRequest and EmbeddingResponse for the separate embeddings service.
// EmbeddingProvider is separate from Provider because chat and embeddings
// often use different endpoints/services.
type EmbeddingRequest struct {
	Texts []string
	Model string // optional, uses config default if empty
}

type EmbeddingResponse struct {
	Embeddings [][]float32 // one embedding per input text
	Model      string
	Dimensions int
}

type EmbeddingProvider interface {
	Embed(ctx context.Context, req EmbeddingRequest) (EmbeddingResponse, error)
	Dimensions() int // returns embedding dimensions for this model
}
