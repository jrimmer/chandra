package discord

import (
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	typingKeepaliveInterval    = 3 * time.Second
	typingMaxDuration          = 60 * time.Second
	typingMaxConsecutiveFails  = 2
)

// TypingHeartbeat fires a Discord typing indicator immediately and then
// re-sends it every 3 seconds until Stop() is called or the 60s cap is hit.
// Discord's typing indicator persists for ~10s, so 3s intervals keep it alive.
type TypingHeartbeat struct {
	session   *discordgo.Session
	channelID string

	once sync.Once
	stop chan struct{}
	wg   sync.WaitGroup
}

// NewTypingHeartbeat creates and immediately starts the typing heartbeat.
// Call Stop() when the response has been sent.
func NewTypingHeartbeat(session *discordgo.Session, channelID string) *TypingHeartbeat {
	h := &TypingHeartbeat{
		session:   session,
		channelID: channelID,
		stop:      make(chan struct{}),
	}
	h.wg.Add(1)
	go h.run()
	return h
}

// Stop halts the heartbeat. Safe to call multiple times.
func (h *TypingHeartbeat) Stop() {
	h.once.Do(func() { close(h.stop) })
}

// Wait blocks until the heartbeat goroutine exits. Useful in tests.
func (h *TypingHeartbeat) Wait() {
	h.wg.Wait()
}

func (h *TypingHeartbeat) run() {
	defer h.wg.Done()

	consecutiveFails := 0
	deadline := time.Now().Add(typingMaxDuration)
	ticker := time.NewTicker(typingKeepaliveInterval)
	defer ticker.Stop()

	fire := func() bool {
		if err := h.session.ChannelTyping(h.channelID); err != nil {
			consecutiveFails++
			slog.Debug("discord: typing indicator failed",
				"channel", h.channelID, "fails", consecutiveFails, "err", err)
			if consecutiveFails >= typingMaxConsecutiveFails {
				slog.Warn("discord: typing indicator: too many consecutive failures, stopping",
					"channel", h.channelID)
				return false
			}
		} else {
			consecutiveFails = 0
		}
		return true
	}

	// Fire immediately.
	if !fire() {
		return
	}

	for {
		select {
		case <-h.stop:
			return
		case t := <-ticker.C:
			if t.After(deadline) {
				slog.Debug("discord: typing indicator: max duration reached", "channel", h.channelID)
				return
			}
			if !fire() {
				return
			}
		}
	}
}
