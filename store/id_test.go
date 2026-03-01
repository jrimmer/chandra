package store

import (
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
