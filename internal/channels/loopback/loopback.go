// Package loopback provides a Channel implementation that routes messages
// through in-memory Go channels. Intended exclusively for testing.
//
// Usage:
//
//	ch := loopback.New("test", 32)
//	loop.Run(ctx, sess, msg)             // agent calls ch.Send internally
//	sent := ch.DrainSent()               // assert what the agent said
package loopback

import (
	"context"

	"github.com/jrimmer/chandra/internal/channels"
)

// Reaction records a React() call.
type Reaction struct {
	MessageID string
	Emoji     string
}

// Edit records an Edit() call.
type Edit struct {
	ChannelID string
	MessageID string
	Content   string
}

// Channel implements channels.Channel using buffered Go channels.
// All methods are safe for concurrent use.
type Channel struct {
	id       string
	outbound chan channels.OutboundMessage
	reacts   chan Reaction
	edits    chan Edit
}

// New returns a loopback Channel with the given buffer size applied to all
// internal channels.
func New(id string, bufSize int) *Channel {
	return &Channel{
		id:       id,
		outbound: make(chan channels.OutboundMessage, bufSize),
		reacts:   make(chan Reaction, bufSize),
		edits:    make(chan Edit, bufSize),
	}
}

// DrainSent returns all outbound messages currently buffered (non-blocking).
func (c *Channel) DrainSent() []channels.OutboundMessage {
	var out []channels.OutboundMessage
	for {
		select {
		case m := <-c.outbound:
			out = append(out, m)
		default:
			return out
		}
	}
}

// DrainReactions returns all reactions currently buffered (non-blocking).
func (c *Channel) DrainReactions() []Reaction {
	var out []Reaction
	for {
		select {
		case r := <-c.reacts:
			out = append(out, r)
		default:
			return out
		}
	}
}

// DrainEdits returns all edits currently buffered (non-blocking).
func (c *Channel) DrainEdits() []Edit {
	var out []Edit
	for {
		select {
		case e := <-c.edits:
			out = append(out, e)
		default:
			return out
		}
	}
}

// Sent exposes the outbound channel for blocking reads.
func (c *Channel) Sent() <-chan channels.OutboundMessage { return c.outbound }

// ─── channels.Channel ────────────────────────────────────────────────────────

func (c *Channel) ID() string { return c.id }

func (c *Channel) Listen(_ context.Context, _ chan<- channels.InboundMessage) error {
	return nil // tests drive the loop directly via loop.Run
}

func (c *Channel) Send(_ context.Context, msg channels.OutboundMessage) (string, error) {
	c.outbound <- msg
	return "loopback-" + c.id, nil
}

func (c *Channel) Edit(_ context.Context, channelID, messageID, content string) error {
	c.edits <- Edit{ChannelID: channelID, MessageID: messageID, Content: content}
	return nil
}

func (c *Channel) React(_ context.Context, messageID, emoji string) error {
	c.reacts <- Reaction{MessageID: messageID, Emoji: emoji}
	return nil
}

func (c *Channel) SendCheckpoint(_ context.Context, _, _ string) error { return nil }
func (c *Channel) Reconnect(_ context.Context) error                   { return nil }
func (c *Channel) ConnectionState() channels.ConnectionState            { return channels.StateConnected }

var _ channels.Channel = (*Channel)(nil)
