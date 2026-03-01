package store

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewID_ReturnsValidULID(t *testing.T) {
	id := NewID()
	assert.Len(t, id, 26, "ULID should be 26 characters")

	// Two IDs should be different
	id2 := NewID()
	assert.NotEqual(t, id, id2)
}

func TestNewID_Sortable(t *testing.T) {
	id1 := NewID()
	id2 := NewID()
	// id2 created after id1, should sort after
	assert.True(t, id2 >= id1, "later ULID should sort after earlier")
}

func TestNewID_ConcurrentSafe(t *testing.T) {
	const n = 1000
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i] = NewID()
		}(i)
	}
	wg.Wait()

	// All IDs should be unique
	seen := make(map[string]bool, n)
	for _, id := range ids {
		assert.False(t, seen[id], "duplicate ULID: %s", id)
		seen[id] = true
	}
}
