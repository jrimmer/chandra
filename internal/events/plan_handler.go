package events

import (
	"context"
	"encoding/json"
	"fmt"
)

// ExecutorResumer is the interface for resuming a paused plan.
// This avoids importing the executor package directly.
type ExecutorResumer interface {
	Resume(ctx context.Context, planID string, approved bool) error
}

// NewPlanConfirmedHandler returns an event handler that resumes a paused plan
// when a checkpoint confirmation event is received.
func NewPlanConfirmedHandler(executor ExecutorResumer) Handler {
	return func(ctx context.Context, ev Event) error {
		var confirmed PlanConfirmedEvent
		if err := json.Unmarshal(ev.Payload, &confirmed); err != nil {
			return fmt.Errorf("plan_confirmed: unmarshal payload: %w", err)
		}

		return executor.Resume(ctx, confirmed.PlanID, confirmed.Approved)
	}
}

// WirePlanConfirmed subscribes the plan confirmation handler to the event bus.
// Returns an unsubscribe function.
func WirePlanConfirmed(bus EventBus, executor ExecutorResumer) func() {
	handler := NewPlanConfirmedHandler(executor)
	return bus.Subscribe(EventPlanConfirmed, handler)
}
