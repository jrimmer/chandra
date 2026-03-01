package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jrimmer/chandra/pkg"
)

// Compile-time assertions.
var _ pkg.Tool = (*HAGetState)(nil)
var _ pkg.Tool = (*HASetState)(nil)

// HAGetState fetches the current state of a Home Assistant entity.
type HAGetState struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewHAGetState returns a new HAGetState. If client is nil, http.DefaultClient is used.
func NewHAGetState(baseURL, token string, client *http.Client) *HAGetState {
	if client == nil {
		client = http.DefaultClient
	}
	return &HAGetState{
		baseURL:    baseURL,
		token:      token,
		httpClient: client,
	}
}

// Definition returns the ToolDef for homeassistant.get_state.
func (h *HAGetState) Definition() pkg.ToolDef {
	params := json.RawMessage(`{"type":"object","properties":{"entity_id":{"type":"string"}},"required":["entity_id"]}`)
	return pkg.ToolDef{
		Name:         "homeassistant.get_state",
		Description:  "Get the current state of a Home Assistant entity",
		Tier:         pkg.TierTrusted,
		Capabilities: []pkg.Capability{pkg.CapNetworkOut},
		Parameters:   params,
	}
}

type getStateParams struct {
	EntityID string `json:"entity_id"`
}

// Execute fetches entity state from Home Assistant.
func (h *HAGetState) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var p getStateParams
	if err := json.Unmarshal(call.Parameters, &p); err != nil {
		return errorResult(call.ID, pkg.ErrBadInput, fmt.Sprintf("invalid parameters: %v", err), err), nil
	}
	if p.EntityID == "" {
		return errorResult(call.ID, pkg.ErrBadInput, "entity_id parameter is required and must not be empty", nil), nil
	}

	url := fmt.Sprintf("%s/api/states/%s", h.baseURL, p.EntityID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errorResult(call.ID, pkg.ErrInternal, fmt.Sprintf("failed to build request: %v", err), err), nil
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return errorResult(call.ID, pkg.ErrTransient, fmt.Sprintf("request failed: %v", err), err), nil
	}
	defer resp.Body.Close()

	if toolErr := httpStatusToError(call.ID, resp.StatusCode); toolErr != nil {
		return *toolErr, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errorResult(call.ID, pkg.ErrTransient, fmt.Sprintf("failed to read response: %v", err), err), nil
	}

	return pkg.ToolResult{ID: call.ID, Content: string(body)}, nil
}

// HASetState calls a Home Assistant service to change entity state.
type HASetState struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewHASetState returns a new HASetState. If client is nil, http.DefaultClient is used.
func NewHASetState(baseURL, token string, client *http.Client) *HASetState {
	if client == nil {
		client = http.DefaultClient
	}
	return &HASetState{
		baseURL:    baseURL,
		token:      token,
		httpClient: client,
	}
}

// Definition returns the ToolDef for homeassistant.set_state.
func (h *HASetState) Definition() pkg.ToolDef {
	params := json.RawMessage(`{"type":"object","properties":{"domain":{"type":"string"},"service":{"type":"string"},"entity_id":{"type":"string"}},"required":["domain","service"]}`)
	return pkg.ToolDef{
		Name:         "homeassistant.set_state",
		Description:  "Call a Home Assistant service to change entity state",
		Tier:         pkg.TierTrusted,
		Capabilities: []pkg.Capability{pkg.CapNetworkOut},
		Parameters:   params,
	}
}

type setStateParams struct {
	Domain   string `json:"domain"`
	Service  string `json:"service"`
	EntityID string `json:"entity_id,omitempty"`
}

// Execute posts a service call to Home Assistant.
func (h *HASetState) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var p setStateParams
	if err := json.Unmarshal(call.Parameters, &p); err != nil {
		return errorResult(call.ID, pkg.ErrBadInput, fmt.Sprintf("invalid parameters: %v", err), err), nil
	}
	if p.Domain == "" {
		return errorResult(call.ID, pkg.ErrBadInput, "domain parameter is required and must not be empty", nil), nil
	}
	if p.Service == "" {
		return errorResult(call.ID, pkg.ErrBadInput, "service parameter is required and must not be empty", nil), nil
	}

	body := map[string]string{}
	if p.EntityID != "" {
		body["entity_id"] = p.EntityID
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return errorResult(call.ID, pkg.ErrInternal, fmt.Sprintf("failed to marshal request body: %v", err), err), nil
	}

	reqURL := fmt.Sprintf("%s/api/services/%s/%s", h.baseURL, p.Domain, p.Service)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return errorResult(call.ID, pkg.ErrInternal, fmt.Sprintf("failed to build request: %v", err), err), nil
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return errorResult(call.ID, pkg.ErrTransient, fmt.Sprintf("request failed: %v", err), err), nil
	}
	defer resp.Body.Close()

	if toolErr := httpStatusToError(call.ID, resp.StatusCode); toolErr != nil {
		return *toolErr, nil
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return errorResult(call.ID, pkg.ErrTransient, fmt.Sprintf("failed to read response: %v", err), err), nil
	}

	return pkg.ToolResult{ID: call.ID, Content: string(respBody)}, nil
}

// httpStatusToError maps HTTP status codes to ToolResult with ToolError set.
// Returns nil if the status is successful (2xx).
func httpStatusToError(callID string, status int) *pkg.ToolResult {
	if status >= 200 && status < 300 {
		return nil
	}
	var kind pkg.ToolErrorKind
	var msg string
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = pkg.ErrAuth
		msg = fmt.Sprintf("authentication failed (HTTP %d)", status)
	case status == http.StatusNotFound:
		kind = pkg.ErrNotFound
		msg = fmt.Sprintf("entity not found (HTTP %d)", status)
	case status >= 400 && status < 500:
		kind = pkg.ErrBadInput
		msg = fmt.Sprintf("bad request (HTTP %d)", status)
	default:
		kind = pkg.ErrTransient
		msg = fmt.Sprintf("server error (HTTP %d)", status)
	}
	result := pkg.ToolResult{
		ID: callID,
		Error: &pkg.ToolError{
			Kind:    kind,
			Message: msg,
		},
	}
	return &result
}

// errorResult is a convenience constructor for ToolResult with an error.
func errorResult(callID string, kind pkg.ToolErrorKind, msg string, cause error) pkg.ToolResult {
	return pkg.ToolResult{
		ID: callID,
		Error: &pkg.ToolError{
			Kind:    kind,
			Message: msg,
			Cause:   cause,
		},
	}
}
