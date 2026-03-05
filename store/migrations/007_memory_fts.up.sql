-- Migration 007: Add FTS5 full-text search index for hybrid BM25+vector retrieval.
--
-- memory_fts is a non-content FTS5 table (stores its own copy of the text so it
-- stays consistent even if memory_entries is modified directly). The id column is
-- UNINDEXED so we can join back to memory_entries without tokenising it.
--
-- Tokenizer: unicode61 with diacritic removal gives good multilingual recall.
-- BM25 scoring is built into FTS5 via the bm25() auxiliary function.

CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
    id       UNINDEXED,
    content,
    tokenize = 'unicode61 remove_diacritics 1'
);

-- Back-fill any existing rows so existing deployments get immediate benefit.
INSERT INTO memory_fts (id, content)
SELECT id, content FROM memory_entries
WHERE id NOT IN (SELECT id FROM memory_fts);
