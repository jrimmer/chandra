package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/skills/web"
)

func TestWebSearch_ImplementsTool(t *testing.T) {
	var _ pkg.Tool = (*web.WebSearch)(nil) // compile-time check
}

func TestWebSearch_Definition(t *testing.T) {
	ws := web.NewWebSearch()
	def := ws.Definition()

	if def.Name != "web_search" {
		t.Errorf("expected name %q, got %q", "web_search", def.Name)
	}
	if def.Tier != pkg.TierTrusted {
		t.Errorf("expected TierTrusted (%d), got %d", pkg.TierTrusted, def.Tier)
	}
	if len(def.Capabilities) == 0 {
		t.Fatal("expected at least one capability")
	}
	found := false
	for _, c := range def.Capabilities {
		if c == pkg.CapNetworkOut {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected CapNetworkOut in capabilities, got %v", def.Capabilities)
	}
	if def.Description == "" {
		t.Error("expected non-empty description")
	}
	// Verify parameters JSON schema has "query" as required field
	var schema map[string]any
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		t.Fatalf("parameters is not valid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected 'properties' in parameters schema")
	}
	if _, ok := props["query"]; !ok {
		t.Error("expected 'query' property in parameters schema")
	}
}

func TestWebSearch_Execute_MockHTTP(t *testing.T) {
	const mockBody = `<html><body>Result 1: Go programming. Result 2: Golang tutorial.</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		q := r.URL.Query().Get("q")
		if q == "" {
			t.Error("expected non-empty q parameter")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockBody))
	}))
	defer srv.Close()

	ws := web.NewWebSearch()
	ws.SetBaseURL(srv.URL)
	ws.SetHTTPClient(srv.Client())

	params, _ := json.Marshal(map[string]string{"query": "golang"})
	call := pkg.ToolCall{
		ID:         "call-1",
		Name:       "web_search",
		Parameters: params,
	}

	result, err := ws.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected tool error: %s", result.Error.Message)
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestWebSearch_Execute_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ws := web.NewWebSearch()
	ws.SetBaseURL(srv.URL)
	ws.SetHTTPClient(srv.Client())

	params, _ := json.Marshal(map[string]string{"query": "fail"})
	call := pkg.ToolCall{
		ID:         "call-2",
		Name:       "web_search",
		Parameters: params,
	}

	result, err := ws.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute must not return a Go error; errors go in ToolResult.Error: %v", err)
	}
	if result.Error == nil {
		t.Error("expected ToolResult.Error to be set on HTTP 500")
	}
}

func TestWebSearch_Execute_MissingQuery(t *testing.T) {
	ws := web.NewWebSearch()

	params, _ := json.Marshal(map[string]string{})
	call := pkg.ToolCall{
		ID:         "call-3",
		Name:       "web_search",
		Parameters: params,
	}

	result, err := ws.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute must not return a Go error: %v", err)
	}
	if result.Error == nil {
		t.Error("expected ToolResult.Error when query is missing")
	}
	if result.Error.Kind != pkg.ErrBadInput {
		t.Errorf("expected ErrBadInput, got %v", result.Error.Kind)
	}
}

func TestWebSearch_Execute_TruncatesLongResponse(t *testing.T) {
	longBody := strings.Repeat("x", 5000)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(longBody))
	}))
	defer srv.Close()

	ws := web.NewWebSearch()
	ws.SetBaseURL(srv.URL)
	ws.SetHTTPClient(srv.Client())

	params, _ := json.Marshal(map[string]string{"query": "long"})
	call := pkg.ToolCall{
		ID:         "call-4",
		Name:       "web_search",
		Parameters: params,
	}

	result, err := ws.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected tool error: %s", result.Error.Message)
	}
	if len(result.Content) > 2000 {
		t.Errorf("expected content truncated to 2000 chars, got %d", len(result.Content))
	}
}
