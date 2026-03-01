-- Episodic memory
CREATE TABLE episodes (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    role        TEXT NOT NULL,
    content     TEXT NOT NULL,
    timestamp   INTEGER NOT NULL,
    tags        TEXT
);
CREATE INDEX idx_episodes_session ON episodes(session_id, timestamp DESC);
CREATE INDEX idx_episodes_timestamp ON episodes(timestamp DESC);

-- Semantic memory
CREATE TABLE memory_entries (
    id          TEXT PRIMARY KEY,
    content     TEXT NOT NULL,
    source      TEXT NOT NULL,
    timestamp   INTEGER NOT NULL,
    importance  REAL DEFAULT 0.5
);
CREATE INDEX idx_memory_timestamp ON memory_entries(timestamp DESC);

-- sqlite-vec virtual table (1536 dims = text-embedding-3-small default)
CREATE VIRTUAL TABLE memory_embeddings USING vec0(
    id          TEXT PRIMARY KEY,
    embedding   FLOAT[1536]
);

-- Intent store
CREATE TABLE intents (
    id           TEXT PRIMARY KEY,
    description  TEXT NOT NULL,
    condition    TEXT NOT NULL,
    action       TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active',
    created_at   INTEGER NOT NULL,
    last_checked INTEGER,
    next_check   INTEGER
);
CREATE INDEX idx_intents_status ON intents(status);
CREATE INDEX idx_intents_next_check ON intents(next_check);

-- Agent identity
CREATE TABLE agent_profile (
    id           TEXT PRIMARY KEY DEFAULT 'chandra',
    name         TEXT NOT NULL,
    persona      TEXT NOT NULL,
    traits       TEXT NOT NULL,
    capabilities TEXT NOT NULL
);

-- User profile
CREATE TABLE user_profile (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    timezone     TEXT NOT NULL,
    preferences  TEXT,
    notes        TEXT
);

-- Relationship state
CREATE TABLE relationship_state (
    agent_id           TEXT NOT NULL,
    user_id            TEXT NOT NULL,
    trust_level        INTEGER NOT NULL DEFAULT 3,
    communication_style TEXT NOT NULL DEFAULT 'concise',
    ongoing_context    TEXT,
    last_interaction   INTEGER,
    PRIMARY KEY (agent_id, user_id)
);

-- Tool telemetry
CREATE TABLE tool_telemetry (
    id           TEXT PRIMARY KEY,
    tool_name    TEXT NOT NULL,
    called_at    INTEGER NOT NULL,
    latency_ms   INTEGER NOT NULL,
    success      INTEGER NOT NULL,
    error        TEXT,
    retries      INTEGER DEFAULT 0
);
CREATE INDEX idx_telemetry_tool ON tool_telemetry(tool_name, called_at DESC);

-- Sessions
CREATE TABLE sessions (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    channel_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    started_at      INTEGER NOT NULL,
    last_active     INTEGER NOT NULL,
    meta            TEXT
);
CREATE INDEX idx_sessions_conversation ON sessions(conversation_id, last_active DESC);
CREATE INDEX idx_sessions_channel ON sessions(channel_id, last_active DESC);

-- Action log
CREATE TABLE action_log (
    id           TEXT PRIMARY KEY,
    timestamp    INTEGER NOT NULL,
    type         TEXT NOT NULL,
    summary      TEXT NOT NULL,
    details      TEXT,
    session_id   TEXT,
    tool_name    TEXT,
    success      INTEGER
);
CREATE INDEX idx_action_log_time ON action_log(timestamp DESC);
CREATE INDEX idx_action_log_type ON action_log(type, timestamp DESC);

-- Action rollups
CREATE TABLE action_rollups (
    id           TEXT PRIMARY KEY,
    period       TEXT NOT NULL,
    start_time   INTEGER NOT NULL,
    end_time     INTEGER NOT NULL,
    summary      TEXT NOT NULL,
    action_count INTEGER NOT NULL,
    error_count  INTEGER NOT NULL,
    top_tools    TEXT
);
CREATE INDEX idx_rollups_period ON action_rollups(period, start_time DESC);

-- Confirmation queue
CREATE TABLE confirmations (
    id           TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL,
    tool_name    TEXT NOT NULL,
    parameters   TEXT NOT NULL,
    description  TEXT NOT NULL,
    requested_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    status       TEXT DEFAULT 'pending'
);
CREATE INDEX idx_confirmations_session ON confirmations(session_id, status);
