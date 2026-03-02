package notifications

import (
	"context"
	"testing"
	"time"
)

// mockStore implements the Store interface for testing.
type mockStore struct {
	notifications []Notification
	delivered     map[string]bool
}

func newMockStore() *mockStore {
	return &mockStore{
		delivered: make(map[string]bool),
	}
}

func (m *mockStore) CreateNotification(ctx context.Context, n *Notification) error {
	m.notifications = append(m.notifications, *n)
	return nil
}

func (m *mockStore) ListPending(ctx context.Context, userID string) ([]Notification, error) {
	var result []Notification
	for _, n := range m.notifications {
		if n.UserID == userID && !m.delivered[n.ID] {
			result = append(result, n)
		}
	}
	return result, nil
}

func (m *mockStore) MarkDelivered(ctx context.Context, id string) error {
	m.delivered[id] = true
	return nil
}

func (m *mockStore) Cleanup(ctx context.Context, retention time.Duration) (int, error) {
	return 0, nil
}

// mockChannel implements the Sender interface for testing.
type mockChannel struct {
	sent []string
}

func (m *mockChannel) SendNotification(ctx context.Context, userID, message string) error {
	m.sent = append(m.sent, message)
	return nil
}

func TestNotifyUser_OnlineDelivers(t *testing.T) {
	store := newMockStore()
	ch := &mockChannel{}
	svc := NewService(store, ch)

	// Mark user as online
	svc.SetOnline("user1", true)

	err := svc.NotifyUser(context.Background(), "user1", "plan completed", "plan", "plan-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ch.sent) != 1 {
		t.Errorf("expected 1 sent message, got %d", len(ch.sent))
	}
}

func TestNotifyUser_OfflineQueues(t *testing.T) {
	store := newMockStore()
	ch := &mockChannel{}
	svc := NewService(store, ch)

	// User is offline by default
	err := svc.NotifyUser(context.Background(), "user1", "plan completed", "plan", "plan-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be queued, not sent
	if len(ch.sent) != 0 {
		t.Errorf("expected 0 sent messages (offline), got %d", len(ch.sent))
	}
	if len(store.notifications) != 1 {
		t.Errorf("expected 1 queued notification, got %d", len(store.notifications))
	}
}

func TestNotifyUser_SanitizesSecrets(t *testing.T) {
	store := newMockStore()
	ch := &mockChannel{}
	svc := NewService(store, ch)
	svc.SetOnline("user1", true)

	// Message contains a secret pattern
	err := svc.NotifyUser(context.Background(), "user1", "deployed with key sk-abc123456789extra", "plan", "plan-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ch.sent) != 1 {
		t.Fatalf("expected 1 sent, got %d", len(ch.sent))
	}
	// The secret should be redacted
	if ch.sent[0] == "deployed with key sk-abc123456789extra" {
		t.Error("expected secret to be sanitized")
	}
}

func TestFlushPending_DeliversQueued(t *testing.T) {
	store := newMockStore()
	ch := &mockChannel{}
	svc := NewService(store, ch)

	// Queue notification while offline
	svc.NotifyUser(context.Background(), "user1", "msg1", "plan", "plan-1")
	svc.NotifyUser(context.Background(), "user1", "msg2", "plan", "plan-2")

	// Come online and flush
	svc.SetOnline("user1", true)
	err := svc.FlushPending(context.Background(), "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ch.sent) != 2 {
		t.Errorf("expected 2 sent messages after flush, got %d", len(ch.sent))
	}
}

func TestService_SessionDetection(t *testing.T) {
	store := newMockStore()
	ch := &mockChannel{}
	svc := NewService(store, ch)

	if svc.IsOnline("user1") {
		t.Error("expected user to be offline by default")
	}

	svc.SetOnline("user1", true)
	if !svc.IsOnline("user1") {
		t.Error("expected user to be online after SetOnline(true)")
	}

	svc.SetOnline("user1", false)
	if svc.IsOnline("user1") {
		t.Error("expected user to be offline after SetOnline(false)")
	}
}
