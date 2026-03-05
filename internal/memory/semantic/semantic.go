// Package semantic provides embedding-based semantic memory storage and retrieval
// backed by SQLite with the sqlite-vec extension and FTS5 full-text search.
//
// Retrieval uses hybrid BM25+vector fusion:
//   - BM25 (FTS5) handles keyword recall and exact/near-exact phrase matches.
//   - Vector KNN handles paraphrase and semantic similarity.
//   - Reciprocal Rank Fusion (RRF) merges the two ranked lists without
//     requiring normalised score scales — only rank positions are used.
//
// This addresses the "rephrasing miss" problem: pure vector search degrades
// when the query is phrased differently from the stored text; BM25 fills the
// gap. Together they are consistently better than either alone.
package semantic

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
)

// rrfK is the RRF smoothing constant. The standard value (60) was established
// by Cormack, Clarke & Buettcher (SIGIR 2009). Higher values reduce the
// advantage of top-ranked results; lower values amplify it.
const rrfK = 60

// candidateMultiplier controls how many candidates to fetch from each
// retrieval path before fusion. Using 3×topN gives the fusion step enough
// signal without fetching too many rows.
const candidateMultiplier = 3

// SemanticStore defines the contract for semantic memory operations.
type SemanticStore interface {
	Store(ctx context.Context, entry pkg.MemoryEntry) error
	StoreBatch(ctx context.Context, entries []pkg.MemoryEntry) error
	// Query retrieves entries by pre-computed embedding (pure vector KNN).
	// Pass userID="" to return all entries regardless of owner (admin/CLI use).
	Query(ctx context.Context, embedding []float32, topN int, userID string) ([]pkg.MemoryEntry, error)
	// QueryText retrieves entries using hybrid BM25+vector fusion.
	// Prefer QueryText over Query for text inputs — it produces better recall.
	// Pass userID="" to return all entries regardless of owner (admin/CLI use).
	QueryText(ctx context.Context, text string, topN int, userID string) ([]pkg.MemoryEntry, error)
}

// Compile-time assertion that *Store satisfies SemanticStore.
var _ SemanticStore = (*Store)(nil)

// Store is a semantic memory store backed by SQLite + sqlite-vec + FTS5.
type Store struct {
	db       *sql.DB
	embedder provider.EmbeddingProvider
	dims     int // dimension count as reported by the embedder
}

// NewStore creates a new Store using the provided database connection and
// embedding provider. The embedder's declared dimension count is used at
// runtime; if the SQLite schema was created with a different dimension the
// first Store/Query call will return a descriptive error from sqlite-vec.
func NewStore(db *sql.DB, embedder provider.EmbeddingProvider) (*Store, error) {
	dims := embedder.Dimensions()
	if dims <= 0 {
		return nil, fmt.Errorf("semantic: embedder reports invalid dimension count %d", dims)
	}
	// Reconcile the vec0 table dimensions with the embedder.
	// vec0 rejects inserts whose vector length doesn't match the schema, so if
	// the configured embedding model changed (e.g. OpenAI 1536 → Ollama 768),
	// drop and recreate the table. Embeddings are a derived value — they can
	// always be rebuilt from the source text in memory_entries.
	if err := reconcileEmbeddingDims(db, dims); err != nil {
		return nil, fmt.Errorf("semantic: reconcile dims: %w", err)
	}
	s := &Store{db: db, embedder: embedder, dims: dims}
	// Probe FTS5 availability. If the binary was built without -tags sqlite_fts5,
	// the memory_fts table won't exist and BM25 queries will silently degrade to
	// vector-only. Log a warning so operators know retrieval quality is reduced.
	if ok, _ := s.fts5Available(); !ok {
		fmt.Println("semantic: WARNING: FTS5 not available — hybrid BM25+vector search disabled; rebuild with -tags sqlite_fts5")
	}
	return s, nil
}

// fts5Available reports whether the memory_fts virtual table exists and is queryable.
func (s *Store) fts5Available() (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='memory_fts'`,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// reconcileEmbeddingDims checks the declared dimension of the memory_embeddings
// vec0 table and recreates it when it doesn't match dims.
func reconcileEmbeddingDims(db *sql.DB, dims int) error {
	var createSQL string
	err := db.QueryRow(
		`SELECT sql FROM sqlite_schema WHERE type='table' AND name='memory_embeddings'`,
	).Scan(&createSQL)
	if err != nil {
		return nil
	}

	declaredDims := 0
	if idx := strings.Index(createSQL, "FLOAT["); idx != -1 {
		rest := createSQL[idx+6:]
		if end := strings.Index(rest, "]"); end != -1 {
			_, _ = fmt.Sscanf(rest[:end], "%d", &declaredDims)
		}
	}

	if declaredDims == 0 || declaredDims == dims {
		return nil
	}

	_, err = db.Exec(`DROP TABLE IF EXISTS memory_embeddings`)
	if err != nil {
		return fmt.Errorf("drop memory_embeddings: %w", err)
	}
	_, err = db.Exec(fmt.Sprintf(
		`CREATE VIRTUAL TABLE memory_embeddings USING vec0(id TEXT PRIMARY KEY, embedding FLOAT[%d])`,
		dims,
	))
	if err != nil {
		return fmt.Errorf("recreate memory_embeddings (%d dims): %w", dims, err)
	}
	return nil
}

// ComputeImportance derives an importance score from the content text and
// whether the turn contained a tool call. Exported so tests can exercise the
// heuristic directly.
func ComputeImportance(content string, hasToolCall bool) float32 {
	lower := strings.ToLower(content)
	if strings.Contains(lower, "remember") || strings.Contains(lower, "important") {
		return 0.8
	}
	if hasToolCall {
		return 0.6
	}
	if len(strings.Fields(content)) > 200 {
		return 0.6
	}
	return 0.5
}

// DistanceToScore converts a cosine distance (range 0–2) to a similarity
// score (range 0–1). Exported so tests can exercise the formula directly.
func DistanceToScore(distance float32) float32 {
	return 1.0 - (distance / 2.0)
}

// Store embeds a single MemoryEntry and persists it to the database.
func (s *Store) Store(ctx context.Context, entry pkg.MemoryEntry) error {
	resp, err := s.embedder.Embed(ctx, provider.EmbeddingRequest{Texts: []string{entry.Content}})
	if err != nil {
		return fmt.Errorf("semantic: embed: %w", err)
	}
	if len(resp.Embeddings) == 0 {
		return fmt.Errorf("semantic: embed returned no embeddings")
	}
	return s.insertOne(ctx, entry, resp.Embeddings[0])
}

// StoreBatch embeds and stores multiple MemoryEntries in a single Embed call
// and a single database transaction.
func (s *Store) StoreBatch(ctx context.Context, entries []pkg.MemoryEntry) error {
	if len(entries) == 0 {
		return nil
	}

	texts := make([]string, len(entries))
	for i, e := range entries {
		texts[i] = e.Content
	}

	resp, err := s.embedder.Embed(ctx, provider.EmbeddingRequest{Texts: texts})
	if err != nil {
		return fmt.Errorf("semantic: embed batch: %w", err)
	}
	if len(resp.Embeddings) != len(entries) {
		return fmt.Errorf("semantic: embed batch: got %d embeddings for %d entries",
			len(resp.Embeddings), len(entries))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("semantic: begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for i, e := range entries {
		if err := insertOneInTx(ctx, tx, e, resp.Embeddings[i]); err != nil {
			return fmt.Errorf("semantic: store batch entry %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("semantic: commit batch: %w", err)
	}
	return nil
}

// Query retrieves the topN most semantically similar MemoryEntries using
// pure vector KNN (cosine distance via sqlite-vec). Use QueryText when you
// have a raw text query — hybrid retrieval produces better results.
func (s *Store) Query(ctx context.Context, embedding []float32, topN int, userID string) ([]pkg.MemoryEntry, error) {
	return s.vectorQuery(ctx, embedding, topN, userID)
}

// QueryText retrieves entries using hybrid BM25+vector fusion.
//
// Algorithm:
//  1. Embed the query text to get a vector.
//  2. Run BM25 search (FTS5) and vector KNN search concurrently.
//  3. Merge the two ranked lists using Reciprocal Rank Fusion (RRF).
//  4. Return the top N results ordered by RRF score.
//
// Falls back to pure vector search if FTS5 is unavailable or the query
// contains no indexable tokens.
// Pass userID="" to return entries for all users (admin/CLI use).
func (s *Store) QueryText(ctx context.Context, text string, topN int, userID string) ([]pkg.MemoryEntry, error) {
	if topN <= 0 {
		return nil, nil
	}

	// Embed and run both searches concurrently.
	candidate := topN * candidateMultiplier

	type embedResult struct {
		embedding []float32
		err       error
	}
	embedCh := make(chan embedResult, 1)
	go func() {
		resp, err := s.embedder.Embed(ctx, provider.EmbeddingRequest{Texts: []string{text}})
		if err != nil || len(resp.Embeddings) == 0 {
			embedCh <- embedResult{err: fmt.Errorf("semantic: embed query: %w", err)}
			return
		}
		embedCh <- embedResult{embedding: resp.Embeddings[0]}
	}()

	// BM25 search runs while embedding is in flight.
	bm25Results, bm25Err := s.bm25Query(ctx, text, candidate, userID)

	// Wait for embedding.
	er := <-embedCh
	if er.err != nil {
		return nil, er.err
	}

	// Vector search with the embedding.
	vectorResults, vecErr := s.vectorQuery(ctx, er.embedding, candidate, userID)

	// If both searches failed, propagate the vector error (more reliable).
	if vecErr != nil && bm25Err != nil {
		return nil, fmt.Errorf("semantic: both retrievers failed: vector=%w bm25=%v", vecErr, bm25Err)
	}

	// Fuse results.
	fused := reciprocalRankFusion(bm25Results, vectorResults, topN)
	return fused, nil
}

// bm25Query searches memory_fts using FTS5 BM25 ranking.
// Returns at most limit results, ordered by BM25 score descending.
// Returns nil (not an error) when the FTS table doesn't exist yet or the
// query contains no indexable tokens.
func (s *Store) bm25Query(ctx context.Context, text string, limit int, userID string) ([]pkg.MemoryEntry, error) {
	// Sanitise query: FTS5 MATCH syntax is strict — wrap phrase in quotes for
	// simple safety, strip characters that break the parser.
	ftsQuery := sanitiseFTSQuery(text)
	if ftsQuery == "" {
		return nil, nil
	}

	var q string
	var args []any
	if userID != "" {
		q = `
			SELECT m.id, m.user_id, m.content, m.source, m.timestamp, m.importance,
			       -bm25(memory_fts) AS score
			FROM memory_fts
			JOIN memory_entries m ON m.id = memory_fts.id
			WHERE memory_fts MATCH ?
			  AND m.user_id = ?
			ORDER BY score DESC
			LIMIT ?`
		args = []any{ftsQuery, userID, limit}
	} else {
		q = `
			SELECT m.id, m.user_id, m.content, m.source, m.timestamp, m.importance,
			       -bm25(memory_fts) AS score
			FROM memory_fts
			JOIN memory_entries m ON m.id = memory_fts.id
			WHERE memory_fts MATCH ?
			ORDER BY score DESC
			LIMIT ?`
		args = []any{ftsQuery, limit}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		// FTS table may not exist (pre-migration DB) or query may be invalid —
		// treat as empty result rather than hard failure.
		return nil, nil //nolint:nilerr
	}
	defer rows.Close()

	return scanMemoryRows(rows, true)
}

// vectorQuery performs KNN search over memory_embeddings using sqlite-vec.
func (s *Store) vectorQuery(ctx context.Context, embedding []float32, topN int, userID string) ([]pkg.MemoryEntry, error) {
	serialized := store.SerializeFloat32(embedding)

	var q string
	var args []any
	if userID != "" {
		q = `
			SELECT m.id, m.user_id, m.content, m.source, m.timestamp, m.importance,
			       v.distance
			FROM (
			    SELECT id, distance
			    FROM memory_embeddings
			    WHERE embedding MATCH ? AND k = ?
			    ORDER BY distance
			) v
			JOIN memory_entries m ON m.id = v.id
			WHERE m.user_id = ?
			ORDER BY v.distance ASC`
		args = []any{serialized, topN, userID}
	} else {
		q = `
			SELECT m.id, m.user_id, m.content, m.source, m.timestamp, m.importance,
			       v.distance
			FROM (
			    SELECT id, distance
			    FROM memory_embeddings
			    WHERE embedding MATCH ? AND k = ?
			    ORDER BY distance
			) v
			JOIN memory_entries m ON m.id = v.id
			ORDER BY v.distance ASC`
		args = []any{serialized, topN}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("semantic: vector query: %w", err)
	}
	defer rows.Close()

	return scanMemoryRows(rows, false)
}

// scanMemoryRows reads query results into MemoryEntry slices.
// If scoreIsRaw is true, the 6th column is a raw BM25 score (higher = better).
// If false, the 6th column is a cosine distance (lower = better) and is
// converted to a similarity score via DistanceToScore.
func scanMemoryRows(rows *sql.Rows, scoreIsRaw bool) ([]pkg.MemoryEntry, error) {
	var results []pkg.MemoryEntry
	for rows.Next() {
		var entry pkg.MemoryEntry
		var ts int64
		var rawScore float32

		if err := rows.Scan(&entry.ID, &entry.UserID, &entry.Content, &entry.Source, &ts, &entry.Importance, &rawScore); err != nil {
			return nil, fmt.Errorf("semantic: scan row: %w", err)
		}
		entry.Timestamp = time.Unix(ts, 0).UTC()
		if scoreIsRaw {
			entry.Score = rawScore // BM25: already higher=better
		} else {
			entry.Score = DistanceToScore(rawScore) // cosine distance → similarity
		}
		results = append(results, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("semantic: iterate rows: %w", err)
	}
	return results, nil
}

// reciprocalRankFusion merges two ranked lists using RRF.
//
// RRF score for document d = Σ 1/(k + rank_i(d))
// where rank_i(d) is the 1-based rank of d in list i (or absent → not scored).
// Documents appearing in both lists are strongly boosted; those in only one
// list still contribute via their single-list rank.
//
// Reference: Cormack, Clarke & Buettcher, SIGIR 2009.
func reciprocalRankFusion(bm25, vector []pkg.MemoryEntry, topN int) []pkg.MemoryEntry {
	type scored struct {
		entry pkg.MemoryEntry
		rrf   float64
	}

	scores := make(map[string]*scored)

	addRanked := func(results []pkg.MemoryEntry) {
		for rank, entry := range results {
			contrib := 1.0 / float64(rrfK+rank+1)
			if s, ok := scores[entry.ID]; ok {
				s.rrf += contrib
			} else {
				e := entry // copy
				scores[entry.ID] = &scored{entry: e, rrf: contrib}
			}
		}
	}

	addRanked(bm25)
	addRanked(vector)

	// Collect and sort by RRF score descending.
	all := make([]*scored, 0, len(scores))
	for _, s := range scores {
		all = append(all, s)
	}

	// Simple insertion sort — result set is small (≤ 3×topN entries).
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].rrf > all[j-1].rrf; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}

	if topN > len(all) {
		topN = len(all)
	}
	result := make([]pkg.MemoryEntry, topN)
	for i := range result {
		e := all[i].entry
		e.Score = float32(all[i].rrf) // expose RRF score for observability
		result[i] = e
	}
	return result
}

// sanitiseFTSQuery converts free text into a safe FTS5 MATCH expression.
// Strategy: extract words, join with implicit AND (FTS5 default), wrap each
// in double-quotes to treat them as phrase tokens rather than FTS operators.
// Returns "" if no indexable tokens remain after sanitisation.
func sanitiseFTSQuery(text string) string {
	// Split on whitespace, strip non-alphanumeric-ish characters.
	words := strings.Fields(text)
	var tokens []string
	for _, w := range words {
		// Remove FTS5 special characters: " * ^ ( ) { } [ ] : .
		cleaned := strings.Map(func(r rune) rune {
			switch r {
			case '"', '*', '^', '(', ')', '{', '}', '[', ']', ':', '.', ',', ';', '?', '!':
				return -1
			}
			return r
		}, w)
		if len(cleaned) >= 2 { // FTS5 minimum token length is usually 2
			tokens = append(tokens, `"`+cleaned+`"`)
		}
	}
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " ")
}

// insertOne wraps a single entry insertion in its own transaction.
func (s *Store) insertOne(ctx context.Context, entry pkg.MemoryEntry, embedding []float32) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("semantic: begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if err := insertOneInTx(ctx, tx, entry, embedding); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("semantic: commit: %w", err)
	}
	return nil
}

// insertOneInTx inserts a single MemoryEntry, its embedding, and its FTS5
// index row inside an existing transaction.
func insertOneInTx(ctx context.Context, tx *sql.Tx, entry pkg.MemoryEntry, embedding []float32) error {
	if entry.ID == "" {
		entry.ID = store.NewID()
	}
	if entry.Importance == 0 {
		hasToolCall := strings.Contains(strings.ToLower(entry.Content), "called ")
		entry.Importance = ComputeImportance(entry.Content, hasToolCall)
	}

	ts := entry.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	_, err := tx.ExecContext(ctx,
		`INSERT INTO memory_entries (id, content, source, timestamp, importance)
		 VALUES (?, ?, ?, ?, ?)`,
		entry.ID, entry.Content, entry.Source, ts.Unix(), entry.Importance,
	)
	if err != nil {
		return fmt.Errorf("semantic: insert memory_entries: %w", err)
	}

	// Populate FTS5 index. Soft-fail if the table doesn't exist yet
	// (pre-migration DB) — the BM25 path will silently degrade to vector-only.
	_, _ = tx.ExecContext(ctx,
		`INSERT INTO memory_fts (id, content) VALUES (?, ?)`,
		entry.ID, entry.Content,
	)

	_, err = tx.ExecContext(ctx,
		`INSERT INTO memory_embeddings (id, embedding) VALUES (?, ?)`,
		entry.ID, store.SerializeFloat32(embedding),
	)
	if err != nil {
		return fmt.Errorf("semantic: insert memory_embeddings: %w", err)
	}
	return nil
}

// Ensure sync is used (needed for the concurrent embed goroutine).
var _ = sync.Mutex{}
