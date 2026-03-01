package semantic_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/memory/semantic"
	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
)

// mockEmbedder is a test double for provider.EmbeddingProvider.
type mockEmbedder struct {
	dims      int
	callCount int
	vectors   map[string][]float32 // content -> embedding
}

func (m *mockEmbedder) Embed(_ context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	m.callCount++
	embeddings := make([][]float32, len(req.Texts))
	for i, text := range req.Texts {
		if v, ok := m.vectors[text]; ok {
			embeddings[i] = v
		} else {
			embeddings[i] = make([]float32, m.dims) // zero vector
		}
	}
	return provider.EmbeddingResponse{Embeddings: embeddings, Dimensions: m.dims}, nil
}

func (m *mockEmbedder) Dimensions() int { return m.dims }

// newTestDB creates a temporary database with migrations applied.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	s, err := store.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	require.NoError(t, s.Migrate())
	db := s.DB()
	t.Cleanup(func() { db.Close() })
	return db
}

// makeVec returns a []float32 of length dims with val at position pos and
// zeros elsewhere.
func makeVec(dims, pos int, val float32) []float32 {
	v := make([]float32, dims)
	if pos >= 0 && pos < dims {
		v[pos] = val
	}
	return v
}

func TestSemanticStore_StoreAndQuery(t *testing.T) {
	const dims = 1536
	db := newTestDB(t)
	emb := &mockEmbedder{
		dims: dims,
		vectors: map[string][]float32{
			"alpha":   makeVec(dims, 0, 1.0), // unit vector along dim 0
			"beta":    makeVec(dims, 1, 1.0), // unit vector along dim 1
			"gamma":   makeVec(dims, 2, 1.0), // unit vector along dim 2
		},
	}

	s, err := semantic.NewStore(db, emb)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now().UTC()

	entries := []pkg.MemoryEntry{
		{Content: "alpha", Source: "conversation", Timestamp: now},
		{Content: "beta", Source: "conversation", Timestamp: now},
		{Content: "gamma", Source: "conversation", Timestamp: now},
	}
	for _, e := range entries {
		require.NoError(t, s.Store(ctx, e))
	}

	// Query with the "alpha" embedding — should return "alpha" first.
	results, err := s.Query(ctx, makeVec(dims, 0, 1.0), 3)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "alpha", results[0].Content)
	// Score for exact match (distance ≈ 0) should be near 1.0.
	assert.InDelta(t, 1.0, results[0].Score, 0.01)
}

func TestSemanticStore_QueryText(t *testing.T) {
	const dims = 1536
	db := newTestDB(t)
	content := "hello world"
	emb := &mockEmbedder{
		dims:    dims,
		vectors: map[string][]float32{content: makeVec(dims, 0, 1.0)},
	}

	s, err := semantic.NewStore(db, emb)
	require.NoError(t, err)

	ctx := context.Background()
	entry := pkg.MemoryEntry{Content: content, Source: "observation", Timestamp: time.Now().UTC()}
	require.NoError(t, s.Store(ctx, entry))

	priorCalls := emb.callCount
	results, err := s.QueryText(ctx, content, 5)
	require.NoError(t, err)
	require.Len(t, results, 1)
	// At least one more Embed call for the query text embedding.
	assert.GreaterOrEqual(t, emb.callCount, priorCalls+1)
}

func TestSemanticStore_StoreBatch(t *testing.T) {
	const dims = 1536
	db := newTestDB(t)
	emb := &mockEmbedder{
		dims: dims,
		vectors: map[string][]float32{
			"entry-a": makeVec(dims, 0, 1.0),
			"entry-b": makeVec(dims, 1, 1.0),
			"entry-c": makeVec(dims, 2, 1.0),
		},
	}

	s, err := semantic.NewStore(db, emb)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now().UTC()
	entries := []pkg.MemoryEntry{
		{Content: "entry-a", Source: "event", Timestamp: now},
		{Content: "entry-b", Source: "event", Timestamp: now},
		{Content: "entry-c", Source: "event", Timestamp: now},
	}

	require.NoError(t, s.StoreBatch(ctx, entries))

	// A single Embed call should have been made for all texts.
	assert.Equal(t, 1, emb.callCount, "StoreBatch should call Embed exactly once")

	// Verify all 3 rows exist in the DB.
	var count int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_entries").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestSemanticStore_ScoreMapping(t *testing.T) {
	cases := []struct {
		distance  float64
		wantScore float32
	}{
		{0.0, 1.0},
		{2.0, 0.0},
		{1.0, 0.5},
	}
	for _, tc := range cases {
		score := semantic.DistanceToScore(float32(tc.distance))
		assert.InDelta(t, tc.wantScore, score, 0.001,
			"distance %v should map to score %v", tc.distance, tc.wantScore)
	}
}

func TestSemanticStore_DimensionValidation(t *testing.T) {
	// NewStore now derives the expected dimension from the embedder rather than
	// a hardcoded constant. A zero or negative dimension is invalid and must
	// return an error; a valid non-zero dimension (even if different from the
	// schema's 1536) is accepted at construction time — mismatches are caught
	// at first insert/query by sqlite-vec.
	db := newTestDB(t)

	// Invalid (zero) dimension must be rejected.
	emb0 := &mockEmbedder{dims: 0}
	_, err := semantic.NewStore(db, emb0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid dimension")

	// A valid non-zero dimension is accepted at construction time.
	emb768 := &mockEmbedder{dims: 768}
	_, err = semantic.NewStore(db, emb768)
	require.NoError(t, err, "NewStore should accept any positive dimension at construction time")
}

func TestSemanticStore_PresetIDAndImportance(t *testing.T) {
	db := newTestDB(t)
	embedder := &mockEmbedder{
		dims: 1536,
		vectors: map[string][]float32{
			"preset content": makeVec(1536, 1, 1.0),
		},
	}
	s, err := semantic.NewStore(db, embedder)
	require.NoError(t, err)

	entry := pkg.MemoryEntry{
		ID:         "preset-id-123",
		Content:    "preset content",
		Source:     "conversation",
		Timestamp:  time.Now(),
		Importance: 0.9, // pre-set, should not be overridden
	}
	err = s.Store(context.Background(), entry)
	require.NoError(t, err)

	results, err := s.Query(context.Background(), makeVec(1536, 1, 1.0), 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "preset-id-123", results[0].ID)
	assert.Equal(t, float32(0.9), results[0].Importance)
}

func TestSemanticStore_ImportanceHeuristic(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		hasToolCall bool
		want        float32
	}{
		{"explicit reinforcement", "remember: always use UTC", false, 0.8},
		{"tool call turn", "Called homeassistant.get_state", true, 0.6},
		{"substantive exchange >200 tokens", strings.Repeat("word ", 201), false, 0.6},
		{"default conversation", "sounds good, thanks", false, 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := semantic.ComputeImportance(tc.content, tc.hasToolCall)
			assert.Equal(t, tc.want, got)
		})
	}
}
