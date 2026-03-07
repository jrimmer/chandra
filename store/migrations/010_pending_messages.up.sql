CREATE TABLE IF NOT EXISTS pending_messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

CREATE TABLE IF NOT EXISTS config_confirmations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    key        TEXT    NOT NULL,
    old_value  TEXT    NOT NULL,
    new_value  TEXT    NOT NULL,
    channel_id TEXT    NOT NULL,
    user_id    TEXT    NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

CREATE TABLE IF NOT EXISTS token_usage (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    conv_id           TEXT    NOT NULL,
    user_id           TEXT    NOT NULL,
    channel_id        TEXT    NOT NULL,
    model             TEXT    NOT NULL,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    created_at        INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_token_usage_conv    ON token_usage(conv_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_created ON token_usage(created_at);
CREATE INDEX IF NOT EXISTS idx_token_usage_channel ON token_usage(channel_id, created_at DESC);
