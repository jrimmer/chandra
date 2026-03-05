package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/memory"
	"github.com/jrimmer/chandra/internal/memory/episodic"
	"github.com/jrimmer/chandra/internal/memory/identity"
	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/internal/memory/semantic"
	"github.com/jrimmer/chandra/pkg"
)

// --- mock implementations ---

type mockEpisodic struct{}

func (m *mockEpisodic) Append(_ context.Context, _ pkg.Episode) error               { return nil }
func (m *mockEpisodic) Recent(_ context.Context, _ string, _ int) ([]pkg.Episode, error) {
	return nil, nil
}
func (m *mockEpisodic) RecentAcrossSessions(_ context.Context, _, _ string, _ int) ([]pkg.Episode, error) {
	return nil, nil
}
func (m *mockEpisodic) Since(_ context.Context, _ time.Time) ([]pkg.Episode, error) {
	return nil, nil
}

var _ episodic.EpisodicStore = (*mockEpisodic)(nil)

type mockSemantic struct{}

func (m *mockSemantic) Store(_ context.Context, _ pkg.MemoryEntry) error              { return nil }
func (m *mockSemantic) StoreBatch(_ context.Context, _ []pkg.MemoryEntry) error       { return nil }
func (m *mockSemantic) Query(_ context.Context, _ []float32, _ int, _ string) ([]pkg.MemoryEntry, error) {
	return nil, nil
}
func (m *mockSemantic) QueryText(_ context.Context, _ string, _ int, _ string) ([]pkg.MemoryEntry, error) {
	return nil, nil
}

var _ semantic.SemanticStore = (*mockSemantic)(nil)

type mockIntent struct{}

func (m *mockIntent) Create(_ context.Context, _ intent.Intent) error       { return nil }
func (m *mockIntent) Update(_ context.Context, _ intent.Intent) error       { return nil }
func (m *mockIntent) Active(_ context.Context) ([]intent.Intent, error)     { return nil, nil }
func (m *mockIntent) Due(_ context.Context) ([]intent.Intent, error)        { return nil, nil }
func (m *mockIntent) Complete(_ context.Context, _ string) error            { return nil }
func (m *mockIntent) Reschedule(_ context.Context, _ string, _ time.Time) error { return nil }

var _ intent.IntentStore = (*mockIntent)(nil)

type mockIdentity struct{}

func (m *mockIdentity) Agent() (identity.AgentProfile, error)     { return identity.AgentProfile{}, nil }
func (m *mockIdentity) SetAgent(_ context.Context, _ identity.AgentProfile) error { return nil }
func (m *mockIdentity) User() (identity.UserProfile, error)       { return identity.UserProfile{}, nil }
func (m *mockIdentity) SetUser(_ context.Context, _ identity.UserProfile) error { return nil }
func (m *mockIdentity) Relationship() (identity.RelationshipState, error) {
	return identity.RelationshipState{}, nil
}
func (m *mockIdentity) UpdateRelationship(_ context.Context, _ identity.RelationshipState) error {
	return nil
}

var _ identity.IdentityStore = (*mockIdentity)(nil)

// --- tests ---

func TestMemoryFacade_Accessors(t *testing.T) {
	ep := &mockEpisodic{}
	sem := &mockSemantic{}
	in := &mockIntent{}
	id := &mockIdentity{}

	mem := memory.New(ep, sem, in, id)

	if mem.Episodic() != ep {
		t.Error("Episodic() returned wrong store")
	}

	if mem.Semantic() != sem {
		t.Error("Semantic() returned wrong store")
	}

	if mem.Intent() != in {
		t.Error("Intent() returned wrong store")
	}

	if mem.Identity() != id {
		t.Error("Identity() returned wrong store")
	}
}
