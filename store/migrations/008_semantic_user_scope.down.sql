-- SQLite does not support DROP COLUMN on older versions; the index can be dropped.
-- On SQLite >= 3.35 (Ubuntu 24.04 ships 3.45): DROP COLUMN is supported.
DROP INDEX IF EXISTS idx_memory_user;
ALTER TABLE memory_entries DROP COLUMN user_id;
