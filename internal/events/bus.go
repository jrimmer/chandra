// Package events provides an internal publish/subscribe event bus with
// MQTT-style wildcard topic matching and a bounded worker pool.
package events

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/oklog/ulid/v2"
)

// EventBus is the publish/subscribe contract for the internal event bus.
type EventBus interface {
	Publish(topic string, payload []byte) error
	Subscribe(pattern string, handler func(topic string, payload []byte)) (unsubscribe func())
}

// Event carries a topic and raw payload through the internal bus.
type Event struct {
	Topic   string
	Payload []byte
}

// subscription holds a handler registered against a topic pattern.
type subscription struct {
	pattern string
	handler func(topic string, payload []byte)
}

// Bus is a buffered, worker-pool-backed event bus.
// Call Start before publishing or subscribing for event delivery.
type Bus struct {
	queue      chan Event
	subs       sync.Map // map[string]*subscription
	highTopics []string // exact topic names that block-enqueue rather than drop
	workerN    int
	wg         sync.WaitGroup
}

// Compile-time assertion that *Bus satisfies EventBus.
var _ EventBus = (*Bus)(nil)

// NewEventBus constructs a Bus with the given queue capacity and worker count.
// highPriorityTopics lists exact topic names (not patterns) that will block-
// enqueue rather than drop when the queue is full.
func NewEventBus(queueSize, workerCount int, highPriorityTopics []string) *Bus {
	if queueSize < 1 {
		queueSize = 1
	}
	if workerCount < 1 {
		workerCount = 1
	}
	hp := make([]string, len(highPriorityTopics))
	copy(hp, highPriorityTopics)
	return &Bus{
		queue:      make(chan Event, queueSize),
		highTopics: hp,
		workerN:    workerCount,
	}
}

// Start launches the worker goroutines. Workers run until ctx is cancelled and
// remaining queued events are drained. Call Stop() to wait for shutdown.
func (b *Bus) Start(ctx context.Context) {
	for i := 0; i < b.workerN; i++ {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			for {
				select {
				case ev, ok := <-b.queue:
					if !ok {
						return
					}
					b.deliver(ev)
				case <-ctx.Done():
					// Drain remaining events before exiting.
					for {
						select {
						case ev := <-b.queue:
							b.deliver(ev)
						default:
							return
						}
					}
				}
			}
		}()
	}
}

// Stop waits for all workers to finish. It must be called after the context
// passed to Start has been cancelled.
func (b *Bus) Stop() {
	b.wg.Wait()
}

// Publish enqueues an event for delivery to matching subscribers.
// For high-priority topics, Publish blocks until space is available.
// For all other topics, if the queue is full the event is silently dropped
// (a warning is logged) and nil is returned.
func (b *Bus) Publish(topic string, payload []byte) error {
	ev := Event{Topic: topic, Payload: payload}

	if b.isHighPriority(topic) {
		b.queue <- ev
		return nil
	}

	select {
	case b.queue <- ev:
	default:
		slog.Warn("events: queue full, dropping low-priority event", "topic", topic)
	}
	return nil
}

// Subscribe registers handler for events whose topic matches pattern.
// MQTT wildcard rules apply:
//   - `#` at a pattern segment matches that level and everything below it.
//   - `+` matches exactly one topic segment.
//
// The returned function removes the subscription; calling it more than once
// is safe.
func (b *Bus) Subscribe(pattern string, handler func(topic string, payload []byte)) (unsubscribe func()) {
	id := ulid.Make().String()
	b.subs.Store(id, &subscription{pattern: pattern, handler: handler})
	return func() { b.subs.Delete(id) }
}

// isHighPriority reports whether topic is in the high-priority list.
func (b *Bus) isHighPriority(topic string) bool {
	for _, hp := range b.highTopics {
		if hp == topic {
			return true
		}
	}
	return false
}

// deliver calls all matching subscribers within the calling worker goroutine.
func (b *Bus) deliver(ev Event) {
	b.subs.Range(func(_, val any) bool {
		sub := val.(*subscription)
		if matchTopic(sub.pattern, ev.Topic) {
			sub.handler(ev.Topic, ev.Payload)
		}
		return true
	})
}

// matchTopic reports whether topic matches pattern using MQTT wildcard semantics.
//
// Rules:
//   - `#` at a pattern segment matches that level and all levels below.
//     It must appear only at the end of the pattern.
//   - `+` matches exactly one non-empty topic segment.
//   - Literal segments must match case-sensitively.
func matchTopic(pattern, topic string) bool {
	ps := strings.Split(pattern, "/")
	ts := strings.Split(topic, "/")

	for i, p := range ps {
		if p == "#" {
			// Matches everything at and below this level.
			return true
		}
		if i >= len(ts) {
			return false
		}
		if p == "+" {
			// Matches exactly one segment; continue to next.
			continue
		}
		if p != ts[i] {
			return false
		}
	}
	// All pattern segments consumed; topic must also be fully consumed.
	return len(ps) == len(ts)
}
