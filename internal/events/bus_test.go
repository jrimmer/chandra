package events_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/events"
)

// startBus creates a Bus, starts it with a background context derived from t,
// and registers cleanup to stop it.
func startBus(t *testing.T, queueSize, workers int, highPriority []string) *events.Bus {
	t.Helper()
	bus := events.NewEventBus(queueSize, workers, highPriority)
	ctx, cancel := context.WithCancel(context.Background())
	bus.Start(ctx)
	t.Cleanup(func() {
		cancel()
		bus.Stop()
	})
	return bus
}

func TestEventBus_PublishSubscribe(t *testing.T) {
	bus := startBus(t, 64, 2, nil)

	var received []string
	var mu sync.Mutex

	unsub := bus.Subscribe("test/topic", func(_ context.Context, ev events.Event) error {
		mu.Lock()
		received = append(received, string(ev.Payload))
		mu.Unlock()
		return nil
	})
	defer unsub()

	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "test/topic", Payload: []byte("hello"), Source: "internal"}))
	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "test/topic", Payload: []byte("world"), Source: "internal"}))

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) == 2
	}, 2*time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.ElementsMatch(t, []string{"hello", "world"}, received)
}

func TestEventBus_WildcardHash(t *testing.T) {
	bus := startBus(t, 64, 2, nil)

	var count atomic.Int32
	unsub := bus.Subscribe("homelab/#", func(_ context.Context, _ events.Event) error {
		count.Add(1)
		return nil
	})
	defer unsub()

	// All of these should match homelab/#
	topics := []string{
		"homelab/sensor/temp",
		"homelab/sensor/humidity",
		"homelab/light",
		"homelab/a/b/c/deep",
	}
	for _, topic := range topics {
		require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: topic, Payload: []byte("data"), Source: "internal"}))
	}

	assert.Eventually(t, func() bool {
		return count.Load() == int32(len(topics))
	}, 2*time.Second, 10*time.Millisecond)

	// This should NOT match (different root).
	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "other/sensor/temp", Payload: []byte("data"), Source: "internal"}))

	// Give a moment to ensure the non-matching event doesn't increment.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(len(topics)), count.Load())
}

func TestEventBus_WildcardPlus(t *testing.T) {
	bus := startBus(t, 64, 2, nil)

	var matched atomic.Int32
	unsub := bus.Subscribe("homelab/+/temp", func(_ context.Context, _ events.Event) error {
		matched.Add(1)
		return nil
	})
	defer unsub()

	// Should match: exactly one segment between homelab/ and /temp.
	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "homelab/sensor/temp", Payload: []byte("data"), Source: "internal"}))
	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "homelab/living-room/temp", Payload: []byte("data"), Source: "internal"}))

	assert.Eventually(t, func() bool {
		return matched.Load() == 2
	}, 2*time.Second, 10*time.Millisecond)

	// Should NOT match: two segments in the middle.
	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "homelab/a/b/temp", Payload: []byte("data"), Source: "internal"}))
	// Should NOT match: wrong last segment.
	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "homelab/sensor/humidity", Payload: []byte("data"), Source: "internal"}))

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(2), matched.Load())
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := startBus(t, 64, 2, nil)

	var count atomic.Int32
	unsub := bus.Subscribe("test/topic", func(_ context.Context, _ events.Event) error {
		count.Add(1)
		return nil
	})

	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "test/topic", Payload: []byte("before"), Source: "internal"}))
	assert.Eventually(t, func() bool {
		return count.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)

	// Unsubscribe, then publish again — count must not change.
	unsub()
	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "test/topic", Payload: []byte("after"), Source: "internal"}))

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), count.Load())
}

func TestEventBus_WorkerPool(t *testing.T) {
	// Only 2 workers, publish 10 events. All must eventually be handled.
	bus := startBus(t, 64, 2, nil)

	var count atomic.Int32
	unsub := bus.Subscribe("work/#", func(_ context.Context, _ events.Event) error {
		// Simulate a tiny bit of work.
		time.Sleep(5 * time.Millisecond)
		count.Add(1)
		return nil
	})
	defer unsub()

	for i := 0; i < 10; i++ {
		require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "work/item", Payload: []byte("payload"), Source: "internal"}))
	}

	assert.Eventually(t, func() bool {
		return count.Load() == 10
	}, 5*time.Second, 20*time.Millisecond)
}

func TestEventBus_QueueFull_DropsLowPriority(t *testing.T) {
	// Queue of size 1, one worker that blocks, no high-priority topics.
	// The blocking worker ensures the queue stays full so subsequent
	// low-priority publishes are dropped.
	block := make(chan struct{})
	bus := events.NewEventBus(1, 1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	bus.Start(ctx)
	defer func() {
		cancel()
		close(block)
		bus.Stop()
	}()

	var processed atomic.Int32
	bus.Subscribe("sensor/#", func(_ context.Context, _ events.Event) error {
		<-block // block until test releases
		processed.Add(1)
		return nil
	})

	// First publish fills the queue slot; the worker picks it up and blocks.
	// Give the worker a moment to dequeue the first item before flooding.
	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "sensor/temp", Payload: []byte("1"), Source: "internal"}))
	time.Sleep(20 * time.Millisecond) // let worker dequeue and enter block

	// Now publish many events; queue size=1, worker blocked. These should be
	// dropped (non-blocking path returns nil without error).
	for i := 0; i < 10; i++ {
		_ = bus.Publish(context.Background(), events.Event{Topic: "sensor/temp", Payload: []byte("dropped"), Source: "internal"})
	}

	// Release the blocked worker and stop.
	block <- struct{}{}

	assert.Eventually(t, func() bool {
		return processed.Load() >= 1
	}, 2*time.Second, 10*time.Millisecond)

	// At most a small number of events should have been processed (not all 11).
	assert.Less(t, processed.Load(), int32(11))
}
