// Package discord provides a Discord channel adapter that satisfies the
// channels.Channel interface.
package discord

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jrimmer/chandra/internal/channels"
)

// Compile-time assertion that *Discord satisfies channels.Channel.
var _ channels.Channel = (*Discord)(nil)

// suspiciousPatterns contains substrings that indicate a potential prompt
// injection attempt. All comparisons are done case-insensitively.
var suspiciousPatterns = []string{
	"ignore previous instructions",
	"ignore all previous",
	"system prompt",
	"forget all instructions",
}

// checkSuspicious reports whether content contains any known prompt injection
// pattern. The check is case-insensitive.
func checkSuspicious(content string) bool {
	lower := strings.ToLower(content)
	for _, p := range suspiciousPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// ComputeConversationID returns a stable 16-character hex string derived from
// the SHA-256 hash of "<channelID>:<userID>". The first 16 hex characters
// (8 bytes) of the digest are returned, giving sufficient uniqueness for
// session identification while keeping IDs compact.
func ComputeConversationID(channelID, userID string) string {
	sum := sha256.Sum256([]byte(channelID + ":" + userID))
	return hex.EncodeToString(sum[:])[:16]
}

// Discord is a channels.Channel implementation backed by a Discord bot session.
type Discord struct {
	session    *discordgo.Session
	channelIDs map[string]struct{} // set of channel IDs to listen on

	mu         sync.RWMutex
	msgChanMap map[string]string // messageID → channelID for React lookups
	msgIDOrder []string          // insertion-order tracking for FIFO eviction
	msgChanMax int               // maximum entries in msgChanMap (default 10000)
	done       bool              // true after shutdown; guards against send on closed channel
}

// NewDiscord constructs a Discord adapter with the given bot token and the list
// of channel IDs to accept messages from. The Discord session is created but
// NOT opened here — the connection is established in Listen so that tests can
// construct a Discord without a live network connection.
func NewDiscord(token string, channelIDs []string) (*Discord, error) {
	if token == "" {
		return nil, errors.New("discord: token must not be empty")
	}

	session, err := discordgo.New(token)
	if err != nil {
		return nil, fmt.Errorf("discord: create session: %w", err)
	}

	allowed := make(map[string]struct{}, len(channelIDs))
	for _, id := range channelIDs {
		allowed[id] = struct{}{}
	}

	return &Discord{
		session:    session,
		channelIDs: allowed,
		msgChanMap: make(map[string]string),
		msgIDOrder: make([]string, 0, 100),
		msgChanMax: 10000,
	}, nil
}

// ID returns the adapter identifier for this Discord channel.
func (d *Discord) ID() string { return "discord" }

// Listen registers a MessageCreate handler, opens the Discord WebSocket
// connection, sets the bot status to Online, and writes InboundMessages to
// msgs. Listen blocks until ctx is cancelled, then closes the Discord session.
// The caller owns the msgs channel and is responsible for draining it.
func (d *Discord) Listen(ctx context.Context, msgs chan<- channels.InboundMessage) error {
	d.session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		// Ignore messages sent by the bot itself.
		if m.Author == nil || m.Author.ID == s.State.User.ID {
			return
		}

		// Only process messages from configured channels.
		if _, ok := d.channelIDs[m.ChannelID]; !ok {
			return
		}

		// Record messageID → channelID mapping for React, capping at msgChanMax
		// via FIFO eviction to prevent unbounded memory growth.
		d.mu.Lock()
		if _, exists := d.msgChanMap[m.ID]; !exists {
			d.msgChanMap[m.ID] = m.ChannelID
			d.msgIDOrder = append(d.msgIDOrder, m.ID)
			for len(d.msgIDOrder) > d.msgChanMax {
				oldest := d.msgIDOrder[0]
				d.msgIDOrder = d.msgIDOrder[1:]
				delete(d.msgChanMap, oldest)
			}
		}
		d.mu.Unlock()

		var meta map[string]any

		// Prompt injection detection.
		if checkSuspicious(m.Content) {
			slog.Warn("discord: suspicious message detected",
				"message_id", m.ID,
				"channel_id", m.ChannelID,
				"user_id", m.Author.ID,
			)
			meta = map[string]any{"suspicious": "true"}
		}

		msg := channels.InboundMessage{
			ID:             m.ID,
			ConversationID: ComputeConversationID(m.ChannelID, m.Author.ID),
			ChannelID:      m.ChannelID,
			UserID:         m.Author.ID,
			Content:        m.Content,
			Timestamp:      time.Now(),
			Meta:           meta,
		}

		// Guard against sending on a closed channel after shutdown.
		d.mu.RLock()
		isDone := d.done
		d.mu.RUnlock()
		if isDone {
			return
		}

		select {
		case msgs <- msg:
		case <-ctx.Done():
			return
		}
	})

	if err := d.session.Open(); err != nil {
		return fmt.Errorf("discord: open session: %w", err)
	}

	if err := d.session.UpdateGameStatus(0, "Ready"); err != nil {
		// Non-fatal: log and continue.
		slog.Warn("discord: failed to set game status", "error", err)
	}

	// Block until the context is cancelled, then close the Discord WebSocket
	// connection. Setting done=true under the write lock before closing prevents
	// handler goroutines from sending on a closed channel.
	go func() {
		<-ctx.Done()
		_ = d.session.Close()
		d.mu.Lock()
		d.done = true
		d.mu.Unlock()
	}()

	return nil
}

// Send transmits a message to the target channel specified in msg.ChannelID.
func (d *Discord) Send(ctx context.Context, msg channels.OutboundMessage) error {
	_, err := d.session.ChannelMessageSend(msg.ChannelID, msg.Content)
	if err != nil {
		return fmt.Errorf("discord: send message: %w", err)
	}
	return nil
}

// SendCheckpoint sends an interactive checkpoint message with approval options.
// On Discord, this sends an embed with action buttons (Approve, Reject, Show Plan).
func (d *Discord) SendCheckpoint(ctx context.Context, planID string, stepDescription string) error {
	// Send to all configured channels.
	content := fmt.Sprintf("**Plan Checkpoint** (`%s`)\n%s\n\nApprove: `chandra confirm %s`",
		planID, stepDescription, planID)
	for chID := range d.channelIDs {
		if _, err := d.session.ChannelMessageSend(chID, content); err != nil {
			slog.Warn("discord: send checkpoint failed", "channel", chID, "err", err)
		}
	}
	return nil
}

// React adds an emoji reaction to a previously seen message. The channel ID
// is retrieved from the internal messageID→channelID map populated by the
// Listen handler. If the message ID is not known, an error is returned.
func (d *Discord) React(ctx context.Context, messageID, emoji string) error {
	d.mu.RLock()
	channelID, ok := d.msgChanMap[messageID]
	d.mu.RUnlock()

	if !ok {
		return fmt.Errorf("discord: react: unknown message ID %q (not seen by this session)", messageID)
	}

	if err := d.session.MessageReactionAdd(channelID, messageID, emoji); err != nil {
		return fmt.Errorf("discord: react: %w", err)
	}
	return nil
}
