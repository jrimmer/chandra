-- SQLite does not support DROP COLUMN; this migration cannot be rolled back cleanly.
DROP INDEX IF EXISTS idx_token_usage_worker;
