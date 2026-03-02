package notifications

import (
	"context"
	"sync"
	"time"

	"github.com/jrimmer/chandra/internal/skills"
)

// Notification represents a queued notification.
type Notification struct {
	ID         string
	UserID     string
	Message    string
	SourceType string
	SourceID   string
	CreatedAt  int64
	ExpiresAt  int64
}

// Store is the persistence interface for notifications.
type Store interface {
	CreateNotification(ctx context.Context, n *Notification) error
	ListPending(ctx context.Context, userID string) ([]Notification, error)
	MarkDelivered(ctx context.Context, id string) error
	Cleanup(ctx context.Context, retention time.Duration) (int, error)
}

// Sender delivers notifications to a user via a channel.
type Sender interface {
	SendNotification(ctx context.Context, userID, message string) error
}

// Service manages notification delivery with session detection.
type Service struct {
	store  Store
	sender Sender

	mu     sync.RWMutex
	online map[string]bool
}

// NewService creates a new notification service.
func NewService(store Store, sender Sender) *Service {
	return &Service{
		store:  store,
		sender: sender,
		online: make(map[string]bool),
	}
}

// SetOnline marks a user as online or offline.
func (s *Service) SetOnline(userID string, online bool) {
	s.mu.Lock()
	if online {
		s.online[userID] = true
	} else {
		delete(s.online, userID)
	}
	s.mu.Unlock()
}

// IsOnline returns whether a user is currently online.
func (s *Service) IsOnline(userID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.online[userID]
}

// NotifyUser sends a notification to a user. If the user is online, deliver
// immediately via the channel sender. If offline, queue in the store.
// Messages are sanitized to remove secrets before delivery or storage.
func (s *Service) NotifyUser(ctx context.Context, userID, message, sourceType, sourceID string) error {
	// Sanitize the message to remove any secrets
	sanitized := skills.SanitizeForLog(message, nil)

	if s.IsOnline(userID) {
		return s.sender.SendNotification(ctx, userID, sanitized)
	}

	// Queue for later delivery
	notif := &Notification{
		ID:         generateID(),
		UserID:     userID,
		Message:    sanitized,
		SourceType: sourceType,
		SourceID:   sourceID,
		CreatedAt:  time.Now().Unix(),
		ExpiresAt:  time.Now().Add(7 * 24 * time.Hour).Unix(),
	}
	return s.store.CreateNotification(ctx, notif)
}

// FlushPending delivers all pending notifications for a user who is now online.
func (s *Service) FlushPending(ctx context.Context, userID string) error {
	pending, err := s.store.ListPending(ctx, userID)
	if err != nil {
		return err
	}

	for _, n := range pending {
		if err := s.sender.SendNotification(ctx, userID, n.Message); err != nil {
			return err
		}
		if err := s.store.MarkDelivered(ctx, n.ID); err != nil {
			return err
		}
	}
	return nil
}

// idCounter is a simple counter for test-friendly ID generation.
var (
	idMu      sync.Mutex
	idCounter int
)

func generateID() string {
	idMu.Lock()
	idCounter++
	id := idCounter
	idMu.Unlock()
	return "notif-" + itoa(id)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
