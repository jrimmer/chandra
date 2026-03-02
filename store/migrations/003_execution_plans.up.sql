-- Execution plans
CREATE TABLE IF NOT EXISTS execution_plans (
    id              TEXT PRIMARY KEY,
    goal            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'planning',
    current_step    INTEGER DEFAULT 0,
    checkpoint_step INTEGER,
    state           TEXT,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    completed_at    INTEGER,
    error           TEXT
);
CREATE INDEX IF NOT EXISTS idx_plans_status ON execution_plans(status);

-- Execution steps (includes heartbeat column for step recovery)
CREATE TABLE IF NOT EXISTS execution_steps (
    id              TEXT PRIMARY KEY,
    plan_id         TEXT NOT NULL REFERENCES execution_plans(id),
    step_index      INTEGER NOT NULL,
    description     TEXT NOT NULL,
    skill_name      TEXT,
    action          TEXT NOT NULL,
    parameters      TEXT,
    depends_on      TEXT,
    creates         TEXT,
    rollback_action TEXT,
    status          TEXT NOT NULL DEFAULT 'pending',
    output          TEXT,
    started_at      INTEGER,
    completed_at    INTEGER,
    error           TEXT,
    heartbeat       INTEGER
);
CREATE INDEX IF NOT EXISTS idx_steps_plan ON execution_steps(plan_id, step_index);

-- Approved command templates
CREATE TABLE IF NOT EXISTS approved_commands (
    id                TEXT PRIMARY KEY,
    skill_name        TEXT NOT NULL,
    command_template  TEXT NOT NULL,
    approved_by       TEXT NOT NULL,
    approved_at       INTEGER NOT NULL,
    last_used         INTEGER
);
CREATE INDEX IF NOT EXISTS idx_approved_skill ON approved_commands(skill_name);

-- Pending notifications
CREATE TABLE IF NOT EXISTS pending_notifications (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL,
    message         TEXT NOT NULL,
    source_type     TEXT NOT NULL,
    source_id       TEXT,
    created_at      INTEGER NOT NULL,
    expires_at      INTEGER NOT NULL,
    delivered_at    INTEGER
);
CREATE INDEX IF NOT EXISTS idx_pending_user ON pending_notifications(user_id, delivered_at);

-- Add plan correlation to confirmations
ALTER TABLE confirmations ADD COLUMN plan_id TEXT;
ALTER TABLE confirmations ADD COLUMN step_index INTEGER;
CREATE INDEX IF NOT EXISTS idx_confirmations_plan ON confirmations(plan_id) WHERE plan_id IS NOT NULL;
