// Package channels provides the ChannelSupervisor, a lifecycle wrapper that
// adds exponential-backoff reconnection and health observability to any Channel.
package channels

import (
	"context"
	"log/slog"
	"math"
	"time"
)

// SupervisorConfig holds tuning parameters for the ChannelSupervisor.
type SupervisorConfig struct {
	// InitialBackoff is the wait before the first reconnect attempt (default 1s).
	InitialBackoff time.Duration
	// MaxBackoff caps the backoff interval (default 30s).
	MaxBackoff time.Duration
	// MaxAttempts is the number of consecutive reconnect failures before entering
	// StateFailed and giving up. 0 means retry forever.
	MaxAttempts int
}

func (c *SupervisorConfig) defaults() {
	if c.InitialBackoff <= 0 {
		c.InitialBackoff = time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 30 * time.Second
	}
}

// ChannelSupervisor wraps a Channel and automatically reconnects on failure
// using exponential backoff. It implements Channel so it can replace the inner
// channel anywhere in the daemon without callers noticing.
type ChannelSupervisor struct {
	inner  Channel
	cfg    SupervisorConfig
}

// NewSupervisor wraps inner with supervised reconnect logic.
func NewSupervisor(inner Channel, cfg SupervisorConfig) *ChannelSupervisor {
	cfg.defaults()
	return &ChannelSupervisor{inner: inner, cfg: cfg}
}

// ID delegates to the inner channel.
func (s *ChannelSupervisor) ID() string { return s.inner.ID() }

// Send delegates to the inner channel.
// If the channel is not connected, the message is dropped with a warning.
func (s *ChannelSupervisor) Send(ctx context.Context, msg OutboundMessage) error {
	state := s.inner.ConnectionState()
	if state != StateConnected && state != StateUnknown {
		slog.Warn("channel supervisor: dropping outbound message — channel not connected",
			"channel", s.inner.ID(), "state", state.String())
		return nil
	}
	return s.inner.Send(ctx, msg)
}

// React delegates to the inner channel.
func (s *ChannelSupervisor) React(ctx context.Context, messageID, emoji string) error {
	return s.inner.React(ctx, messageID, emoji)
}

// SendCheckpoint delegates to the inner channel.
func (s *ChannelSupervisor) SendCheckpoint(ctx context.Context, planID, stepDescription string) error {
	return s.inner.SendCheckpoint(ctx, planID, stepDescription)
}

// Reconnect delegates to the inner channel.
func (s *ChannelSupervisor) Reconnect(ctx context.Context) error {
	return s.inner.Reconnect(ctx)
}

// ConnectionState returns the inner channel's connection state.
func (s *ChannelSupervisor) ConnectionState() ConnectionState {
	return s.inner.ConnectionState()
}

// Listen starts the inner channel's Listen loop and restarts it with
// exponential backoff if it exits with a non-context error.
//
// The supervisor exits when ctx is cancelled.
func (s *ChannelSupervisor) Listen(ctx context.Context, msgs chan<- InboundMessage) error {
	attempt := 0
	for {
		err := s.inner.Listen(ctx, msgs)

		// Context cancelled — clean exit.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil {
			attempt++
			if s.cfg.MaxAttempts > 0 && attempt > s.cfg.MaxAttempts {
				slog.Error("channel supervisor: max reconnect attempts reached — giving up",
					"channel", s.inner.ID(), "attempts", attempt)
				return err
			}

			backoff := backoffDuration(attempt, s.cfg.InitialBackoff, s.cfg.MaxBackoff)
			slog.Warn("channel supervisor: connection lost — reconnecting",
				"channel", s.inner.ID(),
				"attempt", attempt,
				"backoff", backoff.String(),
				"err", err)

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}

			if reconnErr := s.inner.Reconnect(ctx); reconnErr != nil {
				slog.Warn("channel supervisor: reconnect failed",
					"channel", s.inner.ID(),
					"attempt", attempt,
					"err", reconnErr)
				continue // backoff loop will increase next wait
			}

			slog.Info("channel supervisor: reconnected, restarting listener",
				"channel", s.inner.ID(), "attempt", attempt)
			attempt = 0 // reset on successful reconnect
			continue
		}

		// Listen returned nil without ctx cancellation — unusual but not fatal.
		// Treat as a clean exit.
		return nil
	}
}

// backoffDuration returns exponential backoff capped at maxBackoff.
// attempt is 1-indexed.
func backoffDuration(attempt int, initial, max time.Duration) time.Duration {
	exp := math.Pow(2, float64(attempt-1))
	d := time.Duration(float64(initial) * exp)
	if d > max {
		return max
	}
	return d
}
