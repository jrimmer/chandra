-- SQLite does not support DROP COLUMN before 3.35.0.
-- Recreate the table without the new columns.
CREATE TABLE intents_old AS SELECT id, description, condition, action, status, created_at, last_checked, next_check FROM intents;
DROP TABLE intents;
ALTER TABLE intents_old RENAME TO intents;
