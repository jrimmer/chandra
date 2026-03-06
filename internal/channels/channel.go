// Package channels defines the Channel interface and message types used for
// bidirectional communication between the agent and external messaging platforms.
package channels

import (
	"context"
	"time"
)

// InboundMessage represents a message received from an external channel.
type InboundMessage struct {
	ID             string         // message ID from channel
	ConversationID string         // stable session ID: hash(channel_id + user_id), set by channel
	ChannelID      string         // source channel (kept for routing; not in design spec)
	UserID         string
	Content        string
	Timestamp      time.Time      // time the message was received
	Meta           map[string]any // e.g., map[string]any{"suspicious": "true"}
}

// OutboundMessage represents a message the agent wants to send to a channel.
type OutboundMessage struct {
	ChannelID string         // target channel for routing
	UserID    string
	Content   string
	ReplyToID string         // optional, for threading
	Meta      map[string]any
}

// ConnectionState represents the current lifecycle state of a channel connection.
type ConnectionState int

const (
	StateUnknown      ConnectionState = iota
	StateConnected                    // WebSocket open, receiving events
	StateReconnecting                 // backoff loop active, attempting reconnect
	StateFailed                       // exhausted retries or unrecoverable error
)

func (s ConnectionState) String() string {
	switch s {
	case StateConnected:
		return "connected"
	case StateReconnecting:
		return "reconnecting"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// ─── Progressive delivery ────────────────────────────────────────────────────

// DeliveryEventKind identifies the phase of the agent pipeline.
type DeliveryEventKind string

const (
	DeliveryReceived   DeliveryEventKind = "received"    // message entered pipeline
	DeliveryThinking   DeliveryEventKind = "thinking"    // LLM call starting
	DeliveryToolStart  DeliveryEventKind = "tool_start"  // tool execution beginning
	DeliveryToolEnd    DeliveryEventKind = "tool_end"    // tool execution finished
	DeliveryDone       DeliveryEventKind = "done"        // response sent successfully
	DeliveryError      DeliveryEventKind = "error"       // pipeline failed
	DeliveryEditTarget DeliveryEventKind = "edit_target" // placeholder message ID for edit-in-place
)

// DeliveryEvent carries status information from the agent pipeline to the channel adapter.
type DeliveryEvent struct {
	Kind         DeliveryEventKind
	MessageID    string // original inbound message ID (for reactions)
	ChannelID    string
	Detail       string // human-readable description, e.g. "Checking weather"
	ToolName     string // populated for DeliveryToolStart / DeliveryToolEnd
	EditTargetID string // placeholder message ID (populated for DeliveryEditTarget)
}

// DeliveryUpdater is an optional interface that channel adapters can implement
// to receive real-time pipeline status events. Calls must be non-blocking.
type DeliveryUpdater interface {
	OnDeliveryEvent(evt DeliveryEvent)
}

// ─── Channel interface ────────────────────────────────────────────────────────

// Channel is the interface that all messaging platform adapters must implement.
type Channel interface {
	ID() string
	Listen(ctx context.Context, msgs chan<- InboundMessage) error
	Send(ctx context.Context, msg OutboundMessage) (string, error)
	// Edit replaces the content of a previously sent message identified by
	// messageID in channelID. Returns an error if the platform does not support
	// message editing or if the message no longer exists.
	Edit(ctx context.Context, channelID, messageID, content string) error
	React(ctx context.Context, messageID, emoji string) error
	// SendCheckpoint sends an interactive checkpoint message with approval options.
	// Implementations may render buttons (Discord) or text commands (CLI).
	SendCheckpoint(ctx context.Context, planID string, stepDescription string) error
	// Reconnect closes and reopens the underlying transport connection.
	// Called by the ChannelSupervisor after a connection failure.
	Reconnect(ctx context.Context) error
	// ConnectionState returns the current connection state for health reporting.
	ConnectionState() ConnectionState
}
