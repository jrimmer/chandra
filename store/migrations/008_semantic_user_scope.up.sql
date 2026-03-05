-- Scope semantic memories to the user who generated them.
-- Existing rows get user_id = '' (unscoped/legacy); they will still match
-- queries with userID="" (admin/no-filter mode) but not per-user queries.
ALTER TABLE memory_entries ADD COLUMN user_id TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_memory_user ON memory_entries(user_id, timestamp DESC);
