package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	tiktoken "github.com/pkoukk/tiktoken-go"

	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/pkg"
)

type Provider struct {
	client *anthsdk.Client
	model  string
}

func NewProvider(baseURL, apiKey, model string) *Provider {
	client := anthsdk.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey(apiKey),
	)
	return &Provider{client: &client, model: model}
}

func (p *Provider) ModelID() string { return p.model }

func (p *Provider) Complete(ctx context.Context, req provider.CompletionRequest) (provider.CompletionResponse, error) {
	// Extract system messages and translate remaining messages.
	// Anthropic requires system content to be passed at the top level,
	// not as a message with role "system".
	var systemBlocks []anthsdk.TextBlockParam
	var apiMessages []anthsdk.MessageParam

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			systemBlocks = append(systemBlocks, anthsdk.TextBlockParam{Text: m.Content})

		case "user":
			apiMessages = append(apiMessages, anthsdk.NewUserMessage(
				anthsdk.NewTextBlock(m.Content),
			))

		case "assistant":
			if len(m.ToolCalls) > 0 {
				// Assistant message with tool calls
				var blocks []anthsdk.ContentBlockParamUnion
				if m.Content != "" {
					blocks = append(blocks, anthsdk.NewTextBlock(m.Content))
				}
				for _, tc := range m.ToolCalls {
					var input any
					if err := json.Unmarshal(tc.Parameters, &input); err != nil {
						input = json.RawMessage(tc.Parameters)
					}
					blocks = append(blocks, anthsdk.NewToolUseBlock(tc.ID, input, tc.Name))
				}
				apiMessages = append(apiMessages, anthsdk.NewAssistantMessage(blocks...))
			} else {
				apiMessages = append(apiMessages, anthsdk.NewAssistantMessage(
					anthsdk.NewTextBlock(m.Content),
				))
			}

		case "tool":
			// Tool result: Anthropic expects this as a user message with a tool_result block
			apiMessages = append(apiMessages, anthsdk.NewUserMessage(
				anthsdk.NewToolResultBlock(m.ToolCallID, m.Content, false),
			))
		}
	}

	// Translate tool definitions into ToolUnionParam (wrapping ToolParam)
	var apiTools []anthsdk.ToolUnionParam
	for _, td := range req.Tools {
		schema := buildInputSchema(td.Parameters)
		tool := anthsdk.ToolParam{
			Name:        td.Name,
			InputSchema: schema,
		}
		if td.Description != "" {
			tool.Description = param.NewOpt(td.Description)
		}
		apiTools = append(apiTools, anthsdk.ToolUnionParam{OfTool: &tool})
	}

	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 4096
	}

	apiReq := anthsdk.MessageNewParams{
		Model:     anthsdk.Model(p.model),
		MaxTokens: maxTokens,
		Messages:  apiMessages,
	}
	if len(systemBlocks) > 0 {
		apiReq.System = systemBlocks
	}
	if len(apiTools) > 0 {
		apiReq.Tools = apiTools
	}
	apiReq.Temperature = param.NewOpt(float64(req.Temperature))

	resp, err := p.client.Messages.New(ctx, apiReq)
	if err != nil {
		return provider.CompletionResponse{}, fmt.Errorf("anthropic complete: %w", err)
	}

	out := provider.CompletionResponse{
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
		StopReason:   mapStopReason(resp.StopReason),
	}

	// Extract text content and tool calls from response
	var textParts []string
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			tb := block.AsToolUse()
			out.ToolCalls = append(out.ToolCalls, pkg.ToolCall{
				ID:         tb.ID,
				Name:       tb.Name,
				Parameters: json.RawMessage(tb.Input),
			})
		}
	}

	out.Message = provider.Message{
		Role:      "assistant",
		Content:   strings.Join(textParts, ""),
		ToolCalls: out.ToolCalls,
	}

	return out, nil
}

// CountTokens returns an approximate token count using the cl100k_base tokenizer.
//
// tiktoken-go implements OpenAI's tokenizers, not Claude's. cl100k_base is
// reasonably close to Claude's tokenizer. This is acceptable for budget purposes,
// as per DESIGN.md section 7: token counting MUST use local tokenization with
// no HTTP calls.
func (p *Provider) CountTokens(messages []provider.Message, tools []pkg.ToolDef) (int, error) {
	enc, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		return 0, fmt.Errorf("tiktoken encoding: %w", err)
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

// buildInputSchema converts a raw JSON schema (json.RawMessage) from a pkg.ToolDef
// into the ToolInputSchemaParam that the Anthropic SDK expects.
// The ExtraFields map is used to pass through any additional schema properties
// (e.g. properties, required, etc.) that are not represented as dedicated struct fields.
func buildInputSchema(rawSchema json.RawMessage) anthsdk.ToolInputSchemaParam {
	schema := anthsdk.ToolInputSchemaParam{}
	if rawSchema == nil {
		return schema
	}

	var parsed map[string]any
	if err := json.Unmarshal(rawSchema, &parsed); err != nil {
		// If we can't parse it, return the empty schema
		return schema
	}

	// Remove the "type" field since ToolInputSchemaParam.Type is always "object"
	delete(parsed, "type")

	if len(parsed) > 0 {
		schema.ExtraFields = parsed
	}
	return schema
}

// mapStopReason translates Anthropic stop reasons to Chandra's canonical stop reasons.
func mapStopReason(r anthsdk.StopReason) string {
	switch r {
	case anthsdk.StopReasonEndTurn:
		return "stop"
	case anthsdk.StopReasonToolUse:
		return "tool_calls"
	case anthsdk.StopReasonMaxTokens:
		return "max_tokens"
	default:
		return string(r)
	}
}
