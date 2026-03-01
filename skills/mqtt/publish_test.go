package mqtt_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/skills/mqtt"
)

func TestMQTTPublish_ImplementsTool(t *testing.T) {
	var _ pkg.Tool = (*mqtt.MQTTPublish)(nil) // compile-time check
}

func TestMQTTPublish_Definition(t *testing.T) {
	mp := mqtt.NewMQTTPublish(nil)
	def := mp.Definition()

	if def.Name != "mqtt.publish" {
		t.Errorf("expected name %q, got %q", "mqtt.publish", def.Name)
	}
	if def.Tier != pkg.TierTrusted {
		t.Errorf("expected TierTrusted, got %d", def.Tier)
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

	var schema map[string]any
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		t.Fatalf("parameters is not valid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected 'properties' in schema")
	}
	for _, field := range []string{"topic", "payload"} {
		if _, ok := props[field]; !ok {
			t.Errorf("expected %q property in schema", field)
		}
	}
}

// mockBus records Publish calls.
type mockBus struct {
	publishedTopic   string
	publishedPayload []byte
	returnErr        error
}

func (m *mockBus) Publish(topic string, payload []byte) error {
	m.publishedTopic = topic
	m.publishedPayload = payload
	return m.returnErr
}

func TestMQTTPublish_Execute(t *testing.T) {
	bus := &mockBus{}
	mp := mqtt.NewMQTTPublish(bus)

	params, _ := json.Marshal(map[string]string{
		"topic":   "home/lights/living",
		"payload": "on",
	})
	call := pkg.ToolCall{ID: "pub-1", Name: "mqtt.publish", Parameters: params}

	result, err := mp.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected tool error: %s", result.Error.Message)
	}
	if result.Content == "" {
		t.Error("expected non-empty Content on success")
	}
	if bus.publishedTopic != "home/lights/living" {
		t.Errorf("expected topic %q, got %q", "home/lights/living", bus.publishedTopic)
	}
	if string(bus.publishedPayload) != "on" {
		t.Errorf("expected payload %q, got %q", "on", string(bus.publishedPayload))
	}
}

func TestMQTTPublish_Execute_NilBus(t *testing.T) {
	mp := mqtt.NewMQTTPublish(nil)

	params, _ := json.Marshal(map[string]string{
		"topic":   "test/topic",
		"payload": "hello",
	})
	call := pkg.ToolCall{ID: "pub-2", Name: "mqtt.publish", Parameters: params}

	result, err := mp.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute must not panic or return Go error on nil bus: %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected ToolResult.Error when bus is nil")
	}
	if !strings.Contains(result.Error.Message, "event bus") {
		t.Errorf("expected 'event bus' in error message, got: %q", result.Error.Message)
	}
}

func TestMQTTPublish_Execute_BusError(t *testing.T) {
	bus := &mockBus{returnErr: errors.New("broker unreachable")}
	mp := mqtt.NewMQTTPublish(bus)

	params, _ := json.Marshal(map[string]string{
		"topic":   "test/topic",
		"payload": "data",
	})
	call := pkg.ToolCall{ID: "pub-3", Name: "mqtt.publish", Parameters: params}

	result, err := mp.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected ToolResult.Error when bus.Publish fails")
	}
	if result.Error.Kind != pkg.ErrTransient {
		t.Errorf("expected ErrTransient, got %v", result.Error.Kind)
	}
}

func TestMQTTPublish_Execute_MissingTopic(t *testing.T) {
	bus := &mockBus{}
	mp := mqtt.NewMQTTPublish(bus)

	params, _ := json.Marshal(map[string]string{"payload": "data"})
	call := pkg.ToolCall{ID: "pub-4", Name: "mqtt.publish", Parameters: params}

	result, err := mp.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected ToolResult.Error when topic is missing")
	}
	if result.Error.Kind != pkg.ErrBadInput {
		t.Errorf("expected ErrBadInput, got %v", result.Error.Kind)
	}
}

func TestMQTTPublish_Execute_MissingPayload(t *testing.T) {
	bus := &mockBus{}
	mp := mqtt.NewMQTTPublish(bus)

	params, _ := json.Marshal(map[string]string{"topic": "test/topic"})
	call := pkg.ToolCall{ID: "pub-5", Name: "mqtt.publish", Parameters: params}

	result, err := mp.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected ToolResult.Error when payload is missing")
	}
	if result.Error.Kind != pkg.ErrBadInput {
		t.Errorf("expected ErrBadInput, got %v", result.Error.Kind)
	}
}

