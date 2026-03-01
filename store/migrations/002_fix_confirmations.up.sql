-- Drop the old confirmations table (it had a mandatory session_id FK and a
-- different schema from what the confirm package needs). Replace it with
-- a schema that matches the confirm.Store implementation.
DROP TABLE IF EXISTS confirmations;

CREATE TABLE IF NOT EXISTS confirmations (
    id         TEXT PRIMARY KEY,
    tool_call  TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_confirmations_status
    ON confirmations(status, expires_at);
