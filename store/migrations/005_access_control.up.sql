-- Invite codes for the 'invite' access policy
CREATE TABLE IF NOT EXISTS invite_codes (
    code            TEXT PRIMARY KEY,
    uses_remaining  INTEGER NOT NULL DEFAULT 1,   -- -1 = unlimited
    expires_at      INTEGER,                       -- Unix timestamp; NULL = no expiry
    created_at      INTEGER NOT NULL,
    redeemed_by     TEXT                           -- user ID of first redemption (single-use)
);

-- Access requests for the 'request' access policy
CREATE TABLE IF NOT EXISTS access_requests (
    id              TEXT PRIMARY KEY,
    channel_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    username        TEXT NOT NULL,
    first_message   TEXT,
    status          TEXT NOT NULL DEFAULT 'pending', -- pending | approved | denied | blocked
    created_at      INTEGER NOT NULL,
    decided_at      INTEGER
);

-- Approved users (allowlist) with source tracking
CREATE TABLE IF NOT EXISTS allowed_users (
    channel_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    username        TEXT,
    source          TEXT NOT NULL DEFAULT 'manual', -- manual | hello_world | invite | request | role
    added_at        INTEGER NOT NULL,
    PRIMARY KEY (channel_id, user_id)
);
