// Package ratelimit provides a simple per-key token bucket rate limiter.
//
// Each unique key (e.g. a Discord user ID) gets an independent bucket that
// refills to its capacity at the start of each window. A typical use is
// per-user message throttling: Allow returns false when a user exceeds the
// configured message rate, enabling the caller to drop or delay the message.
//
// The limiter is goroutine-safe and allocates one bucket per seen key.
// Buckets whose reset time has passed are lazily replaced on the next Allow
// call, so there is no background sweep goroutine.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a per-key token bucket rate limiter.
type Limiter struct {
	mu     sync.Mutex
	rate   int           // max tokens (messages) per window
	window time.Duration // window duration (e.g. time.Minute)

	buckets map[string]*bucket
}

type bucket struct {
	tokens  int
	resetAt time.Time
}

// New returns a Limiter that allows up to rate events per window per key.
// rate=0 or window=0 disables limiting (Allow always returns true).
func New(rate int, window time.Duration) *Limiter {
	return &Limiter{
		rate:    rate,
		window:  window,
		buckets: make(map[string]*bucket),
	}
}

// Allow returns true if the key is within its rate limit and consumes one token.
// Returns true unconditionally if the limiter was created with rate=0 or window=0.
func (l *Limiter) Allow(key string) bool {
	if l.rate <= 0 || l.window <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok || now.After(b.resetAt) {
		// New key or window expired: fresh bucket with one token already consumed.
		l.buckets[key] = &bucket{tokens: l.rate - 1, resetAt: now.Add(l.window)}
		return true
	}
	if b.tokens > 0 {
		b.tokens--
		return true
	}
	return false // bucket empty
}

// Remaining returns the number of tokens remaining for key in the current window.
// Returns rate if key has never been seen or its window has expired.
func (l *Limiter) Remaining(key string) int {
	if l.rate <= 0 || l.window <= 0 {
		return -1 // unlimited
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok || time.Now().After(b.resetAt) {
		return l.rate
	}
	return b.tokens
}
