-- Add approval_message_id to track which DM message was sent to the owner
-- for reaction-based approve/deny of access requests.
ALTER TABLE access_requests ADD COLUMN approval_message_id TEXT;
CREATE INDEX IF NOT EXISTS idx_access_requests_approval_msg ON access_requests(approval_message_id);
