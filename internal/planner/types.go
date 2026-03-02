package planner

import (
	"encoding/json"
	"time"
)

// Plan status constants.
const (
	PlanPlanning               = "planning"
	PlanExecuting              = "executing"
	PlanPaused                 = "paused"
	PlanCompleted              = "completed"
	PlanFailed                 = "failed"
	PlanRolledBack             = "rolled_back"
	PlanTimeout                = "timeout"
	PlanTimeoutPartial         = "timeout_partial_rollback"
	PlanRolledBackPartial      = "rolled_back_partial"
	PlanFailedAwaitingDecision = "failed_awaiting_decision"
)

// Step status constants.
const (
	StepPending              = "pending"
	StepRunning              = "running"
	StepCompleted            = "completed"
	StepFailed               = "failed"
	StepRolledBack           = "rolled_back"
	StepRollbackFailed       = "rollback_failed"
	StepAwaitingConfirmation = "awaiting_confirmation"
	StepSkipped              = "skipped"
)

// Checkpoint reason constants describe why a step requires confirmation.
const (
	CheckpointInstallSoftware = "install_software"
	CheckpointCreateInfra     = "create_infrastructure"
	CheckpointModifyData      = "modify_data"
	CheckpointExternalAction  = "external_action"
	CheckpointDestructive     = "destructive"
	CheckpointCostImplication = "cost_implication"
)

// StepIdempotency indicates whether a step can safely be re-executed.
type StepIdempotency int

const (
	IdempotentTrue    StepIdempotency = iota // Safe to re-run
	IdempotentFalse                          // Not safe to re-run
	IdempotentUnknown                        // Treated as false for safety
)

// OutputMode controls how step output is persisted.
type OutputMode int

const (
	OutputAuto       OutputMode = iota // Infer from content
	OutputPersistent                   // Always persist to SQLite
	OutputEphemeral                    // In-memory only, lost on restart
)

// ExecutionPlan represents a decomposed goal with ordered steps.
type ExecutionPlan struct {
	ID             string
	Goal           string
	Steps          []ExecutionStep
	State          map[string]any
	Status         string
	CurrentStep    int
	CheckpointStep int
	Checkpoints    []int // Pre-computed step indices requiring confirmation
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    time.Time
	Error          string
}

// ExecutionStep is a single action within an execution plan.
type ExecutionStep struct {
	ID                 string
	PlanID             string
	StepIndex          int
	Description        string
	SkillName          string
	Action             string
	Parameters         map[string]any
	DependsOn          []string
	Creates            []string
	Rollback           *RollbackAction
	Checkpoint         bool
	CheckpointReason   string
	CheckpointTimeout  time.Duration
	ExpectedDuration   time.Duration
	HeartbeatTimeout   time.Duration // How long without heartbeat = dead (default: 2x ExpectedDuration)
	Heartbeat          time.Time     // Last heartbeat timestamp, updated periodically during execution
	Idempotency        StepIdempotency
	OutputMode         OutputMode
	DependsOnEphemeral bool            // Force re-execution on restart if ephemeral deps lost
	Status             string
	Output             json.RawMessage
	StepOutput         *StepOutput
	Error              string
	StartedAt          time.Time
	CompletedAt        time.Time
}

// RollbackAction describes how to undo a step.
type RollbackAction struct {
	Description string
	SkillName   string
	Action      string
	Parameters  map[string]any
}

// StepOutput separates persistent and ephemeral step output.
type StepOutput struct {
	Persistent       map[string]any // Persisted to SQLite, survives restart
	Ephemeral        map[string]any // In-memory only, lost on restart
	CreatedResources []ResourceRef
	PreviousState    json.RawMessage
	Idempotent       bool
}

// ResourceRef identifies a resource created by a step for rollback tracking.
type ResourceRef struct {
	Type string // "container", "vm", "file", "service"
	ID   string
	Host string
}

// ExecutionResult summarizes the outcome of a plan execution run.
type ExecutionResult struct {
	PlanID     string
	Success    bool
	StepsRun   int
	FailedAt   int
	Error      error
	Outputs    map[string]any
	RolledBack bool
}

// ExecutionStatus is a snapshot of a plan's current state.
type ExecutionStatus struct {
	PlanID         string
	State          string
	CurrentStep    int
	CheckpointStep int
	Outputs        map[string]any
}

// Gap describes a missing capability needed for plan execution.
type Gap struct {
	Type       string // "skill", "infrastructure", "credential"
	Name       string
	Required   bool
	Resolution string // "create", "install", "configure"
}

// CheckpointConfig controls checkpoint timeout behavior.
type CheckpointConfig struct {
	DefaultTimeout time.Duration // 24h default
	MaxTimeout     time.Duration // 7 days max
	Extendable     bool          // Can user extend via "chandra plan extend <id>"?
}
