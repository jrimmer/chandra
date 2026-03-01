package events

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jrimmer/chandra/internal/memory/intent"
)

// EventIntentHandler listens on an EventBus and translates each received event
// into an Intent via the intent store. Per design, events never call
// RunScheduled directly — they go through the Intent store so the scheduler
// picks them up on its normal tick.
type EventIntentHandler struct {
	intentStore intent.IntentStore
	bus         EventBus
	topics      []string

	mu   sync.Mutex
	seen map[string]time.Time // dedup: "topic:payload" -> time last seen

	unsubs []func() // unsubscribe functions returned by Subscribe
}

// NewEventIntentHandler creates a handler that, when started, will subscribe
// to each pattern in topics on bus and create an Intent for each matching event.
func NewEventIntentHandler(intentStore intent.IntentStore, bus EventBus, topics []string) *EventIntentHandler {
	t := make([]string, len(topics))
	copy(t, topics)
	return &EventIntentHandler{
		intentStore: intentStore,
		bus:         bus,
		topics:      t,
		seen:        make(map[string]time.Time),
	}
}

// Start subscribes to all configured topic patterns. It is not safe to call
// Start more than once on the same handler.
func (h *EventIntentHandler) Start() {
	for _, pattern := range h.topics {
		p := pattern // capture
		unsub := h.bus.Subscribe(p, h.handle)
		h.unsubs = append(h.unsubs, unsub)
	}
}

// Stop removes all subscriptions registered by Start.
func (h *EventIntentHandler) Stop() {
	for _, unsub := range h.unsubs {
		unsub()
	}
	h.unsubs = nil
}

// handle is the event callback. It deduplicates, then creates an Intent.
func (h *EventIntentHandler) handle(ctx context.Context, ev Event) error {
	raw := sha256.Sum256([]byte(fmt.Sprintf("%s:%s", ev.Topic, string(ev.Payload))))
	key := fmt.Sprintf("%x", raw)

	h.mu.Lock()
	if last, ok := h.seen[key]; ok && time.Since(last) < 5*time.Minute {
		h.mu.Unlock()
		return nil
	}
	h.seen[key] = time.Now()
	// Prune stale dedup entries to prevent unbounded growth.
	h.pruneSeenLocked()
	h.mu.Unlock()

	in, err := h.intentStore.Create(ctx,
		fmt.Sprintf("event:%s", ev.Topic),
		"event",
		string(ev.Payload),
	)
	if err != nil {
		slog.Warn("events: intent create failed, dropping event", "topic", ev.Topic, "error", err)
		return nil
	}

	// Trigger immediate scheduling by setting NextCheck to now.
	in.NextCheck = time.Now()
	if err := h.intentStore.Update(ctx, in); err != nil {
		slog.Warn("events: intent update failed", "topic", ev.Topic, "error", err)
	}
	return nil
}

// pruneSeenLocked removes dedup entries older than 5 minutes.
// Must be called with h.mu held.
func (h *EventIntentHandler) pruneSeenLocked() {
	cutoff := time.Now().Add(-5 * time.Minute)
	for k, t := range h.seen {
		if t.Before(cutoff) {
			delete(h.seen, k)
		}
	}
}
