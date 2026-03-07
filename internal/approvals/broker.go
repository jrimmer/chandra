// Package approvals provides an in-memory broker for exec approval requests.
// A caller (exec tool) registers a channel keyed by Discord message ID;
// the Discord reaction handler resolves it when an operator reacts.
package approvals

import (
	"sync"
)

// Broker manages pending exec approval channels.
// It is safe for concurrent use.
type Broker struct {
	mu      sync.Mutex
	pending map[string]chan bool
}

// New returns an initialised Broker.
func New() *Broker {
	return &Broker{pending: make(map[string]chan bool)}
}

// Register creates a buffered result channel for the given message ID and
// returns a receive-only handle. The caller must select on this channel with a
// timeout and call Cancel when done to prevent resource leaks.
// If a registration already exists for msgID it is silently replaced.
func (b *Broker) Register(msgID string) <-chan bool {
	ch := make(chan bool, 1)
	b.mu.Lock()
	b.pending[msgID] = ch
	b.mu.Unlock()
	return ch
}

// Resolve signals the waiter for msgID with the approval decision.
// Returns true if a waiter was found, false if it had already been
// cancelled or timed out.
func (b *Broker) Resolve(msgID string, approved bool) bool {
	b.mu.Lock()
	ch, ok := b.pending[msgID]
	if ok {
		delete(b.pending, msgID)
	}
	b.mu.Unlock()
	if ok {
		ch <- approved
	}
	return ok
}

// Cancel removes the waiter for msgID without signalling it.
// The exec tool calls this via defer so that stale entries do not accumulate
// when a wait times out before a reaction arrives.
func (b *Broker) Cancel(msgID string) {
	b.mu.Lock()
	delete(b.pending, msgID)
	b.mu.Unlock()
}
