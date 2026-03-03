CREATE TABLE IF NOT EXISTS channel_verifications (
    channel_id      TEXT NOT NULL,
    verified_at     INTEGER NOT NULL,  -- Unix timestamp (seconds)
    verified_user_id TEXT NOT NULL,
    PRIMARY KEY (channel_id)
);
