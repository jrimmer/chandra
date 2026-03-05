package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/budget"
	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/internal/memory/semantic"
	"github.com/jrimmer/chandra/pkg"
)

// TestIntegration_CBM_Adversarial seeds 1000 semantic memories and 50 active
// intents, then verifies that budget.Assemble produces a result that fits
// within the token limit without panicking.
func TestIntegration_CBM_Adversarial(t *testing.T) {
	ctx := context.Background()

	// 1. Real SQLite DB with migrations.
	s := openTestDB(t)
	db := s.DB()

	// 2. Seed 1000 semantic memories using StoreBatch with fixed embeddings.
	embedder := &fixedEmbedder{}
	semStore, err := semantic.NewStore(db, embedder)
	if err != nil {
		t.Fatalf("semantic.NewStore: %v", err)
	}

	// Build batches of 100 entries each (10 batches = 1000 total).
	const totalMemories = 1000
	const batchSize = 100
	for batch := 0; batch < totalMemories/batchSize; batch++ {
		entries := make([]pkg.MemoryEntry, batchSize)
		for i := range entries {
			entries[i] = pkg.MemoryEntry{
				Content:    fmt.Sprintf("Memory entry %d from batch %d: this is a semantically stored fact about the world.", batch*batchSize+i, batch),
				Source:     "adversarial-test",
				Timestamp:  time.Now().UTC(),
				Importance: 0.5,
			}
		}
		if err := semStore.StoreBatch(ctx, entries); err != nil {
			t.Fatalf("semStore.StoreBatch (batch %d): %v", batch, err)
		}
	}

	// 3. Seed 50 active intents.
	intentStore := intent.NewStore(db)
	for i := 0; i < 50; i++ {
		if err := intentStore.Create(ctx, intent.Intent{
			Description: fmt.Sprintf("adversarial intent %d about monitoring system health", i),
			Condition:   "on_schedule",
			Action:      fmt.Sprintf("check metric %d", i),
		}); err != nil {
			t.Fatalf("intentStore.Create (intent %d): %v", i, err)
		}
	}

	// 4. Query all seeded memories to build ranked candidates.
	// Using totalMemories as the limit ensures the CBM receives more candidates
	// than fit in the budget, forcing it to drop entries — the adversarial scenario.
	memories, err := semStore.QueryText(ctx, "test query for adversarial scenario", totalMemories, "")
	if err != nil {
		t.Fatalf("semStore.QueryText: %v", err)
	}

	// Convert to budget candidates.
	ranked := make([]budget.ContextCandidate, len(memories))
	for i, m := range memories {
		wordCount := len(m.Content) / 5 // rough token estimate
		ranked[i] = budget.ContextCandidate{
			Role:       "memory",
			Content:    m.Content,
			Priority:   m.Score,
			Importance: m.Importance,
			Recency:    m.Timestamp,
			Tokens:     wordCount,
		}
	}

	// 5. Create ContextBudget with small token limit (4096 tokens).
	const tokenLimit = 4096
	budgetMgr := budget.New(0.5, 0.3, 0.2, 24, &budgetIntentAdapter{store: intentStore})

	// 6. Run Assemble — must not panic.
	window, err := budgetMgr.Assemble(ctx, tokenLimit, nil, ranked, nil, 0)
	if err != nil {
		t.Fatalf("budget.Assemble: %v", err)
	}

	// 7. Assert: output token count <= 4096.
	if window.TotalTokens > tokenLimit {
		t.Errorf("TotalTokens %d exceeds limit %d", window.TotalTokens, tokenLimit)
	}

	// 8. Assert: adversarial load caused CBM to drop at least one candidate.
	// With 1000 memories at ~20 tokens each (~20,000 total) and a 4096 token budget,
	// the CBM must drop the majority of candidates. Dropped == 0 would mean the
	// budget logic is not working.
	if window.Dropped == 0 {
		t.Errorf("CBM.Assemble dropped 0 candidates; expected drops under adversarial load (1000 memories, 4096 token limit)")
	}
	t.Logf("CBM adversarial result: TotalTokens=%d, Messages=%d, Dropped=%d",
		window.TotalTokens, len(window.Messages), window.Dropped)
}
