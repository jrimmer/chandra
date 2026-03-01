package openai

import (
	"context"
	"encoding/json"
	"fmt"

	tiktoken "github.com/pkoukk/tiktoken-go"
	goopenai "github.com/sashabaranov/go-openai"

	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/pkg"
)

type Provider struct {
	client *goopenai.Client
	model  string
}

func NewProvider(baseURL, apiKey, model string) *Provider {
	cfg := goopenai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	client := goopenai.NewClientWithConfig(cfg)
	return &Provider{client: client, model: model}
}

func (p *Provider) ModelID() string { return p.model }

func (p *Provider) Complete(ctx context.Context, req provider.CompletionRequest) (provider.CompletionResponse, error) {
	// Translate Chandra messages to OpenAI messages
	var oaiMessages []goopenai.ChatCompletionMessage
	for _, m := range req.Messages {
		oaiMsg := goopenai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.ToolCallID != "" {
			oaiMsg.ToolCallID = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				oaiMsg.ToolCalls = append(oaiMsg.ToolCalls, goopenai.ToolCall{
					ID:   tc.ID,
					Type: goopenai.ToolTypeFunction,
					Function: goopenai.FunctionCall{
						Name:      tc.Name,
						Arguments: string(tc.Parameters),
					},
				})
			}
		}
		oaiMessages = append(oaiMessages, oaiMsg)
	}

	// Translate tool definitions
	var oaiTools []goopenai.Tool
	for _, td := range req.Tools {
		oaiTools = append(oaiTools, goopenai.Tool{
			Type: goopenai.ToolTypeFunction,
			Function: &goopenai.FunctionDefinition{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.Parameters,
			},
		})
	}

	oaiReq := goopenai.ChatCompletionRequest{
		Model:       p.model,
		Messages:    oaiMessages,
		Tools:       oaiTools,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	resp, err := p.client.CreateChatCompletion(ctx, oaiReq)
	if err != nil {
		return provider.CompletionResponse{}, fmt.Errorf("openai complete: %w", err)
	}

	if len(resp.Choices) == 0 {
		return provider.CompletionResponse{}, fmt.Errorf("openai complete: no choices in response")
	}

	choice := resp.Choices[0]
	out := provider.CompletionResponse{
		Message: provider.Message{
			Role:    choice.Message.Role,
			Content: choice.Message.Content,
		},
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		StopReason:   string(choice.FinishReason),
	}

	// Translate tool calls back
	for _, tc := range choice.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, pkg.ToolCall{
			ID:         tc.ID,
			Name:       tc.Function.Name,
			Parameters: json.RawMessage(tc.Function.Arguments),
		})
	}
	out.Message.ToolCalls = out.ToolCalls

	return out, nil
}

func (p *Provider) CountTokens(messages []provider.Message, tools []pkg.ToolDef) (int, error) {
	enc, err := tiktoken.EncodingForModel(p.model)
	if err != nil {
		// Fall back to cl100k_base for unknown models
		enc, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			return 0, fmt.Errorf("tiktoken encoding: %w", err)
		}
	}

	total := 0
	for _, m := range messages {
		total += len(enc.Encode(m.Content, nil, nil))
		total += 4 // per-message overhead (role, separators)
	}
	total += 2 // reply priming tokens

	// Count tool definition tokens
	for _, td := range tools {
		total += len(enc.Encode(td.Name, nil, nil))
		total += len(enc.Encode(td.Description, nil, nil))
		if td.Parameters != nil {
			total += len(enc.Encode(string(td.Parameters), nil, nil))
		}
	}

	return total, nil
}
