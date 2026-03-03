package discord

import (
	"context"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
)

// VerifyOptions controls the Hello World loop test.
type VerifyOptions struct {
	TimeoutSec int
	Message    string
}

// DefaultVerifyOptions returns sane defaults for the loop test.
func DefaultVerifyOptions() VerifyOptions {
	return VerifyOptions{
		TimeoutSec: 120,
		Message:    "👋 Hi! I'm Chandra. Reply to this message to complete setup.",
	}
}

// VerifyResult is returned by RunLoopTest.
type VerifyResult struct {
	ReplyUserID   string
	ReplyUsername string
	MessageID     string // the sent message ID
}

// RunLoopTest sends a message to channelID, waits for a reply (matched by
// parent message ID), and returns the replying user's ID and name.
// The caller is responsible for writing the result to the DB.
//
// token is the Discord bot token.
// channelID is the Discord channel ID to send the test message to.
func RunLoopTest(ctx context.Context, token, channelID string, opts VerifyOptions) (*VerifyResult, error) {
	sess, err := discordgo.New(token)
	if err != nil {
		return nil, fmt.Errorf("discord: create session: %w", err)
	}

	sess.Identify.Intents = discordgo.IntentGuildMessages | discordgo.IntentMessageContent

	replyCh := make(chan *VerifyResult, 1)
	var sentMsgID string

	// Register handler before opening so we don't miss fast replies.
	sess.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author == nil || m.Author.Bot {
			return
		}
		// Match reply to our specific sent message.
		if m.MessageReference != nil && m.MessageReference.MessageID == sentMsgID {
			select {
			case replyCh <- &VerifyResult{
				ReplyUserID:   m.Author.ID,
				ReplyUsername: m.Author.Username,
				MessageID:     sentMsgID,
			}:
			default:
			}
		}
	})

	if err := sess.Open(); err != nil {
		return nil, fmt.Errorf("discord: open connection: %w", err)
	}
	defer sess.Close()

	sent, err := sess.ChannelMessageSend(channelID, opts.Message)
	if err != nil {
		return nil, fmt.Errorf("discord: send message: %w", err)
	}
	sentMsgID = sent.ID

	timeout := time.Duration(opts.TimeoutSec) * time.Second
	select {
	case result := <-replyCh:
		return result, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("no reply received within %s", timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
