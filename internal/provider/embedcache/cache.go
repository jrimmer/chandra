// Package embedcache provides a thread-safe LRU cache for embedding vectors.
//
// Embedding models are stateless: the same text always produces the same
// vector. In a conversational agent, the same query is often embedded multiple
// times across a session (e.g. "what did I say about X" → QueryText → Embed).
// The embed call dominates non-LLM latency (~100ms against a local Ollama
// instance). Caching eliminates that cost on repeated and near-repeated queries.
//
// Design choices:
//   - LRU eviction keeps the cache bounded regardless of usage patterns.
//   - TTL expiry means a config change (different model or dims) will not
//     silently serve stale vectors beyond the TTL window.
//   - Text content (not hash) is the map key: collision-free, and user
//     messages are short enough that key size is not a concern.
//   - Stats() is exposed for health endpoints and observability.
package embedcache

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jrimmer/chandra/internal/provider"
)

const (
	// DefaultCapacity is the default maximum number of cached embeddings.
	// 256 entries covers a full working session with room to spare.
	DefaultCapacity = 256

	// DefaultTTL is the default time an embedding stays valid in the cache.
	// 5 minutes balances hit rate against staleness after config changes.
	DefaultTTL = 5 * time.Minute
)

// Stats holds cache performance counters.
type Stats struct {
	Hits   int64
	Misses int64
	Size   int
}

// HitRate returns the fraction of Embed calls served from cache (0–1).
// Returns 0 when no calls have been made yet.
func (s Stats) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}

// cacheEntry is the internal value stored in the LRU list and map.
type cacheEntry struct {
	text    string    // map key (also stored here for safety)
	vector  []float32 // cached embedding
	expires time.Time
	elem    *list.Element // position in LRU list (front = most recently used)
}

// CachingEmbedder wraps any provider.EmbeddingProvider with an in-memory LRU
// cache. Individual text→embedding pairs are cached by text content. The cache
// is safe for concurrent use from multiple goroutines.
//
// If multiple goroutines embed the same text simultaneously, each will call
// through to the inner embedder independently. The last writer wins in the
// cache. This is a minor inefficiency but avoids request coalescing complexity;
// the practical impact is negligible since concurrent identical queries are rare.
type CachingEmbedder struct {
	inner provider.EmbeddingProvider

	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	items map[string]*cacheEntry
	lru   *list.List // front = most recently used, back = LRU candidate

	hits   atomic.Int64
	misses atomic.Int64
}

// Compile-time assertion.
var _ provider.EmbeddingProvider = (*CachingEmbedder)(nil)

// New creates a CachingEmbedder wrapping inner with the given capacity and TTL.
// cap must be ≥ 1. ttl must be > 0.
func New(inner provider.EmbeddingProvider, cap int, ttl time.Duration) *CachingEmbedder {
	if cap < 1 {
		cap = DefaultCapacity
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &CachingEmbedder{
		inner: inner,
		cap:   cap,
		ttl:   ttl,
		items: make(map[string]*cacheEntry, cap),
		lru:   list.New(),
	}
}

// Dimensions delegates to the inner embedder.
func (c *CachingEmbedder) Dimensions() int { return c.inner.Dimensions() }

// Embed satisfies provider.EmbeddingProvider. Texts with a live cache entry
// are served without calling the inner embedder. Cache misses and expired
// entries are fetched in a single batched call to the inner embedder.
func (c *CachingEmbedder) Embed(ctx context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	if len(req.Texts) == 0 {
		return provider.EmbeddingResponse{Dimensions: c.inner.Dimensions()}, nil
	}

	now := time.Now()
	results := make([][]float32, len(req.Texts))

	// missIdx maps position in the miss slice to the original request index.
	type missEntry struct {
		text    string
		origIdx int
	}
	var missList []missEntry

	c.mu.Lock()
	for i, text := range req.Texts {
		if e, ok := c.items[text]; ok && now.Before(e.expires) {
			// Cache hit — promote to front (most recently used).
			c.lru.MoveToFront(e.elem)
			results[i] = e.vector
			c.mu.Unlock()
			c.hits.Add(1)
			c.mu.Lock()
		} else {
			if ok {
				// Expired — remove before re-fetching.
				c.evictLocked(e)
			}
			missList = append(missList, missEntry{text: text, origIdx: i})
			c.mu.Unlock()
			c.misses.Add(1)
			c.mu.Lock()
		}
	}
	c.mu.Unlock()

	if len(missList) == 0 {
		return provider.EmbeddingResponse{
			Embeddings: results,
			Dimensions: c.inner.Dimensions(),
		}, nil
	}

	// Batch-embed all misses in one call.
	missTexts := make([]string, len(missList))
	for i, m := range missList {
		missTexts[i] = m.text
	}
	resp, err := c.inner.Embed(ctx, provider.EmbeddingRequest{Texts: missTexts})
	if err != nil {
		return provider.EmbeddingResponse{}, fmt.Errorf("embedcache: inner embed: %w", err)
	}
	if len(resp.Embeddings) != len(missList) {
		return provider.EmbeddingResponse{}, fmt.Errorf(
			"embedcache: inner embed returned %d embeddings for %d texts",
			len(resp.Embeddings), len(missList),
		)
	}

	// Populate results and cache.
	expires := now.Add(c.ttl)
	c.mu.Lock()
	for i, m := range missList {
		vec := resp.Embeddings[i]
		results[m.origIdx] = vec
		c.storeLocked(m.text, vec, expires)
	}
	c.mu.Unlock()

	return provider.EmbeddingResponse{
		Embeddings: results,
		Dimensions: c.inner.Dimensions(),
		Model:      resp.Model,
	}, nil
}

// Stats returns a snapshot of cache performance counters and current size.
// Safe to call concurrently.
func (c *CachingEmbedder) Stats() Stats {
	c.mu.Lock()
	size := len(c.items)
	c.mu.Unlock()
	return Stats{
		Hits:   c.hits.Load(),
		Misses: c.misses.Load(),
		Size:   size,
	}
}

// Invalidate removes a single entry from the cache. Useful for testing.
func (c *CachingEmbedder) Invalidate(text string) {
	c.mu.Lock()
	if e, ok := c.items[text]; ok {
		c.evictLocked(e)
	}
	c.mu.Unlock()
}

// Flush removes all entries from the cache.
func (c *CachingEmbedder) Flush() {
	c.mu.Lock()
	c.items = make(map[string]*cacheEntry, c.cap)
	c.lru.Init()
	c.mu.Unlock()
}

// storeLocked adds or updates a cache entry. Must be called with c.mu held.
func (c *CachingEmbedder) storeLocked(text string, vec []float32, expires time.Time) {
	// If already present (concurrent writers), update in place.
	if e, ok := c.items[text]; ok {
		e.vector = vec
		e.expires = expires
		c.lru.MoveToFront(e.elem)
		return
	}
	// Evict LRU entry if at capacity.
	if len(c.items) >= c.cap {
		if back := c.lru.Back(); back != nil {
			c.evictLocked(back.Value.(*cacheEntry))
		}
	}
	e := &cacheEntry{text: text, vector: vec, expires: expires}
	e.elem = c.lru.PushFront(e)
	c.items[text] = e
}

// evictLocked removes an entry. Must be called with c.mu held.
func (c *CachingEmbedder) evictLocked(e *cacheEntry) {
	c.lru.Remove(e.elem)
	delete(c.items, e.text)
}
