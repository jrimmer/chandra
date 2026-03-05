-- Add recurrence support to intents.
-- recurrence_interval_ms = 0 means one-shot (original behaviour).
-- recurrence_interval_ms > 0 means "reschedule next_check += interval after each fire".
ALTER TABLE intents ADD COLUMN recurrence_interval_ms INTEGER NOT NULL DEFAULT 0;
