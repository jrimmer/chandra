-- Migration 006: add delivery target columns to intents.
-- channel_id + user_id tell the scheduler where to deliver the fired turn.
-- Default empty string for existing rows (they have no delivery target).
ALTER TABLE intents ADD COLUMN channel_id TEXT NOT NULL DEFAULT '';
ALTER TABLE intents ADD COLUMN user_id    TEXT NOT NULL DEFAULT '';
