-- Migration 011: Add worker_id to token_usage for itemized worker cost tracking.
-- worker_id is NULL for parent conversation turns, set for worker turns.
-- This enables per-worker subtotals and grand total rollup in get_usage_stats.
ALTER TABLE token_usage ADD COLUMN worker_id TEXT;
CREATE INDEX IF NOT EXISTS idx_token_usage_worker ON token_usage(worker_id) WHERE worker_id IS NOT NULL;
