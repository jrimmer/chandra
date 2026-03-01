// Package semantic provides embedding-based semantic memory storage and retrieval
// backed by SQLite with the sqlite-vec extension.
package semantic

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
)

// SemanticStore defines the contract for semantic memory operations.
type SemanticStore interface {
	Store(ctx context.Context, entry pkg.MemoryEntry) error
	StoreBatch(ctx context.Context, entries []pkg.MemoryEntry) error
	Query(ctx context.Context, embedding []float32, topN int) ([]pkg.MemoryEntry, error)
	QueryText(ctx context.Context, text string, topN int) ([]pkg.MemoryEntry, error)
}

// Compile-time assertion that *Store satisfies SemanticStore.
var _ SemanticStore = (*Store)(nil)

// Store is a semantic memory store backed by SQLite + sqlite-vec.
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
	return &Store{db: db, embedder: embedder, dims: dims}, nil
}

// ComputeImportance derives an importance score from the content text and
// whether the turn contained a tool call. It is exported so tests can exercise
// the heuristic directly. Callers that have already set entry.Importance should
// not call this — Store() respects the caller-provided value when it is
// non-zero.
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
// If entry.ID is empty a new ULID is generated. If entry.Importance is zero
// it is computed via heuristics.
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
// cosine distance over the provided embedding vector.
func (s *Store) Query(ctx context.Context, embedding []float32, topN int) ([]pkg.MemoryEntry, error) {
	serialized := store.SerializeFloat32(embedding)

	const q = `
		SELECT m.id, m.content, m.source, m.timestamp, m.importance,
		       vec_distance_cosine(e.embedding, ?) AS distance
		FROM memory_embeddings e
		JOIN memory_entries m ON m.id = e.id
		ORDER BY distance ASC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, serialized, topN)
	if err != nil {
		return nil, fmt.Errorf("semantic: query: %w", err)
	}
	defer rows.Close()

	var results []pkg.MemoryEntry
	for rows.Next() {
		var entry pkg.MemoryEntry
		var ts int64
		var distance float32

		if err := rows.Scan(&entry.ID, &entry.Content, &entry.Source, &ts, &entry.Importance, &distance); err != nil {
			return nil, fmt.Errorf("semantic: scan row: %w", err)
		}
		entry.Timestamp = time.Unix(ts, 0).UTC()
		entry.Score = DistanceToScore(distance)
		results = append(results, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("semantic: iterate rows: %w", err)
	}
	return results, nil
}

// QueryText embeds the given text and then calls Query.
func (s *Store) QueryText(ctx context.Context, text string, topN int) ([]pkg.MemoryEntry, error) {
	resp, err := s.embedder.Embed(ctx, provider.EmbeddingRequest{Texts: []string{text}})
	if err != nil {
		return nil, fmt.Errorf("semantic: embed query text: %w", err)
	}
	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("semantic: embed query text returned no embeddings")
	}
	return s.Query(ctx, resp.Embeddings[0], topN)
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

// insertOneInTx inserts a single MemoryEntry and its embedding inside an
// existing transaction.
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

	_, err = tx.ExecContext(ctx,
		`INSERT INTO memory_embeddings (id, embedding) VALUES (?, ?)`,
		entry.ID, store.SerializeFloat32(embedding),
	)
	if err != nil {
		return fmt.Errorf("semantic: insert memory_embeddings: %w", err)
	}
	return nil
}
