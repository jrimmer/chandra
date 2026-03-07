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

	allowBots       bool // if true, messages from other bots are accepted (testing only)
	reactionStatus  bool // show emoji reactions during processing (default true)
	editInPlace     bool // send placeholder then edit with response (default false)

	// seenMsgIDs deduplicates inbound messages against Discord replay on reconnect.
	seenMsgIDs   map[string]struct{}
	seenMsgOrder []string
	seenMsgMax   int

	// delivery tracks active StatusReactionController + TypingHeartbeat per
	// inbound message ID. Cleaned up automatically when the turn completes.
	delivery    sync.Map // map[messageID]*deliveryTracker
	editTargets sync.Map // map[inboundMsgID]placeholderMsgID for edit-in-place
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
			if m.Author == nil || m.Author.ID == s.State.User.ID || (m.Author.Bot && !d.allowBots) {
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
			var refContent, refRole string
			if m.MessageReference != nil && m.MessageReference.MessageID != "" {
				meta["is_reply"] = "true"
				// Resolve referenced message content for reply-context injection.
				// Try discordgo state cache first; fall back to m.ReferencedMessage (gateway-populated).
				var refAuthorID, refBody string
				if refMsg, err := s.State.Message(m.ChannelID, m.MessageReference.MessageID); err == nil {
					refAuthorID = refMsg.Author.ID
					refBody = refMsg.Content
				} else if m.ReferencedMessage != nil {
					if m.ReferencedMessage.Author != nil {
						refAuthorID = m.ReferencedMessage.Author.ID
					}
					refBody = m.ReferencedMessage.Content
				}
				if refAuthorID == botID {
					meta["is_reply_to_bot"] = "true"
					refRole = "assistant"
				} else {
					refRole = "user"
				}
				refContent = refBody
			}

			msg := channels.InboundMessage{
				ID:                m.ID,
				ConversationID:    ComputeConversationID(m.ChannelID, m.Author.ID),
				ChannelID:         m.ChannelID,
				UserID:            m.Author.ID,
				Content:           m.Content,
				Timestamp:         time.Now(),
				Meta:              meta,
				ReferencedContent: refContent,
				ReferencedRole:    refRole,
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
// Returns the Discord message ID of the sent message, or "" on error.
func (d *Discord) Send(ctx context.Context, msg channels.OutboundMessage) (string, error) {
	var sent *discordgo.Message
	var err error
	if msg.ReplyToID != "" {
		ref := &discordgo.MessageReference{MessageID: msg.ReplyToID, ChannelID: msg.ChannelID}
		sent, err = d.session.ChannelMessageSendReply(msg.ChannelID, msg.Content, ref)
	} else {
		sent, err = d.session.ChannelMessageSend(msg.ChannelID, msg.Content)
	}
	if err != nil {
		return "", fmt.Errorf("discord: send message: %w", err)
	}
	return sent.ID, nil
}

// Edit replaces the content of a previously sent message.
func (d *Discord) Edit(ctx context.Context, channelID, messageID, content string) error {
	_, err := d.session.ChannelMessageEdit(channelID, messageID, content)
	if err != nil {
		return fmt.Errorf("discord: edit message: %w", err)
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
// Reactions (if reactionStatus=true): 👀 received, 🤔 thinking, tool emojis.
// Done/Error reactions are intentionally omitted — final state is the message.
// Edit-in-place (if editInPlace=true): placeholder text tracks agent state.
func (d *Discord) OnDeliveryEvent(evt channels.DeliveryEvent) {
	switch evt.Kind {
	case channels.DeliveryReceived:
		if evt.MessageID == "" {
			return
		}
		th := NewTypingHeartbeat(d.session, evt.ChannelID)
		var rc *StatusReactionController
		if d.reactionStatus {
			rc = NewStatusReactionController(d.session, evt.ChannelID, evt.MessageID, nil, nil)
			rc.SetQueued()
		}
		d.delivery.Store(evt.MessageID, &deliveryTracker{reaction: rc, typing: th})

	case channels.DeliveryEditTarget:
		// Store the placeholder message ID so subsequent events can edit it.
		if d.editInPlace && evt.MessageID != "" && evt.EditTargetID != "" {
			d.editTargets.Store(evt.MessageID, evt.EditTargetID)
		}

	case channels.DeliveryThinking:
		if t, ok := d.delivery.Load(evt.MessageID); ok {
			tr := t.(*deliveryTracker)
			if d.reactionStatus && tr.reaction != nil {
				tr.reaction.SetThinking()
			}
		}
		if d.editInPlace {
			d.editPlaceholder(evt.MessageID, evt.ChannelID, "Thinking\u2026")
		}

	case channels.DeliveryToolStart:
		if t, ok := d.delivery.Load(evt.MessageID); ok {
			tr := t.(*deliveryTracker)
			if d.reactionStatus && tr.reaction != nil {
				tr.reaction.SetTool(evt.ToolName)
			}
		}
		if d.editInPlace {
			d.editPlaceholder(evt.MessageID, evt.ChannelID, resolveToolStatusText(evt.ToolName))
		}

	case channels.DeliveryToolEnd:
		// Reactions: stay on tool emoji until next event.
		// Edit-in-place: return to Thinking… after tool result.
		if d.editInPlace {
			d.editPlaceholder(evt.MessageID, evt.ChannelID, "Thinking\u2026")
		}

	case channels.DeliveryDone, channels.DeliveryError:
		// No Done(👍) or Error(😱) reactions — final state is the response text.
		// Just clean up the tracker and remove any in-progress reaction.
		if t, ok := d.delivery.LoadAndDelete(evt.MessageID); ok {
			tr := t.(*deliveryTracker)
			tr.typing.Stop()
			if tr.reaction != nil {
				tr.reaction.Finish()
			}
		}
		d.editTargets.Delete(evt.MessageID)
	}
}

// editPlaceholder edits the placeholder message for the given inbound message ID.
// Non-blocking: called from OnDeliveryEvent which must return immediately.
func (d *Discord) editPlaceholder(inboundMsgID, channelID, text string) {
	if v, ok := d.editTargets.Load(inboundMsgID); ok {
		placeholderID := v.(string)
		go func() {
			if _, err := d.session.ChannelMessageEdit(channelID, placeholderID, text); err != nil {
				slog.Debug("discord: edit placeholder failed", "err", err)
			}
		}()
	}
}

// resolveToolStatusText returns a human-readable status string for the given tool.
func resolveToolStatusText(toolName string) string {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	switch {
	case strings.Contains(lower, "exec") || strings.Contains(lower, "bash") || strings.Contains(lower, "shell"):
		return "Running `exec`\u2026"
	case strings.Contains(lower, "read_file") || lower == "read":
		return "Reading file\u2026"
	case strings.Contains(lower, "write_file") || lower == "write":
		return "Writing file\u2026"
	case strings.Contains(lower, "web_search") || strings.Contains(lower, "search"):
		return "Searching the web\u2026"
	case strings.Contains(lower, "fetch") || strings.Contains(lower, "browse"):
		return "Fetching URL\u2026"
	case lower == "note_context":
		return "Reviewing notes\u2026"
	case lower == "list_intents":
		return "Checking schedule\u2026"
	case lower == "schedule_reminder":
		return "Setting reminder\u2026"
	case lower == "get_current_time":
		return "Checking time\u2026"
	case lower == "read_skill" || lower == "write_skill":
		return "Working on skill\u2026"
	default:
		return "Using `" + toolName + "`\u2026"
	}
}

// SetAllowBots configures whether messages from other bot accounts are accepted.
// Only use during testing to allow test harness bots to send prompts.
func (d *Discord) SetAllowBots(v bool) { d.allowBots = v }

// SetReactionStatus enables or disables emoji status reactions during processing.
func (d *Discord) SetReactionStatus(v bool) { d.reactionStatus = v }

// SetEditInPlace enables or disables the edit-in-place delivery mode.
func (d *Discord) SetEditInPlace(v bool) { d.editInPlace = v }
