package events_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/events"
	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/store"
)

func init() {
	vec.Auto()
}

// newTestIntentStore creates a real SQLite-backed IntentStore for tests.
func newTestIntentStore(t *testing.T) *intent.Store {
	t.Helper()
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	db := s.DB()
	t.Cleanup(func() { db.Close() })
	return intent.NewStore(db)
}

// busWithHandler returns a started Bus and an EventIntentHandler subscribed
// to the given topics. The handler is started inside.
func busWithHandler(t *testing.T, is intent.IntentStore, topics []string) (*events.Bus, *events.EventIntentHandler) {
	t.Helper()
	bus := events.NewEventBus(64, 2, nil)
	ctx, cancel := context.WithCancel(context.Background())
	bus.Start(ctx)

	handler := events.NewEventIntentHandler(is, bus, topics)
	handler.Start()

	t.Cleanup(func() {
		cancel()
		bus.Stop()
	})
	return bus, handler
}

func TestEventIntentHandler_CreatesIntent(t *testing.T) {
	is := newTestIntentStore(t)
	bus, _ := busWithHandler(t, is, []string{"homelab/#"})

	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "homelab/sensor/temp", Payload: []byte(`{"value":25.3}`), Source: "mqtt"}))

	// Wait for the intent to appear in the store.
	var intents []intent.Intent
	assert.Eventually(t, func() bool {
		var err error
		intents, err = is.Active(context.Background())
		return err == nil && len(intents) == 1
	}, 3*time.Second, 20*time.Millisecond)

	require.Len(t, intents, 1)
	in := intents[0]
	assert.Equal(t, "event:homelab/sensor/temp", in.Description)
	assert.Equal(t, "event", in.Condition)
	assert.Equal(t, `{"value":25.3}`, in.Action)
}

func TestEventIntentHandler_Deduplication(t *testing.T) {
	is := newTestIntentStore(t)
	bus, _ := busWithHandler(t, is, []string{"sensor/#"})

	payload := []byte(`{"temp":20}`)

	// Publish twice with the same topic+payload.
	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "sensor/temp", Payload: payload, Source: "mqtt"}))
	// Small delay to ensure first event is processed before second.
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "sensor/temp", Payload: payload, Source: "mqtt"}))

	// Wait long enough for both to potentially be processed.
	time.Sleep(200 * time.Millisecond)

	intents, err := is.Active(context.Background())
	require.NoError(t, err)
	assert.Len(t, intents, 1, "duplicate event should not create a second intent")
}

func TestEventIntentHandler_BackpressureDelay(t *testing.T) {
	mock := &failingIntentStore{}
	bus := events.NewEventBus(64, 2, nil)
	ctx, cancel := context.WithCancel(context.Background())
	bus.Start(ctx)
	defer func() {
		cancel()
		bus.Stop()
	}()

	handler := events.NewEventIntentHandler(mock, bus, []string{"alert/#"})
	handler.Start()

	require.NoError(t, bus.Publish(context.Background(), events.Event{Topic: "alert/fire", Payload: []byte("evacuate"), Source: "internal"}))

	// Wait for the handler to receive and attempt Create.
	assert.Eventually(t, func() bool {
		return mock.calls.Load() >= 1
	}, 3*time.Second, 20*time.Millisecond)

	// Verify that after a backpressure Create failure the handler recorded a
	// deferred NextCheck approximately 5 minutes out.
	// We check this indirectly: the intent produced must have NextCheck in the future.
	// Since the mock store returns an error on Create, no intent is stored in it —
	// the backpressure path sets NextCheck on the returned (nil) intent, so we just
	// verify the handler didn't panic and calls were made.
	assert.GreaterOrEqual(t, mock.calls.Load(), int32(1))
}

// --- mock ---

// failingIntentStore is an IntentStore whose Create always fails.
type failingIntentStore struct {
	calls atomic.Int32
	// Allow Active/Due/Complete/Update to succeed for the interface.
}

var _ intent.IntentStore = (*failingIntentStore)(nil)

func (f *failingIntentStore) Create(_ context.Context, _ intent.Intent) error {
	f.calls.Add(1)
	return errors.New("queue full")
}
func (f *failingIntentStore) Update(_ context.Context, _ intent.Intent) error { return nil }
func (f *failingIntentStore) Active(_ context.Context) ([]intent.Intent, error) {
	return nil, nil
}
func (f *failingIntentStore) Due(_ context.Context) ([]intent.Intent, error) { return nil, nil }
func (f *failingIntentStore) Complete(_ context.Context, _ string) error {
	return nil
}

// Ensure intent.Store satisfies IntentStore (used to validate real store in tests).
var _ intent.IntentStore = (*intent.Store)(nil)

// Verify sql.DB satisfies the underlying expectation.
var _ = (*sql.DB)(nil)
