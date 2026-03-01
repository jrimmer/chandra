-- Restore the original confirmations table.
DROP TABLE IF EXISTS confirmations;

CREATE TABLE IF NOT EXISTS confirmations (
    id           TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL REFERENCES sessions(id),
    tool_name    TEXT NOT NULL,
    parameters   TEXT NOT NULL,
    description  TEXT NOT NULL,
    requested_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    status       TEXT DEFAULT 'pending'
);
CREATE INDEX IF NOT EXISTS idx_confirmations_session ON confirmations(session_id, status);
