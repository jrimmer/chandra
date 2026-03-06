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

// Compile-time assertions.
var _ channels.Channel         = (*Discord)(nil)
var _ channels.DeliveryUpdater = (*Discord)(nil)

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
	msgChanMap map[string]string  // messageID → channelID for React lookups
	msgIDOrder []string           // insertion-order tracking for FIFO eviction
	msgChanMax int                // maximum entries in msgChanMap (default 10000)
	done       bool               // true after shutdown; guards against send on closed channel
	connState  channels.ConnectionState // current connection state

	// handlerOnce ensures AddHandler is only called once across Listen calls.
	// discordgo accumulates handlers — calling Listen after a supervisor reconnect
	// would register duplicate handlers and cause every message to be processed twice.
	handlerOnce sync.Once

	// seenMsgIDs deduplicates inbound messages against Discord replay on reconnect.
	seenMsgIDs   map[string]struct{}
	seenMsgOrder []string
	seenMsgMax   int

	// delivery tracks active StatusReactionController + TypingHeartbeat per
	// inbound message ID. Cleaned up automatically when the turn completes.
	delivery sync.Map // map[messageID]*deliveryTracker
}

// deliveryTracker holds the active reaction controller and typing heartbeat
// for a single in-flight agent turn.
type deliveryTracker struct {
	reaction *StatusReactionController
	typing   *TypingHeartbeat
}

// NewDiscord constructs a Discord adapter with the given bot token and the list
// of channel IDs to accept messages from. The Discord session is created but
// NOT opened here — the connection is established in Listen so that tests can
// construct a Discord without a live network connection.
func NewDiscord(token string, channelIDs []string) (*Discord, error) {
	if token == "" {
		return nil, errors.New("discord: token must not be empty")
	}
	token = normaliseBotToken(token)

	session, err := discordgo.New(token)
	if err != nil {
		return nil, fmt.Errorf("discord: create session: %w", err)
	}

	// Set required Gateway intents.
	// IntentGuildMessages: receive MESSAGE_CREATE events in guild channels.
	// IntentMessageContent: receive message body (privileged intent; must be
	// enabled in the Discord Developer Portal under Privileged Gateway Intents).
	session.Identify.Intents = discordgo.IntentGuildMessages | discordgo.IntentMessageContent

	allowed := make(map[string]struct{}, len(channelIDs))
	for _, id := range channelIDs {
		allowed[id] = struct{}{}
	}

	return &Discord{
		session:      session,
		channelIDs:   allowed,
		msgChanMap:   make(map[string]string),
		msgIDOrder:   make([]string, 0, 100),
		msgChanMax:   10000,
		connState:    channels.StateUnknown,
		seenMsgIDs:   make(map[string]struct{}),
		seenMsgOrder: make([]string, 0, 128),
		seenMsgMax:   1024,
	}, nil
}

// ID returns the adapter identifier for this Discord channel.
func (d *Discord) ID() string { return "discord" }

// Listen registers a MessageCreate handler, opens the Discord WebSocket
// connection, sets the bot status to Online, and writes InboundMessages to
// msgs. Listen blocks until ctx is cancelled, then closes the Discord session.
// The caller owns the msgs channel and is responsible for draining it.
func (d *Discord) Listen(ctx context.Context, msgs chan<- channels.InboundMessage) error {
	d.handlerOnce.Do(func() {
	d.session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
			// Ignore messages from the bot itself or any other bot.
			// Bots should not be responding to each other in a loop.
			if m.Author == nil || m.Author.ID == s.State.User.ID || m.Author.Bot {
				return
			}

			// Only process messages from configured channels.
			if _, ok := d.channelIDs[m.ChannelID]; !ok {
				return
			}

			// Deduplicate: Discord replays events on reconnect. Drop already-seen IDs.
			d.mu.Lock()
			if _, seen := d.seenMsgIDs[m.ID]; seen {
				d.mu.Unlock()
				slog.Debug("discord: dropping duplicate message", "id", m.ID)
				return
			}
			d.seenMsgIDs[m.ID] = struct{}{}
			d.seenMsgOrder = append(d.seenMsgOrder, m.ID)
			for len(d.seenMsgOrder) > d.seenMsgMax {
				delete(d.seenMsgIDs, d.seenMsgOrder[0])
				d.seenMsgOrder = d.seenMsgOrder[1:]
			}
			d.mu.Unlock()

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

			meta := map[string]any{}

			// Prompt injection detection.
			if checkSuspicious(m.Content) {
				slog.Warn("discord: suspicious message detected",
					"message_id", m.ID,
					"channel_id", m.ChannelID,
					"user_id", m.Author.ID,
				)
				meta["suspicious"] = "true"
			}

			// Directedness signals for the routing layer.
			// bot_mentioned: message explicitly @-mentions this bot.
			botID := s.State.User.ID
			for _, u := range m.Mentions {
				if u.ID == botID {
					meta["bot_mentioned"] = "true"
					break
				}
			}
			// is_reply: message is a reply to any prior message (not just bot messages).
			// The routing layer uses this in combination with bot_mentioned.
			if m.MessageReference != nil && m.MessageReference.MessageID != "" {
				meta["is_reply"] = "true"
				// is_reply_to_bot: referenced message is one we sent (in msgChanMap or known bot ID).
				// We only have our own outbound message IDs if we track them; for now use the
				// simpler signal: check if the referenced message author is the bot via cache.
				// discordgo caches recent messages in session.State — try that first.
				if refMsg, err := s.State.Message(m.ChannelID, m.MessageReference.MessageID); err == nil {
					if refMsg.Author != nil && refMsg.Author.ID == botID {
						meta["is_reply_to_bot"] = "true"
					}
				}
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

		// Track connect/disconnect events for health reporting.
		d.session.AddHandler(func(s *discordgo.Session, e *discordgo.Connect) {
			d.mu.Lock()
			d.connState = channels.StateConnected
			d.mu.Unlock()
			slog.Info("discord: connected")
		})
		d.session.AddHandler(func(s *discordgo.Session, e *discordgo.Disconnect) {
			d.mu.Lock()
			if !d.done {
				d.connState = channels.StateReconnecting
			}
			d.mu.Unlock()
			slog.Warn("discord: disconnected; discordgo will attempt reconnect")
		})

	})

	if err := d.session.Open(); err != nil {
		return fmt.Errorf("discord: open session: %w", err)
	}

	d.mu.Lock()
	d.connState = channels.StateConnected
	d.mu.Unlock()

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

// Reconnect closes the current Discord session and opens a new one.
// Called by the ChannelSupervisor when it needs to re-establish the connection.
func (d *Discord) Reconnect(ctx context.Context) error {
	d.mu.Lock()
	d.connState = channels.StateReconnecting
	d.mu.Unlock()

	// Close the existing session (ignore error — we're tearing down anyway).
	_ = d.session.Close()

	// Re-open the WebSocket connection.
	if err := d.session.Open(); err != nil {
		d.mu.Lock()
		d.connState = channels.StateFailed
		d.mu.Unlock()
		return fmt.Errorf("discord: reconnect open: %w", err)
	}

	d.mu.Lock()
	d.connState = channels.StateConnected
	d.mu.Unlock()
	slog.Info("discord: reconnected successfully")
	return nil
}

// ConnectionState returns the current connection state for health reporting.
func (d *Discord) ConnectionState() channels.ConnectionState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.connState
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

// OnDeliveryEvent implements channels.DeliveryUpdater. It is called by the
// agent loop on each pipeline status change and must return immediately.
//
// On DeliveryReceived a new StatusReactionController and TypingHeartbeat are
// created for the message. Subsequent events drive the reaction state machine.
// On DeliveryDone or DeliveryError the tracker is cleaned up.
func (d *Discord) OnDeliveryEvent(evt channels.DeliveryEvent) {
	switch evt.Kind {
	case channels.DeliveryReceived:
		// Create tracker if the message ID is present.
		if evt.MessageID == "" {
			return
		}
		rc := NewStatusReactionController(d.session, evt.ChannelID, evt.MessageID, nil, nil)
		th := NewTypingHeartbeat(d.session, evt.ChannelID)
		tracker := &deliveryTracker{reaction: rc, typing: th}
		d.delivery.Store(evt.MessageID, tracker)
		rc.SetQueued()

	case channels.DeliveryThinking:
		if t, ok := d.delivery.Load(evt.MessageID); ok {
			t.(*deliveryTracker).reaction.SetThinking()
		}

	case channels.DeliveryToolStart:
		if t, ok := d.delivery.Load(evt.MessageID); ok {
			t.(*deliveryTracker).reaction.SetTool(evt.ToolName)
		}

	case channels.DeliveryToolEnd:
		// No state change on ToolEnd — controller stays in the tool emoji
		// until the next ToolStart or Done/Error. Stall timers reset automatically
		// on the next scheduleEmoji call.

	case channels.DeliveryDone:
		if t, ok := d.delivery.LoadAndDelete(evt.MessageID); ok {
			tr := t.(*deliveryTracker)
			tr.typing.Stop()
			tr.reaction.SetDone()
		}

	case channels.DeliveryError:
		if t, ok := d.delivery.LoadAndDelete(evt.MessageID); ok {
			tr := t.(*deliveryTracker)
			tr.typing.Stop()
			tr.reaction.SetError()
		}
	}
}
