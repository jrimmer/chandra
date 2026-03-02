package events

// EventType classifies event topics for the internal event bus.
type EventType = string

const (
	EventPlanConfirmed EventType = "plan_confirmed"
	EventPlanTimeout   EventType = "plan_timeout"
	EventSkillApproved EventType = "skill_approved"
)

// PlanConfirmedEvent carries details when a plan checkpoint is approved or rejected.
type PlanConfirmedEvent struct {
	PlanID    string
	StepIndex int
	Approved  bool
	UserID    string
}
