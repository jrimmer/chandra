package integration

// memory_hybrid_test.go
//
// Integration tests for hybrid BM25+vector memory retrieval (migration 007).
//
// # The fixedEmbedder problem
//
// The existing integration tests use fixedEmbedder — a mock that returns the
// same vector (all 0.1) for every input text. This is fine for storage
// lifecycle tests (does a fact get stored? does it persist?) but it makes
// vector KNN ranking meaningless: cosine distance is 0 for every pair, so
// results are returned in insertion order rather than by relevance.
//
// Before hybrid search this was acceptable because there was only one ranking
// signal. With two paths (BM25 + vector KNN merged by RRF), we need to verify
// that BOTH paths contribute correctly and that the merged result is better
// than either alone. fixedEmbedder is blind to this.
//
// # rankedEmbedder
//
// This file introduces rankedEmbedder: a mock that uses FNV-1a word hashing
// to produce vectors where texts sharing vocabulary get similar vectors and
// texts with different vocabulary get different vectors. It enables ranking
// tests to be meaningful without requiring a real Ollama call.
//
// Design:
//   - Each word in the text contributes 1.0 to the dimension at (fnv32a(word) % dims).
//   - The resulting vector is L2-normalised so cosine distance is well-defined.
//   - Texts sharing words → overlapping active dimensions → smaller distance.
//   - Texts with no shared words → disjoint active dimensions → larger distance.
//
// # Tests in this file
//
//   TestMemory_RankedEmbedder_SimilarTextsScore — sanity check: similar texts
//   have a smaller cosine distance than dissimilar texts.
//
//   TestMemory_HybridRankingSelectsRelevantEntry — store 10 topically diverse
//   entries, query on a specific topic; assert the correct entry ranks first.
//
//   TestMemory_BackfillEnablesBM25ForPreexistingRows — simulate rows that
//   predated migration 007 (present in memory_entries but not memory_fts);
//   run the backfill SQL; assert BM25 finds them.
//
//   TestMemory_RecallUnderNoise — store 30 noise entries + 1 target; query on
//   target-specific keywords; assert target appears in top 5.
//
//   TestMemory_ExistingDesignIntent_RememberKeyword_ContentCheck — hardens the
//   design intent test for "remember" to assert the *correct* entry was
//   retrieved, not just that something was retrieved.

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jrimmer/chandra/internal/memory/semantic"
	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/pkg"
)

// -------------------------------------------------------------------------
// rankedEmbedder — FNV word-hash based mock embedding provider.
// -------------------------------------------------------------------------

// rankedEmbedder produces semantically meaningful vectors for test use.
// Two texts sharing vocabulary will have a smaller cosine distance than
// two texts with entirely different vocabularies. This makes ranking tests
// meaningful without requiring a real embedding service.
type rankedEmbedder struct {
	dims int
}

func newRankedEmbedder(dims int) *rankedEmbedder { return &rankedEmbedder{dims: dims} }

func (r *rankedEmbedder) Embed(_ context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	embs := make([][]float32, len(req.Texts))
	for i, text := range req.Texts {
		embs[i] = wordHashVec(text, r.dims)
	}
	return provider.EmbeddingResponse{
		Embeddings: embs,
		Model:      "ranked-mock-embedder",
		Dimensions: r.dims,
	}, nil
}

func (r *rankedEmbedder) Dimensions() int { return r.dims }

var _ provider.EmbeddingProvider = (*rankedEmbedder)(nil)

// wordHashVec produces a normalised float32 vector by accumulating word-level
// FNV-1a hash contributions. Each word in text contributes 1.0 to the
// dimension at (fnv32a(word) % dims). The result is L2-normalised.
func wordHashVec(text string, dims int) []float32 {
	vec := make([]float32, dims)
	words := strings.Fields(strings.ToLower(text))
	if len(words) == 0 {
		return vec
	}
	for _, word := range words {
		h := fnv.New32a()
		h.Write([]byte(word))
		idx := int(h.Sum32()) % dims
		vec[idx] += 1.0
	}
	// L2 normalise so cosine distance is well-defined (range 0–2).
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}
	return vec
}

// cosineDist computes the cosine distance (1 - cosine_similarity) between
// two normalised vectors. Range: 0 (identical) to 2 (opposite).
func cosineDist(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return 1.0 - dot
}

// -------------------------------------------------------------------------
// Helpers shared by this file.
// -------------------------------------------------------------------------

// newRankedHarness opens a fresh DB, runs migrations, and returns a
// semantic.Store backed by rankedEmbedder.
func newRankedHarness(t *testing.T, dims int) (*semantic.Store, context.Context) {
	t.Helper()
	s := openTestDB(t)
	emb := newRankedEmbedder(dims)
	sem, err := semantic.NewStore(s.DB(), emb)
	require.NoError(t, err)
	return sem, context.Background()
}

// storeEntry is a one-liner helper for storing a plain MemoryEntry.
func storeEntry(t *testing.T, sem *semantic.Store, ctx context.Context, content, source string) {
	t.Helper()
	require.NoError(t, sem.Store(ctx, pkg.MemoryEntry{
		Content:   content,
		Source:    source,
		Timestamp: time.Now().UTC(),
	}))
}

// -------------------------------------------------------------------------
// Test 1: sanity check — rankedEmbedder produces meaningful distance ordering.
// -------------------------------------------------------------------------

// TestMemory_RankedEmbedder_SimilarTextsScore verifies that the rankedEmbedder
// mock produces cosine distances that reflect vocabulary overlap, not just
// random noise. This is the prerequisite for ranking tests to be meaningful.
func TestMemory_RankedEmbedder_SimilarTextsScore(t *testing.T) {
	const dims = 64
	emb := newRankedEmbedder(dims)

	// Two texts with identical vocabulary → very small distance.
	a, _ := emb.Embed(context.Background(), provider.EmbeddingRequest{Texts: []string{"emergency contact telephone number"}})
	b, _ := emb.Embed(context.Background(), provider.EmbeddingRequest{Texts: []string{"emergency contact telephone"}})
	similar := cosineDist(a.Embeddings[0], b.Embeddings[0])

	// Two texts with completely different vocabulary → larger distance.
	c, _ := emb.Embed(context.Background(), provider.EmbeddingRequest{Texts: []string{"jazz concert saxophone improvisation"}})
	different := cosineDist(a.Embeddings[0], c.Embeddings[0])

	assert.Less(t, similar, different,
		"similar texts (shared vocabulary) must have smaller cosine distance than dissimilar texts; "+
			"got similar=%f, different=%f", similar, different)
	assert.Less(t, similar, 0.5,
		"near-identical texts must be very close (distance < 0.5); got %f", similar)
}

// -------------------------------------------------------------------------
// Test 2: hybrid ranking selects the relevant entry from a diverse pool.
// -------------------------------------------------------------------------

// TestMemory_HybridRankingSelectsRelevantEntry verifies that hybrid
// BM25+vector retrieval surfaces the topically correct entry when given a
// diverse pool of stored facts. Uses rankedEmbedder so both paths (keyword
// and vector) contribute meaningful signal.
func TestMemory_HybridRankingSelectsRelevantEntry(t *testing.T) {
	const dims = 64
	sem, ctx := newRankedHarness(t, dims)

	// Ten topically diverse entries. Only one is about emergency contacts.
	entries := []string{
		"the weather today is sunny with light clouds",
		"the football game ended in a draw after extra time",
		"my favourite recipe is pasta with olive oil and garlic",
		"the train departs from platform seven at half past eight",
		"please remember: my emergency contact is Claire Dupont at 555-0177",
		"the project deadline has been moved to the end of the quarter",
		"I usually prefer tea in the mornings rather than coffee",
		"the server upgrade is scheduled for this weekend",
		"my dog is a golden retriever named Biscuit",
		"the conference call starts at three in the afternoon",
	}
	for _, e := range entries {
		storeEntry(t, sem, ctx, e, "test")
	}

	// Query on emergency contact — specific enough that only entry[4] matches well.
	results, err := sem.QueryText(ctx, "emergency contact phone number", 5, "")
	require.NoError(t, err)
	require.NotEmpty(t, results, "query must return at least one result")

	// The emergency contact entry must rank first.
	assert.Contains(t, results[0].Content, "Claire Dupont",
		"hybrid search must rank the emergency contact entry first; got: %q", results[0].Content)
}

// -------------------------------------------------------------------------
// Test 3: migration 007 backfill makes pre-existing rows BM25-searchable.
// -------------------------------------------------------------------------

// TestMemory_BackfillEnablesBM25ForPreexistingRows verifies that rows which
// existed in memory_entries before migration 007 (and therefore never had a
// corresponding memory_fts row) become BM25-searchable after the backfill SQL
// runs. This tests the correctness of the migration 007 backfill clause:
//
//	INSERT INTO memory_fts (id, content)
//	SELECT id, content FROM memory_entries
//	WHERE id NOT IN (SELECT id FROM memory_fts);
func TestMemory_BackfillEnablesBM25ForPreexistingRows(t *testing.T) {
	s := openTestDB(t)
	db := s.DB()
	ctx := context.Background()

	// Insert a row directly into memory_entries, bypassing the Go layer.
	// This simulates a row that predates migration 007 — it will not have
	// a corresponding entry in memory_fts.
	preExistingID := "pre-migration-backfill-test-001"
	_, err := db.ExecContext(ctx,
		`INSERT INTO memory_entries (id, content, source, timestamp, importance) VALUES (?, ?, ?, ?, ?)`,
		preExistingID,
		"the administrator username is devops and the deployment key is stored in vault",
		"test-backfill",
		time.Now().Unix(),
		0.5,
	)
	require.NoError(t, err, "direct SQL insert into memory_entries must succeed")

	// Confirm the row is NOT in memory_fts yet.
	var ftsCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_fts WHERE id = ?`, preExistingID,
	).Scan(&ftsCount))
	require.Equal(t, 0, ftsCount,
		"row must not be in memory_fts before backfill — this simulates the pre-007 state")

	// Run the migration 007 backfill SQL.
	_, err = db.ExecContext(ctx, `
		INSERT INTO memory_fts (id, content)
		SELECT id, content FROM memory_entries
		WHERE id NOT IN (SELECT id FROM memory_fts)`)
	require.NoError(t, err, "backfill SQL must succeed")

	// Verify BM25 can now find the backfilled row.
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM memory_fts WHERE memory_fts MATCH '"deployment" "vault"' ORDER BY rank`)
	require.NoError(t, err, "BM25 query must succeed after backfill")
	defer rows.Close()

	var foundBackfilled bool
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		if id == preExistingID {
			foundBackfilled = true
		}
	}
	require.NoError(t, rows.Err())
	assert.True(t, foundBackfilled,
		"backfill must make the pre-migration row findable via BM25; it must appear in FTS results for 'deployment vault'")

	// Also verify the row count in memory_fts now includes the backfilled row.
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_fts WHERE id = ?`, preExistingID,
	).Scan(&ftsCount))
	assert.Equal(t, 1, ftsCount, "exactly one FTS row must exist for the backfilled entry")
}

// -------------------------------------------------------------------------
// Test 4: recall under noise — relevant entry surfaces from a large pool.
// -------------------------------------------------------------------------

// TestMemory_RecallUnderNoise verifies that the target memory entry surfaces
// in the top-5 results even when the semantic store contains 30 noise entries
// on unrelated topics. Uses rankedEmbedder so both BM25 and vector contribute.
//
// This tests the most important real-world property of the memory system:
// "does the right thing come back, even with many stored entries?"
func TestMemory_RecallUnderNoise(t *testing.T) {
	const dims = 128 // higher dims = better separation for noise test
	sem, ctx := newRankedHarness(t, dims)

	// 30 noise entries across unrelated topics. None contain the target keywords.
	noiseTopics := [][]string{
		{"the weather forecast shows rain throughout the week with light winds"},
		{"football season results: three wins, one draw, two losses"},
		{"recipe for chocolate chip cookies: flour butter sugar eggs vanilla"},
		{"the museum exhibition opens on friday and runs through september"},
		{"my morning routine starts with stretching followed by breakfast"},
		{"the book club meets every second thursday to discuss the month's reading"},
		{"gardening tip: water plants in the morning before the heat sets in"},
		{"the bus route changed last month and now stops at the corner"},
		{"yoga class schedule: monday wednesday friday at six in the evening"},
		{"the cats name is mittens and she likes sleeping in sunbeams"},
		{"the landlord replaced the heating system last november"},
		{"grocery list: milk eggs bread butter cheese apples oranges"},
		{"the gym is closed on public holidays but opens at six on weekdays"},
		{"movie recommendation: the documentary about coral reefs was fascinating"},
		{"bank holiday weekend means the post will be delayed by one day"},
		{"the hiking trail starts at the car park and takes about three hours"},
		{"piano practice schedule: thirty minutes every morning before work"},
		{"the dentist appointment is next tuesday at half past two"},
		{"parking permit renewal is due at the end of the month"},
		{"the neighbour's dog barks at night which makes sleeping difficult"},
		{"the office printer needs a new toner cartridge"},
		{"subscription renewal reminder: streaming service renews on the fifteenth"},
		{"the local library has extended its opening hours on saturdays"},
		{"team standup is every weekday at nine fifteen via video call"},
		{"the flight to auckland departs at seven forty in the morning"},
		{"house insurance renewal quote arrived and it is higher than last year"},
		{"the plumber is coming thursday to fix the bathroom tap"},
		{"cooking class starts in two weeks: italian cuisine theme"},
		{"the podcast about history has a new episode every tuesday"},
		{"eye test overdue: optician appointment should be booked this month"},
	}

	for i, noise := range noiseTopics {
		storeEntry(t, sem, ctx, noise[0], fmt.Sprintf("noise-%d", i))
	}

	// Single target entry with highly specific, rare keywords not present in noise.
	targetContent := "remember: the kubernetes cluster telemetry dashboard is at grafana.internal and the prometheus scrape interval is fifteen seconds"
	storeEntry(t, sem, ctx, targetContent, "target")

	// Query using keywords specific to the target.
	results, err := sem.QueryText(ctx, "kubernetes telemetry dashboard prometheus grafana", 5, "")
	require.NoError(t, err)
	require.NotEmpty(t, results, "recall under noise must return at least one result")

	// The target must appear in the top 5.
	found := false
	for _, r := range results {
		if strings.Contains(r.Content, "grafana.internal") || strings.Contains(r.Content, "prometheus") {
			found = true
			break
		}
	}
	assert.True(t, found,
		"target entry must appear in top 5 results from a pool of 31 entries; "+
			"got top results: %v", func() []string {
			ss := make([]string, len(results))
			for i, r := range results {
				ss[i] = fmt.Sprintf("[%d] score=%.4f %q", i, r.Score, r.Content[:min(60, len(r.Content))])
			}
			return ss
		}())
}

// -------------------------------------------------------------------------
// Test 5: harden existing design intent — verify the RIGHT entry is retrieved.
// -------------------------------------------------------------------------

// TestMemory_RememberKeyword_RetrievesCorrectEntry hardens the design intent
// test for the "remember" keyword by asserting that the *correct* entry is
// the top result, not just that something was retrieved.
//
// This closes the blind spot created by fixedEmbedder: with identical vectors
// for every entry, `require.NotEmpty` passes even if retrieval returns the
// wrong entry. With BM25 active, "sister name" should prefer the entry that
// actually contains "sister" — verified here explicitly.
func TestMemory_RememberKeyword_RetrievesCorrectEntry(t *testing.T) {
	const dims = 64

	s := openTestDB(t)
	db := s.DB()
	ctx := context.Background()
	emb := newRankedEmbedder(dims)
	sem, err := semantic.NewStore(db, emb)
	require.NoError(t, err)

	// Store multiple entries so retrieval is non-trivial.
	distractors := []string{
		"the office meeting is on wednesday at ten",
		"grocery run this weekend: need coffee and oat milk",
		"doctor appointment rescheduled to next month",
	}
	for _, d := range distractors {
		storeEntry(t, sem, ctx, d, "distractor")
	}

	// The target: explicitly reinforced with "remember".
	target := "Please remember that my sister's name is Harriet and she lives in Wellington."
	storeEntry(t, sem, ctx, target, "conversation")

	// Query that matches only the target vocabulary.
	results, err := sem.QueryText(ctx, "sister name family", 5, "")
	require.NoError(t, err)
	require.NotEmpty(t, results, "semantic store must return results for 'sister name family'")

	// The first result must be the planted entry, not a distractor.
	assert.Contains(t, results[0].Content, "Harriet",
		"top result must be the planted sister entry, not a distractor; "+
			"got top result: %q", results[0].Content)
}

// min is a local helper for Go < 1.21 compatibility.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
