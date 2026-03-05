package embedcache_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/internal/provider/embedcache"
)

// -------------------------------------------------------------------------
// Test double: countingEmbedder tracks how many times the inner Embed is called.
// -------------------------------------------------------------------------

type countingEmbedder struct {
	mu        sync.Mutex
	callCount int
	dims      int
	// Optional: return this error on the next call.
	nextErr error
}

func newCounter(dims int) *countingEmbedder { return &countingEmbedder{dims: dims} }

func (e *countingEmbedder) Embed(_ context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.nextErr != nil {
		err := e.nextErr
		e.nextErr = nil
		return provider.EmbeddingResponse{}, err
	}
	e.callCount++
	embs := make([][]float32, len(req.Texts))
	for i, text := range req.Texts {
		// Produce a deterministic vector: first element = float32(len(text)).
		vec := make([]float32, e.dims)
		vec[0] = float32(len(text))
		embs[i] = vec
	}
	return provider.EmbeddingResponse{
		Embeddings: embs,
		Dimensions: e.dims,
		Model:      "counting-embedder",
	}, nil
}

func (e *countingEmbedder) Dimensions() int { return e.dims }
func (e *countingEmbedder) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.callCount
}

// -------------------------------------------------------------------------
// Tests.
// -------------------------------------------------------------------------

func TestCachingEmbedder_CacheHitAvoidsInnerCall(t *testing.T) {
	inner := newCounter(4)
	c := embedcache.New(inner, 256, time.Minute)
	ctx := context.Background()

	// First call: cache miss → inner called.
	r1, err := c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"hello world"}})
	require.NoError(t, err)
	require.Len(t, r1.Embeddings, 1)
	assert.Equal(t, 1, inner.Calls(), "first call must reach inner embedder")

	// Second call, same text: cache hit → inner NOT called again.
	r2, err := c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"hello world"}})
	require.NoError(t, err)
	assert.Equal(t, 1, inner.Calls(), "second call for same text must be served from cache")
	assert.Equal(t, r1.Embeddings[0], r2.Embeddings[0], "cached embedding must equal original")
}

func TestCachingEmbedder_DifferentTextsCallInnerSeparately(t *testing.T) {
	inner := newCounter(4)
	c := embedcache.New(inner, 256, time.Minute)
	ctx := context.Background()

	_, err := c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"text a"}})
	require.NoError(t, err)
	_, err = c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"text b"}})
	require.NoError(t, err)

	assert.Equal(t, 2, inner.Calls(), "two distinct texts must produce two inner calls")
}

func TestCachingEmbedder_BatchPartialCacheHit(t *testing.T) {
	inner := newCounter(4)
	c := embedcache.New(inner, 256, time.Minute)
	ctx := context.Background()

	// Pre-warm "alpha" into cache.
	_, err := c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"alpha"}})
	require.NoError(t, err)
	require.Equal(t, 1, inner.Calls())

	// Batch request: "alpha" (cached) + "beta" (miss) + "gamma" (miss).
	resp, err := c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"alpha", "beta", "gamma"}})
	require.NoError(t, err)
	require.Len(t, resp.Embeddings, 3)

	// Inner should have been called once more (batch of 2 misses).
	assert.Equal(t, 2, inner.Calls(), "partial batch: only misses should reach inner embedder")

	// Embedding at index 0 (alpha) is from cache; 1 and 2 are fresh.
	assert.Equal(t, float32(5), resp.Embeddings[0][0], "alpha embedding must come from cache (len=5)")
	assert.Equal(t, float32(4), resp.Embeddings[1][0], "beta embedding fresh (len=4)")
	assert.Equal(t, float32(5), resp.Embeddings[2][0], "gamma embedding fresh (len=5)")
}

func TestCachingEmbedder_TTLExpiry(t *testing.T) {
	inner := newCounter(4)
	// Very short TTL so we can test expiry without sleeping long.
	c := embedcache.New(inner, 256, 50*time.Millisecond)
	ctx := context.Background()

	_, err := c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"expiry test"}})
	require.NoError(t, err)
	assert.Equal(t, 1, inner.Calls())

	// Wait for entry to expire.
	time.Sleep(80 * time.Millisecond)

	// Second call: TTL has passed, entry expired → inner called again.
	_, err = c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"expiry test"}})
	require.NoError(t, err)
	assert.Equal(t, 2, inner.Calls(), "expired entry must not be served from cache")
}

func TestCachingEmbedder_LRUEviction(t *testing.T) {
	inner := newCounter(4)
	// Tiny cache: 2 slots only.
	c := embedcache.New(inner, 2, time.Minute)
	ctx := context.Background()

	embed := func(text string) {
		t.Helper()
		_, err := c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{text}})
		require.NoError(t, err)
	}

	embed("first")  // fills slot 1: cache=[first]
	embed("second") // fills slot 2: cache=[second(MRU), first(LRU)]
	embed("third")  // evicts first (LRU): cache=[third(MRU), second]
	assert.Equal(t, 3, inner.Calls())

	// "first" was evicted — re-embedding must call inner.
	// Re-inserting first evicts second (new LRU): cache=[first(MRU), third]
	embed("first")
	assert.Equal(t, 4, inner.Calls(), "evicted entry must re-hit inner embedder")

	// "third" survived (was MRU when first was re-inserted).
	embed("third")
	assert.Equal(t, 4, inner.Calls(), "third must still be cached")

	// "second" was evicted when first was re-inserted.
	embed("second")
	assert.Equal(t, 5, inner.Calls(), "second must have been evicted by first re-insertion")
}

func TestCachingEmbedder_LRUPromotesOnAccess(t *testing.T) {
	inner := newCounter(4)
	c := embedcache.New(inner, 2, time.Minute)
	ctx := context.Background()

	embed := func(text string) {
		t.Helper()
		_, err := c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{text}})
		require.NoError(t, err)
	}

	embed("first")  // slot 1 — will be LRU
	embed("second") // slot 2
	embed("first")  // cache hit — promotes first to MRU; now second is LRU
	embed("third")  // evicts second (LRU), not first
	assert.Equal(t, 3, inner.Calls()) // only 3 misses: first, second, third

	// first should still be cached (promoted).
	embed("first")
	assert.Equal(t, 3, inner.Calls(), "first must still be cached after LRU promotion")

	// second was evicted.
	embed("second")
	assert.Equal(t, 4, inner.Calls(), "second must have been evicted by third")
}

func TestCachingEmbedder_InnerError_NotCached(t *testing.T) {
	inner := newCounter(4)
	c := embedcache.New(inner, 256, time.Minute)
	ctx := context.Background()

	inner.nextErr = errors.New("ollama: connection refused")
	_, err := c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"error text"}})
	require.Error(t, err, "inner error must propagate")

	// Retry after error recovery: must call inner again (error was not cached).
	_, err = c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"error text"}})
	require.NoError(t, err)
	assert.Equal(t, 1, inner.Calls(), "failed embed must not be cached; retry must reach inner")
}

func TestCachingEmbedder_EmptyRequest(t *testing.T) {
	inner := newCounter(4)
	c := embedcache.New(inner, 256, time.Minute)

	resp, err := c.Embed(context.Background(), provider.EmbeddingRequest{Texts: nil})
	require.NoError(t, err)
	assert.Empty(t, resp.Embeddings, "empty request must return empty response")
	assert.Equal(t, 0, inner.Calls(), "empty request must not call inner")
}

func TestCachingEmbedder_Stats(t *testing.T) {
	inner := newCounter(4)
	c := embedcache.New(inner, 256, time.Minute)
	ctx := context.Background()

	embed := func(text string) {
		_, _ = c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{text}})
	}

	embed("a") // miss
	embed("b") // miss
	embed("a") // hit
	embed("a") // hit
	embed("c") // miss

	stats := c.Stats()
	assert.Equal(t, int64(2), stats.Hits, "hit count")
	assert.Equal(t, int64(3), stats.Misses, "miss count")
	assert.Equal(t, 3, stats.Size, "cache size: a, b, c")
	assert.InDelta(t, 0.4, stats.HitRate(), 0.001, "hit rate: 2/5")
}

func TestCachingEmbedder_Flush(t *testing.T) {
	inner := newCounter(4)
	c := embedcache.New(inner, 256, time.Minute)
	ctx := context.Background()

	_, _ = c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"persistent text"}})
	assert.Equal(t, 1, inner.Calls())

	c.Flush()

	_, _ = c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"persistent text"}})
	assert.Equal(t, 2, inner.Calls(), "after flush, all entries must be re-fetched")
}

func TestCachingEmbedder_ConcurrentAccess(t *testing.T) {
	inner := newCounter(4)
	c := embedcache.New(inner, 256, time.Minute)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Embed(ctx, provider.EmbeddingRequest{Texts: []string{"concurrent text"}})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	// No assertion on call count (concurrent misses may all reach inner).
	// The test passes if there's no race condition (run with -race).
	stats := c.Stats()
	assert.Greater(t, stats.Hits+stats.Misses, int64(0), "some calls must have been counted")
}
