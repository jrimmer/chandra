package pkg

import "time"

// Episode is a single turn in conversation history.
type Episode struct {
	ID        string
	SessionID string
	Role      string    // "user" | "assistant" | "tool"
	Content   string
	Timestamp time.Time
	Tags      []string
}

// MemoryEntry is a semantic memory item with embedding.
type MemoryEntry struct {
	ID         string
	UserID     string    // user who generated this memory; "" = unscoped (legacy/admin)
	Content    string
	Embedding  []float32
	Source     string    // "conversation" | "event" | "observation"
	Timestamp  time.Time
	Importance float32   // 0.0–1.0, set at insert time via heuristics
	Score      float32   // populated on retrieval (combined ranking), 0 on storage
}
