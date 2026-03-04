package mqtt

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jrimmer/chandra/pkg"
)

// EventBus is a minimal interface for publishing messages.
// The full implementation will be provided by the event bus in Phase 10.
type EventBus interface {
	Publish(topic string, payload []byte) error
}

// MQTTPublish implements pkg.Tool to publish MQTT messages via an EventBus.
type MQTTPublish struct {
	bus EventBus
}

// Compile-time assertion.
var _ pkg.Tool = (*MQTTPublish)(nil)

// NewMQTTPublish returns a new MQTTPublish. bus may be nil; Execute will
// return a ToolError in that case rather than panicking.
func NewMQTTPublish(bus EventBus) *MQTTPublish {
	return &MQTTPublish{bus: bus}
}

// Definition returns the ToolDef for the mqtt_publish skill.
func (mp *MQTTPublish) Definition() pkg.ToolDef {
	params := json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string"},"payload":{"type":"string"}},"required":["topic","payload"]}`)
	return pkg.ToolDef{
		Name:         "mqtt_publish",
		Description:  "Publish a message to an MQTT topic via the event bus",
		Tier:         pkg.TierTrusted,
		Capabilities: []pkg.Capability{pkg.CapNetworkOut},
		Parameters:   params,
	}
}

type publishParams struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
}

// Execute parses topic and payload from call.Parameters and delegates to EventBus.Publish.
// All errors are returned via ToolResult.Error; the Go error return is always nil.
func (mp *MQTTPublish) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	if mp.bus == nil {
		return pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrInternal,
				Message: "no event bus configured",
			},
		}, nil
	}

	var p publishParams
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
	if p.Topic == "" {
		return pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrBadInput,
				Message: "topic parameter is required and must not be empty",
			},
		}, nil
	}
	if p.Payload == "" {
		return pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrBadInput,
				Message: "payload parameter is required and must not be empty",
			},
		}, nil
	}

	if err := mp.bus.Publish(p.Topic, []byte(p.Payload)); err != nil {
		return pkg.ToolResult{
			ID: call.ID,
			Error: &pkg.ToolError{
				Kind:    pkg.ErrTransient,
				Message: fmt.Sprintf("publish failed: %v", err),
				Cause:   err,
			},
		}, nil
	}

	return pkg.ToolResult{
		ID:      call.ID,
		Content: fmt.Sprintf("published to topic %q", p.Topic),
	}, nil
}
