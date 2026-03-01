package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/jrimmer/chandra/pkg"
)

const (
	defaultBaseURL = "https://html.duckduckgo.com/html"
	maxContentLen  = 2000
)

// WebSearch implements pkg.Tool to search the web via DuckDuckGo Lite.
type WebSearch struct {
	baseURL    string
	httpClient *http.Client
}

// Compile-time assertion.
var _ pkg.Tool = (*WebSearch)(nil)

// NewWebSearch returns a new WebSearch with default HTTP client and DuckDuckGo base URL.
func NewWebSearch() *WebSearch {
	return &WebSearch{
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
}

// SetBaseURL overrides the search endpoint base URL (used in tests).
func (ws *WebSearch) SetBaseURL(u string) {
	ws.baseURL = u
}

// SetHTTPClient overrides the HTTP client (used in tests).
func (ws *WebSearch) SetHTTPClient(c *http.Client) {
	ws.httpClient = c
}

// Definition returns the ToolDef for the web search skill.
func (ws *WebSearch) Definition() pkg.ToolDef {
	params := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)
	return pkg.ToolDef{
		Name:         "web.search",
		Description:  "Search the web for information",
		Tier:         pkg.TierTrusted,
		Capabilities: []pkg.Capability{pkg.CapNetworkOut},
		Parameters:   params,
	}
}

type searchParams struct {
	Query string `json:"query"`
}

// Execute runs the web search and returns a ToolResult.
// Errors are reported via ToolResult.Error rather than the returned Go error.
func (ws *WebSearch) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var p searchParams
	if err := json.Unmarshal(call.Parameters, &p); err != nil {
		return pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrBadInput,
				Message: fmt.Sprintf("invalid parameters: %v", err),
				Cause:   err,
			},
		}, nil
	}
	if p.Query == "" {
		return pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrBadInput,
				Message: "query parameter is required and must not be empty",
			},
		}, nil
	}

	searchURL := fmt.Sprintf("%s/?q=%s", ws.baseURL, url.QueryEscape(p.Query))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrInternal,
				Message: fmt.Sprintf("failed to build request: %v", err),
				Cause:   err,
			},
		}, nil
	}
	req.Header.Set("User-Agent", "Chandra/1.0 (+https://github.com/jrimmer/chandra)")

	client := ws.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrTransient,
				Message: fmt.Sprintf("HTTP request failed: %v", err),
				Cause:   err,
			},
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrTransient,
				Message: fmt.Sprintf("search returned HTTP %d", resp.StatusCode),
			},
		}, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrTransient,
				Message: fmt.Sprintf("failed to read response: %v", err),
				Cause:   err,
			},
		}, nil
	}

	content := string(body)
	if len(content) > maxContentLen {
		content = content[:maxContentLen]
	}

	return pkg.ToolResult{
		ID:      call.ID,
		Content: content,
	}, nil
}
