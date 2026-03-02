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

// Channel is the interface that all messaging platform adapters must implement.
type Channel interface {
	ID() string
	Listen(ctx context.Context, msgs chan<- InboundMessage) error
	Send(ctx context.Context, msg OutboundMessage) error
	React(ctx context.Context, messageID, emoji string) error
	// SendCheckpoint sends an interactive checkpoint message with approval options.
	// Implementations may render buttons (Discord) or text commands (CLI).
	SendCheckpoint(ctx context.Context, planID string, stepDescription string) error
}
