// Package memory provides a unified facade over all four memory layers:
// episodic, semantic, intent, and identity.
package memory

import (
	"github.com/jrimmer/chandra/internal/memory/episodic"
	"github.com/jrimmer/chandra/internal/memory/identity"
	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/internal/memory/semantic"
)

// Memory wraps all four memory stores behind a single interface.
type Memory interface {
	Episodic() episodic.EpisodicStore
	Semantic() semantic.SemanticStore
	Intent()   intent.IntentStore
	Identity() identity.IdentityStore
}

// Compile-time assertion that *memory satisfies Memory.
var _ Memory = (*memory)(nil)

// memory is the concrete implementation of the Memory facade.
type memory struct {
	ep  episodic.EpisodicStore
	sem semantic.SemanticStore
	in  intent.IntentStore
	id  identity.IdentityStore
}

// New constructs a Memory facade wrapping the four provided stores.
func New(
	ep episodic.EpisodicStore,
	sem semantic.SemanticStore,
	in intent.IntentStore,
	id identity.IdentityStore,
) Memory {
	return &memory{
		ep:  ep,
		sem: sem,
		in:  in,
		id:  id,
	}
}

// Episodic returns the episodic memory store.
func (m *memory) Episodic() episodic.EpisodicStore { return m.ep }

// Semantic returns the semantic memory store.
func (m *memory) Semantic() semantic.SemanticStore { return m.sem }

// Intent returns the intent store.
func (m *memory) Intent() intent.IntentStore { return m.in }

// Identity returns the identity store.
func (m *memory) Identity() identity.IdentityStore { return m.id }
