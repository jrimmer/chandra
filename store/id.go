package store

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	entropyMu sync.Mutex
	entropy   = ulid.Monotonic(rand.Reader, 0)
)

// NewID returns a new ULID string. Thread-safe, monotonically increasing.
// On monotonic overflow (extremely rare burst scenario), resets entropy source.
func NewID() string {
	entropyMu.Lock()
	defer entropyMu.Unlock()
	id, err := ulid.New(ulid.Timestamp(time.Now()), entropy)
	if err != nil {
		// Overflow: reset monotonic entropy source and retry
		entropy = ulid.Monotonic(rand.Reader, 0)
		id = ulid.MustNew(ulid.Timestamp(time.Now()), entropy)
	}
	return id.String()
}
