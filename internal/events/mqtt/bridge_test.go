package mqtt_test

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/config"
	"github.com/jrimmer/chandra/internal/events"
	mqttbridge "github.com/jrimmer/chandra/internal/events/mqtt"
)

// freePort returns a free TCP port on loopback by briefly listening on :0
// and immediately closing the listener. There is a small TOCTOU window, but
// for test purposes this is acceptable.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// newBus returns a started Bus and registers cleanup.
func newBus(t *testing.T) *events.Bus {
	t.Helper()
	bus := events.NewEventBus(64, 2, nil)
	ctx, cancel := context.WithCancel(context.Background())
	bus.Start(ctx)
	t.Cleanup(func() {
		cancel()
		bus.Stop()
	})
	return bus
}

// TestBridge_Disabled verifies that a disabled bridge starts and stops cleanly.
func TestBridge_Disabled(t *testing.T) {
	bus := newBus(t)
	cfg := config.MQTTConfig{Mode: "disabled"}

	bridge, err := mqttbridge.NewBridge(cfg, bus)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, bridge.Start(ctx))
	require.NoError(t, bridge.Stop())
}

// TestBridge_Embedded_SecurityCheck verifies that starting an embedded broker
// with a non-loopback bind address and no credentials is rejected.
func TestBridge_Embedded_SecurityCheck(t *testing.T) {
	bus := newBus(t)
	cfg := config.MQTTConfig{
		Mode: "embedded",
		// Non-loopback bind with no credentials — must fail security check.
		Bind: "0.0.0.0:1883",
	}

	bridge, err := mqttbridge.NewBridge(cfg, bus)
	require.NoError(t, err)

	ctx := context.Background()
	err = bridge.Start(ctx)
	require.Error(t, err, "expected security error for network bind without auth")
	assert.Contains(t, err.Error(), "auth")
}

// TestBridge_Embedded_LocalhostNoAuth verifies that a loopback-only embedded
// broker with no credentials is permitted (safe default).
func TestBridge_Embedded_LocalhostNoAuth(t *testing.T) {
	bus := newBus(t)
	cfg := config.MQTTConfig{
		Mode:   "embedded",
		Bind:   "127.0.0.1:0", // port 0 = OS-assigned; avoids conflicts
		Topics: []string{"test/#"},
	}

	bridge, err := mqttbridge.NewBridge(cfg, bus)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, bridge.Start(ctx))
	defer bridge.Stop() //nolint:errcheck
}

// TestBridge_Embedded_PublishForwarded verifies that a message published to
// the embedded broker is forwarded to the internal event bus.
func TestBridge_Embedded_PublishForwarded(t *testing.T) {
	t.Skip("requires deterministic embedded broker startup; tested via integration")
}

// TestBridge_External_CompilationSmoke verifies external mode bridge constructs
// without error (actual connection requires a running broker).
func TestBridge_External_CompilationSmoke(t *testing.T) {
	bus := newBus(t)
	cfg := config.MQTTConfig{
		Mode:   "external",
		Broker: "tcp://127.0.0.1:11883", // no broker running; just checks construction
		Topics: []string{"homelab/#"},
	}

	bridge, err := mqttbridge.NewBridge(cfg, bus)
	require.NoError(t, err)
	require.NotNil(t, bridge)
}

// TestBridge_ForwardsToBus exercises the embedded broker end-to-end:
// start bridge → connect paho client → publish → verify event on bus.
func TestBridge_ForwardsToBus(t *testing.T) {
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	brokerURL := fmt.Sprintf("tcp://%s", addr)

	bus := newBus(t)

	var received atomic.Int32
	unsub := bus.Subscribe("sensor/#", func(_ context.Context, _ events.Event) error {
		received.Add(1)
		return nil
	})
	defer unsub()

	cfg := config.MQTTConfig{
		Mode:   "embedded",
		Bind:   addr,
		Topics: []string{"sensor/#"},
	}

	bridge, err := mqttbridge.NewBridge(cfg, bus)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	require.NoError(t, bridge.Start(ctx))
	defer bridge.Stop() //nolint:errcheck

	// Give the embedded broker a moment to be ready.
	time.Sleep(200 * time.Millisecond)

	// Publish via paho to the embedded broker.
	pahoCfg := config.MQTTConfig{
		Mode:   "external",
		Broker: brokerURL,
		Topics: []string{},
	}
	publisherBus := events.NewEventBus(4, 1, nil)
	publisher, err := mqttbridge.NewBridge(pahoCfg, publisherBus)
	require.NoError(t, err)
	require.NoError(t, publisher.Start(ctx))
	defer publisher.Stop() //nolint:errcheck

	time.Sleep(100 * time.Millisecond)

	require.NoError(t, publisher.PublishRaw("sensor/temp", []byte("25.3")))

	assert.Eventually(t, func() bool {
		return received.Load() >= 1
	}, 5*time.Second, 50*time.Millisecond)
}
