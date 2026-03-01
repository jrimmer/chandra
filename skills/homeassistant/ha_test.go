package homeassistant_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/skills/homeassistant"
)

func TestHA_ImplementsTool(t *testing.T) {
	var _ pkg.Tool = (*homeassistant.HAGetState)(nil)
	var _ pkg.Tool = (*homeassistant.HASetState)(nil)
}

func TestHA_GetState_Definition(t *testing.T) {
	h := homeassistant.NewHAGetState("http://localhost:8123", "token", nil)
	def := h.Definition()

	if def.Name != "homeassistant.get_state" {
		t.Errorf("expected name %q, got %q", "homeassistant.get_state", def.Name)
	}
	if def.Tier != pkg.TierTrusted {
		t.Errorf("expected TierTrusted, got %d", def.Tier)
	}
	assertHasCapNetworkOut(t, def)
	assertParamRequired(t, def.Parameters, "entity_id")
}

func TestHA_SetState_Definition(t *testing.T) {
	h := homeassistant.NewHASetState("http://localhost:8123", "token", nil)
	def := h.Definition()

	if def.Name != "homeassistant.set_state" {
		t.Errorf("expected name %q, got %q", "homeassistant.set_state", def.Name)
	}
	if def.Tier != pkg.TierTrusted {
		t.Errorf("expected TierTrusted, got %d", def.Tier)
	}
	assertHasCapNetworkOut(t, def)
	assertParamRequired(t, def.Parameters, "domain")
	assertParamRequired(t, def.Parameters, "service")
}

func TestHA_GetState_Execute_MockAPI(t *testing.T) {
	const mockState = `{"entity_id":"light.living_room","state":"on","attributes":{}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/api/states/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockState))
	}))
	defer srv.Close()

	h := homeassistant.NewHAGetState(srv.URL, "test-token", srv.Client())
	params, _ := json.Marshal(map[string]string{"entity_id": "light.living_room"})
	call := pkg.ToolCall{ID: "get-1", Name: "homeassistant.get_state", Parameters: params}

	result, err := h.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected tool error: %s", result.Error.Message)
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
	if !strings.Contains(result.Content, "light.living_room") {
		t.Errorf("expected entity_id in content, got: %s", result.Content)
	}
}

func TestHA_SetState_Execute_MockAPI(t *testing.T) {
	const mockResponse = `[{"entity_id":"light.living_room","state":"off"}]`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/api/services/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer srv.Close()

	h := homeassistant.NewHASetState(srv.URL, "test-token", srv.Client())
	params, _ := json.Marshal(map[string]string{
		"domain":    "light",
		"service":   "turn_off",
		"entity_id": "light.living_room",
	})
	call := pkg.ToolCall{ID: "set-1", Name: "homeassistant.set_state", Parameters: params}

	result, err := h.Execute(context.Background(), call)
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

func TestHA_GetState_MissingEntityID(t *testing.T) {
	h := homeassistant.NewHAGetState("http://localhost:8123", "token", nil)
	params, _ := json.Marshal(map[string]string{})
	call := pkg.ToolCall{ID: "get-2", Name: "homeassistant.get_state", Parameters: params}

	result, err := h.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == nil {
		t.Error("expected ToolResult.Error when entity_id is missing")
	}
	if result.Error.Kind != pkg.ErrBadInput {
		t.Errorf("expected ErrBadInput, got %v", result.Error.Kind)
	}
}

func TestHA_SetState_MissingDomain(t *testing.T) {
	h := homeassistant.NewHASetState("http://localhost:8123", "token", nil)
	params, _ := json.Marshal(map[string]string{"service": "turn_on"})
	call := pkg.ToolCall{ID: "set-2", Name: "homeassistant.set_state", Parameters: params}

	result, err := h.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == nil {
		t.Error("expected ToolResult.Error when domain is missing")
	}
	if result.Error.Kind != pkg.ErrBadInput {
		t.Errorf("expected ErrBadInput, got %v", result.Error.Kind)
	}
}

func TestHA_GetState_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	h := homeassistant.NewHAGetState(srv.URL, "bad-token", srv.Client())
	params, _ := json.Marshal(map[string]string{"entity_id": "sensor.temp"})
	call := pkg.ToolCall{ID: "get-3", Name: "homeassistant.get_state", Parameters: params}

	result, err := h.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == nil {
		t.Error("expected ToolResult.Error on HTTP 401")
	}
	if result.Error.Kind != pkg.ErrAuth {
		t.Errorf("expected ErrAuth, got %v", result.Error.Kind)
	}
}

// helpers

func assertHasCapNetworkOut(t *testing.T, def pkg.ToolDef) {
	t.Helper()
	for _, c := range def.Capabilities {
		if c == pkg.CapNetworkOut {
			return
		}
	}
	t.Errorf("expected CapNetworkOut in capabilities, got %v", def.Capabilities)
}

func assertParamRequired(t *testing.T, raw json.RawMessage, field string) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parameters is not valid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected 'properties' in schema")
	}
	if _, ok := props[field]; !ok {
		t.Errorf("expected %q property in schema", field)
	}
}
