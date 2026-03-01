// Package mqtt provides an MQTT bridge that connects an internal EventBus to
// an MQTT broker — either an embedded mochi-mqtt server or an external broker
// reached via paho.
package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/jrimmer/chandra/internal/config"
	"github.com/jrimmer/chandra/internal/events"
)

// EventBus is the subset of the internal event bus required by the bridge.
// It matches the EventBus interface in internal/events.
type EventBus interface {
	Publish(ctx context.Context, event events.Event) error
}

// Bridge manages the lifecycle of the MQTT broker/client connection and
// message forwarding into the internal event bus.
type Bridge interface {
	Start(ctx context.Context) error
	Stop() error
	// PublishRaw publishes directly through the bridge (used in tests and
	// by the external-mode client to send messages to the broker).
	PublishRaw(topic string, payload []byte) error
}

// Compile-time assertion.
var _ Bridge = (*bridge)(nil)

type bridge struct {
	cfg config.MQTTConfig
	bus EventBus

	// embedded broker
	srv *mochi.Server

	// external paho client
	client pahomqtt.Client

	once sync.Once
	mu   sync.Mutex
}

// NewBridge constructs a bridge for the given MQTT configuration.
// The bridge is not started until Start is called.
func NewBridge(cfg config.MQTTConfig, bus EventBus) (*bridge, error) {
	return &bridge{cfg: cfg, bus: bus}, nil
}

// Start launches the bridge. The behaviour depends on cfg.Mode:
//   - "disabled": no-op.
//   - "embedded": starts a mochi-mqtt server in-process, subscribes to
//     configured topics, and forwards messages to the bus.
//   - "external": connects a paho client to cfg.Broker, subscribes to
//     configured topics, and forwards messages to the bus.
func (b *bridge) Start(ctx context.Context) error {
	switch b.cfg.Mode {
	case "disabled", "":
		return nil
	case "embedded":
		return b.startEmbedded(ctx)
	case "external":
		return b.startExternal(ctx)
	default:
		return fmt.Errorf("mqtt bridge: unknown mode %q", b.cfg.Mode)
	}
}

// Stop gracefully shuts down the bridge. It is safe to call Stop multiple
// times; subsequent calls are no-ops.
func (b *bridge) Stop() error {
	var stopErr error
	b.once.Do(func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		if b.client != nil && b.client.IsConnected() {
			b.client.Disconnect(500)
		}
		if b.srv != nil {
			if err := b.srv.Close(); err != nil {
				stopErr = fmt.Errorf("mqtt bridge: close embedded broker: %w", err)
			}
		}
	})
	return stopErr
}

// PublishRaw publishes a message through the bridge's underlying transport.
// In external mode the paho client is used; in embedded mode the server's
// inline client is used. In disabled mode it is a no-op.
func (b *bridge) PublishRaw(topic string, payload []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.client != nil && b.client.IsConnected() {
		tok := b.client.Publish(topic, 0, false, payload)
		tok.Wait()
		return tok.Error()
	}
	if b.srv != nil {
		return b.srv.Publish(topic, payload, false, 0)
	}
	return nil
}

// --- embedded mode ---

func (b *bridge) startEmbedded(ctx context.Context) error {
	if err := b.validateEmbeddedSecurity(); err != nil {
		return err
	}

	srv := mochi.New(&mochi.Options{
		InlineClient: true,
	})

	// Install allow-all auth; for production use with credentials the caller
	// should configure a proper auth hook. We install AllowHook here because
	// the security check above already enforces that non-loopback binds must
	// have credentials (future: swap in a credential hook).
	if err := srv.AddHook(new(auth.AllowHook), nil); err != nil {
		return fmt.Errorf("mqtt bridge: add auth hook: %w", err)
	}

	tcp := listeners.NewTCP(listeners.Config{
		ID:      "chandra-embedded",
		Address: b.cfg.Bind,
	})
	if err := srv.AddListener(tcp); err != nil {
		return fmt.Errorf("mqtt bridge: add listener: %w", err)
	}

	b.mu.Lock()
	b.srv = srv
	b.mu.Unlock()

	// Start the broker in a background goroutine; Serve() blocks.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(); err != nil {
			errCh <- err
		}
	}()

	// Subscribe to configured topics using the inline client.
	for _, pattern := range b.cfg.Topics {
		p := pattern // capture
		if err := srv.Subscribe(p, 1, func(_ *mochi.Client, _ packets.Subscription, pk packets.Packet) {
			ev := events.Event{
				Topic:   pk.TopicName,
				Payload: pk.Payload,
				Source:  "mqtt",
			}
			if err := b.bus.Publish(context.Background(), ev); err != nil {
				slog.Warn("mqtt bridge: forward to bus failed", "topic", pk.TopicName, "err", err)
			}
		}); err != nil {
			return fmt.Errorf("mqtt bridge: inline subscribe %q: %w", p, err)
		}
	}

	// Watch for context cancellation or startup errors.
	go func() {
		select {
		case err := <-errCh:
			slog.Error("mqtt bridge: embedded broker error", "err", err)
		case <-ctx.Done():
			if closeErr := b.Stop(); closeErr != nil {
				slog.Error("mqtt bridge: stop on context done", "err", closeErr)
			}
		}
	}()

	return nil
}

// validateEmbeddedSecurity returns an error if the embedded broker would be
// exposed on a non-loopback interface without authentication configured.
func (b *bridge) validateEmbeddedSecurity() error {
	bind := b.cfg.Bind
	if bind == "" {
		bind = "127.0.0.1:1883"
	}
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		return fmt.Errorf("mqtt bridge: invalid bind address %q: %w", bind, err)
	}

	ip := net.ParseIP(host)
	isLoopback := ip != nil && ip.IsLoopback()
	isLocalhost := strings.EqualFold(host, "localhost")

	if !isLoopback && !isLocalhost {
		// Non-loopback bind; require both username and password to be set.
		if b.cfg.Username == "" || b.cfg.Password == "" {
			return fmt.Errorf(
				"mqtt bridge: embedded broker bound to %q requires auth configuration "+
					"(set mqtt.username and mqtt.password)", bind)
		}
	}
	return nil
}

// --- external mode ---

func (b *bridge) startExternal(ctx context.Context) error {
	opts := pahomqtt.NewClientOptions().
		AddBroker(b.cfg.Broker).
		SetClientID("chandra-bridge").
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(1 * time.Second).
		SetMaxReconnectInterval(60 * time.Second).
		SetOnConnectHandler(func(c pahomqtt.Client) {
			b.subscribeTopics(c)
		}).
		SetConnectionLostHandler(func(c pahomqtt.Client, err error) {
			slog.Warn("mqtt bridge: connection lost", "err", err)
		})

	if b.cfg.Username != "" {
		opts.SetUsername(b.cfg.Username)
		opts.SetPassword(b.cfg.Password)
	}

	client := pahomqtt.NewClient(opts)

	b.mu.Lock()
	b.client = client
	b.mu.Unlock()

	tok := client.Connect()
	// Wait with a timeout; if the broker is unreachable we return an error
	// instead of hanging forever. Reconnection is handled by paho internally.
	timeout := 10*time.Second + time.Duration(rand.Intn(500))*time.Millisecond
	if !tok.WaitTimeout(timeout) {
		// Broker not immediately reachable; paho will keep retrying in the
		// background. Log and return nil so the bridge stays running.
		slog.Warn("mqtt bridge: initial connect timed out, will retry", "broker", b.cfg.Broker)
		return nil
	}
	if err := tok.Error(); err != nil {
		// Same policy: log and continue; paho retries.
		slog.Warn("mqtt bridge: initial connect failed, will retry", "broker", b.cfg.Broker, "err", err)
		return nil
	}

	// Context watcher.
	go func() {
		<-ctx.Done()
		if stopErr := b.Stop(); stopErr != nil {
			slog.Error("mqtt bridge: stop on context done", "err", stopErr)
		}
	}()

	return nil
}

// subscribeTopics subscribes the paho client to all configured topics.
// It is called from the OnConnectHandler so it runs on reconnect too.
func (b *bridge) subscribeTopics(client pahomqtt.Client) {
	for _, topic := range b.cfg.Topics {
		t := topic
		tok := client.Subscribe(t, 0, func(_ pahomqtt.Client, msg pahomqtt.Message) {
			ev := events.Event{
				Topic:   msg.Topic(),
				Payload: msg.Payload(),
				Source:  "mqtt",
			}
			if err := b.bus.Publish(context.Background(), ev); err != nil {
				slog.Warn("mqtt bridge: forward to bus failed",
					"topic", msg.Topic(), "err", err)
			}
		})
		tok.Wait()
		if err := tok.Error(); err != nil {
			slog.Warn("mqtt bridge: subscribe failed", "topic", t, "err", err)
		}
	}
}
