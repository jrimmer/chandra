-- SQLite doesn't support DROP COLUMN before 3.35.0; recreate the table.
DROP INDEX IF EXISTS idx_access_requests_approval_msg;
CREATE TABLE access_requests_backup AS SELECT id, channel_id, user_id, username, first_message, status, created_at, decided_at FROM access_requests;
DROP TABLE access_requests;
ALTER TABLE access_requests_backup RENAME TO access_requests;
