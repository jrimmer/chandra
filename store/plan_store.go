package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// PlanRecord represents a row in the execution_plans table.
type PlanRecord struct {
	ID             string
	Goal           string
	Status         string
	CurrentStep    int
	CheckpointStep *int
	State          json.RawMessage
	CreatedAt      int64
	UpdatedAt      int64
	CompletedAt    *int64
	Error          *string
}

// StepRecord represents a row in the execution_steps table.
type StepRecord struct {
	ID             string
	PlanID         string
	StepIndex      int
	Description    string
	SkillName      *string
	Action         string
	Parameters     json.RawMessage
	DependsOn      json.RawMessage
	Creates        json.RawMessage
	RollbackAction json.RawMessage
	Status         string
	Output         json.RawMessage
	StartedAt      *int64
	CompletedAt    *int64
	Error          *string
	Heartbeat      *int64
}

// ApprovedCommandRecord represents a row in the approved_commands table.
type ApprovedCommandRecord struct {
	ID              string
	SkillName       string
	CommandTemplate string
	ApprovedBy      string
	ApprovedAt      int64
	LastUsed        *int64
}

// NotificationRecord represents a row in the pending_notifications table.
type NotificationRecord struct {
	ID          string
	UserID      string
	Message     string
	SourceType  string
	SourceID    *string
	CreatedAt   int64
	ExpiresAt   int64
	DeliveredAt *int64
}

// scanPlan scans a plan row using nullable intermediaries.
func scanPlan(scanner interface{ Scan(...any) error }) (*PlanRecord, error) {
	p := &PlanRecord{}
	var state, errStr sql.NullString
	err := scanner.Scan(&p.ID, &p.Goal, &p.Status, &p.CurrentStep, &p.CheckpointStep,
		&state, &p.CreatedAt, &p.UpdatedAt, &p.CompletedAt, &errStr)
	if err != nil {
		return nil, err
	}
	if state.Valid {
		p.State = json.RawMessage(state.String)
	}
	if errStr.Valid {
		p.Error = &errStr.String
	}
	return p, nil
}

// scanStep scans a step row using nullable intermediaries.
func scanStep(scanner interface{ Scan(...any) error }) (*StepRecord, error) {
	st := &StepRecord{}
	var params, depsOn, creates, rollback, output sql.NullString
	err := scanner.Scan(&st.ID, &st.PlanID, &st.StepIndex, &st.Description, &st.SkillName,
		&st.Action, &params, &depsOn, &creates, &rollback,
		&st.Status, &output, &st.StartedAt, &st.CompletedAt, &st.Error, &st.Heartbeat)
	if err != nil {
		return nil, err
	}
	if params.Valid {
		st.Parameters = json.RawMessage(params.String)
	}
	if depsOn.Valid {
		st.DependsOn = json.RawMessage(depsOn.String)
	}
	if creates.Valid {
		st.Creates = json.RawMessage(creates.String)
	}
	if rollback.Valid {
		st.RollbackAction = json.RawMessage(rollback.String)
	}
	if output.Valid {
		st.Output = json.RawMessage(output.String)
	}
	return st, nil
}

const planColumns = `id, goal, status, current_step, checkpoint_step, state, created_at, updated_at, completed_at, error`
const stepColumns = `id, plan_id, step_index, description, skill_name, action, parameters, depends_on, creates, rollback_action, status, output, started_at, completed_at, error, heartbeat`

// CreatePlan inserts a new execution plan.
func (s *Store) CreatePlan(ctx context.Context, p *PlanRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO execution_plans (`+planColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Goal, p.Status, p.CurrentStep, p.CheckpointStep, p.State,
		p.CreatedAt, p.UpdatedAt, p.CompletedAt, p.Error,
	)
	if err != nil {
		return fmt.Errorf("create plan: %w", err)
	}
	return nil
}

// GetPlan retrieves a plan by ID.
func (s *Store) GetPlan(ctx context.Context, id string) (*PlanRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+planColumns+` FROM execution_plans WHERE id = ?`, id)
	p, err := scanPlan(row)
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}
	return p, nil
}

// UpdatePlanStatus updates a plan's status and current step.
func (s *Store) UpdatePlanStatus(ctx context.Context, id, status string, currentStep int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE execution_plans SET status = ?, current_step = ?, updated_at = ? WHERE id = ?`,
		status, currentStep, time.Now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("update plan status: %w", err)
	}
	return nil
}

// CompletePlan marks a plan as completed.
func (s *Store) CompletePlan(ctx context.Context, id, status string, planErr *string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`UPDATE execution_plans SET status = ?, completed_at = ?, updated_at = ?, error = ? WHERE id = ?`,
		status, now, now, planErr, id,
	)
	if err != nil {
		return fmt.Errorf("complete plan: %w", err)
	}
	return nil
}

// ListPlansByStatus returns all plans with the given status.
func (s *Store) ListPlansByStatus(ctx context.Context, status string) ([]PlanRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+planColumns+` FROM execution_plans WHERE status = ? ORDER BY created_at DESC`, status)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()

	var plans []PlanRecord
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan plan: %w", err)
		}
		plans = append(plans, *p)
	}
	return plans, rows.Err()
}

// CreateStep inserts a new execution step.
func (s *Store) CreateStep(ctx context.Context, step *StepRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO execution_steps (`+stepColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		step.ID, step.PlanID, step.StepIndex, step.Description, step.SkillName,
		step.Action, step.Parameters, step.DependsOn, step.Creates, step.RollbackAction,
		step.Status, step.Output, step.StartedAt, step.CompletedAt, step.Error, step.Heartbeat,
	)
	if err != nil {
		return fmt.Errorf("create step: %w", err)
	}
	return nil
}

// ListSteps returns all steps for a plan, ordered by step_index.
func (s *Store) ListSteps(ctx context.Context, planID string) ([]StepRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+stepColumns+` FROM execution_steps WHERE plan_id = ? ORDER BY step_index`, planID)
	if err != nil {
		return nil, fmt.Errorf("list steps: %w", err)
	}
	defer rows.Close()

	var steps []StepRecord
	for rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return nil, fmt.Errorf("scan step: %w", err)
		}
		steps = append(steps, *st)
	}
	return steps, rows.Err()
}

// GetStep retrieves a single step by ID.
func (s *Store) GetStep(ctx context.Context, stepID string) (*StepRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+stepColumns+` FROM execution_steps WHERE id = ?`, stepID)
	st, err := scanStep(row)
	if err != nil {
		return nil, fmt.Errorf("get step: %w", err)
	}
	return st, nil
}

// UpdateStepStatus updates a step's status and optional output/error/completion time.
func (s *Store) UpdateStepStatus(ctx context.Context, stepID, status string, output *json.RawMessage, stepErr *string, completedAt *int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE execution_steps SET status = ?, output = COALESCE(?, output), error = COALESCE(?, error), completed_at = COALESCE(?, completed_at)
		 WHERE id = ?`,
		status, output, stepErr, completedAt, stepID,
	)
	if err != nil {
		return fmt.Errorf("update step status: %w", err)
	}
	return nil
}

// BatchUpdateHeartbeats writes multiple heartbeat timestamps in a single transaction.
// The map key is the step ID, value is the heartbeat time.
// Implements the executor.StepStore interface.
func (s *Store) BatchUpdateHeartbeats(updates map[string]time.Time) error {
	if len(updates) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin heartbeat tx: %w", err)
	}

	stmt, err := tx.Prepare("UPDATE execution_steps SET heartbeat = ? WHERE id = ?")
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare heartbeat stmt: %w", err)
	}
	defer stmt.Close()

	for id, ts := range updates {
		if _, err := stmt.Exec(ts.Unix(), id); err != nil {
			tx.Rollback()
			return fmt.Errorf("update heartbeat for %s: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit heartbeat tx: %w", err)
	}
	return nil
}

// CreateApprovedCommand inserts a new approved command template.
func (s *Store) CreateApprovedCommand(ctx context.Context, cmd *ApprovedCommandRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO approved_commands (id, skill_name, command_template, approved_by, approved_at, last_used)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		cmd.ID, cmd.SkillName, cmd.CommandTemplate, cmd.ApprovedBy, cmd.ApprovedAt, cmd.LastUsed,
	)
	if err != nil {
		return fmt.Errorf("create approved command: %w", err)
	}
	return nil
}

// ListApprovedCommands returns approved commands for a skill.
func (s *Store) ListApprovedCommands(ctx context.Context, skillName string) ([]ApprovedCommandRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, skill_name, command_template, approved_by, approved_at, last_used
		 FROM approved_commands WHERE skill_name = ?`, skillName,
	)
	if err != nil {
		return nil, fmt.Errorf("list approved commands: %w", err)
	}
	defer rows.Close()

	var cmds []ApprovedCommandRecord
	for rows.Next() {
		var c ApprovedCommandRecord
		if err := rows.Scan(&c.ID, &c.SkillName, &c.CommandTemplate, &c.ApprovedBy, &c.ApprovedAt, &c.LastUsed); err != nil {
			return nil, fmt.Errorf("scan approved command: %w", err)
		}
		cmds = append(cmds, c)
	}
	return cmds, rows.Err()
}

// HasApprovedCommandTemplate checks if a command template is approved for a skill.
func (s *Store) HasApprovedCommandTemplate(ctx context.Context, skillName, template string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM approved_commands WHERE skill_name = ? AND command_template = ?`,
		skillName, template,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check approved command: %w", err)
	}
	return count > 0, nil
}

// CreateNotification inserts a new pending notification.
func (s *Store) CreateNotification(ctx context.Context, n *NotificationRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pending_notifications (id, user_id, message, source_type, source_id, created_at, expires_at, delivered_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.UserID, n.Message, n.SourceType, n.SourceID, n.CreatedAt, n.ExpiresAt, n.DeliveredAt,
	)
	if err != nil {
		return fmt.Errorf("create notification: %w", err)
	}
	return nil
}

// ListPendingNotifications returns undelivered notifications for a user.
func (s *Store) ListPendingNotifications(ctx context.Context, userID string) ([]NotificationRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, message, source_type, source_id, created_at, expires_at, delivered_at
		 FROM pending_notifications WHERE user_id = ? AND delivered_at IS NULL AND expires_at > ?
		 ORDER BY created_at`, userID, time.Now().Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("list pending notifications: %w", err)
	}
	defer rows.Close()

	var notifs []NotificationRecord
	for rows.Next() {
		var n NotificationRecord
		if err := rows.Scan(&n.ID, &n.UserID, &n.Message, &n.SourceType, &n.SourceID,
			&n.CreatedAt, &n.ExpiresAt, &n.DeliveredAt); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		notifs = append(notifs, n)
	}
	return notifs, rows.Err()
}

// MarkNotificationDelivered marks a notification as delivered.
func (s *Store) MarkNotificationDelivered(ctx context.Context, id string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`UPDATE pending_notifications SET delivered_at = ? WHERE id = ?`, now, id,
	)
	if err != nil {
		return fmt.Errorf("mark notification delivered: %w", err)
	}
	return nil
}

// CleanupNotifications removes delivered notifications older than the retention duration.
// Returns the number of deleted rows.
func (s *Store) CleanupNotifications(ctx context.Context, retention time.Duration) (int, error) {
	cutoff := time.Now().Add(-retention).Unix()
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM pending_notifications WHERE delivered_at IS NOT NULL AND created_at < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("cleanup notifications: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// UpdatePlanState updates the plan's state JSON blob.
func (s *Store) UpdatePlanState(ctx context.Context, id string, state json.RawMessage) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE execution_plans SET state = ?, updated_at = ? WHERE id = ?`,
		state, time.Now().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("update plan state: %w", err)
	}
	return nil
}

// DeletePlan removes a plan and its steps.
func (s *Store) DeletePlan(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete plan tx: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM execution_steps WHERE plan_id = ?", id); err != nil {
		tx.Rollback()
		return fmt.Errorf("delete steps: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM execution_plans WHERE id = ?", id); err != nil {
		tx.Rollback()
		return fmt.Errorf("delete plan: %w", err)
	}
	return tx.Commit()
}

// GetStaleRunningSteps returns running steps with heartbeats older than the cutoff.
func (s *Store) GetStaleRunningSteps(ctx context.Context, cutoff time.Time) ([]StepRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+stepColumns+` FROM execution_steps
		 WHERE status = 'running' AND heartbeat IS NOT NULL AND heartbeat < ?
		 ORDER BY plan_id, step_index`, cutoff.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("get stale steps: %w", err)
	}
	defer rows.Close()

	var steps []StepRecord
	for rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return nil, fmt.Errorf("scan stale step: %w", err)
		}
		steps = append(steps, *st)
	}
	return steps, rows.Err()
}

// Ensure Store implements the BatchUpdateHeartbeats interface at compile time.
var _ interface {
	BatchUpdateHeartbeats(map[string]time.Time) error
} = (*Store)(nil)
