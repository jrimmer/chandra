// Package channels defines the Channel interface and message types used for
// bidirectional communication between the agent and external messaging platforms.
package channels

import "context"

// InboundMessage represents a message received from an external channel.
type InboundMessage struct {
	ID             string            // message ID from channel
	ConversationID string            // stable session ID
	ChannelID      string            // source channel
	UserID         string
	Content        string
	Metadata       map[string]string // e.g., "suspicious": "true"
}

// OutboundMessage represents a message the agent wants to send to a channel.
type OutboundMessage struct {
	ChannelID string // target channel for routing
	Content   string
	ReplyToID string // optional, for threading
}

// Channel is the interface that all messaging platform adapters must implement.
type Channel interface {
	Listen(ctx context.Context) (<-chan InboundMessage, error)
	Send(ctx context.Context, msg OutboundMessage) error
	React(ctx context.Context, messageID, emoji string) error
}
