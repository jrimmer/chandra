package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// mockExecutorResumer tracks Resume calls.
type mockExecutorResumer struct {
	resumed []resumeCall
}

type resumeCall struct {
	PlanID   string
	Approved bool
}

func (m *mockExecutorResumer) Resume(ctx context.Context, planID string, approved bool) error {
	m.resumed = append(m.resumed, resumeCall{PlanID: planID, Approved: approved})
	return nil
}

func TestPlanConfirmedHandler_Approved(t *testing.T) {
	exec := &mockExecutorResumer{}
	handler := NewPlanConfirmedHandler(exec)

	payload, _ := json.Marshal(PlanConfirmedEvent{
		PlanID:    "plan-1",
		StepIndex: 1,
		Approved:  true,
		UserID:    "user1",
	})

	ev := Event{
		Topic:     EventPlanConfirmed,
		Payload:   payload,
		Source:    "internal",
		Timestamp: time.Now(),
	}

	err := handler(context.Background(), ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(exec.resumed) != 1 {
		t.Fatalf("expected 1 resume call, got %d", len(exec.resumed))
	}
	if exec.resumed[0].PlanID != "plan-1" {
		t.Errorf("expected plan-1, got %q", exec.resumed[0].PlanID)
	}
	if !exec.resumed[0].Approved {
		t.Error("expected approved=true")
	}
}

func TestPlanConfirmedHandler_Rejected(t *testing.T) {
	exec := &mockExecutorResumer{}
	handler := NewPlanConfirmedHandler(exec)

	payload, _ := json.Marshal(PlanConfirmedEvent{
		PlanID:    "plan-2",
		StepIndex: 0,
		Approved:  false,
		UserID:    "user1",
	})

	ev := Event{
		Topic:     EventPlanConfirmed,
		Payload:   payload,
		Source:    "internal",
		Timestamp: time.Now(),
	}

	err := handler(context.Background(), ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(exec.resumed) != 1 {
		t.Fatalf("expected 1 resume call, got %d", len(exec.resumed))
	}
	if exec.resumed[0].Approved {
		t.Error("expected approved=false")
	}
}

func TestPlanConfirmedHandler_BadPayload(t *testing.T) {
	exec := &mockExecutorResumer{}
	handler := NewPlanConfirmedHandler(exec)

	ev := Event{
		Topic:     EventPlanConfirmed,
		Payload:   []byte(`{invalid`),
		Source:    "internal",
		Timestamp: time.Now(),
	}

	err := handler(context.Background(), ev)
	if err == nil {
		t.Error("expected error for bad payload")
	}
	if len(exec.resumed) != 0 {
		t.Error("should not resume on bad payload")
	}
}

func TestWirePlanConfirmed(t *testing.T) {
	bus := NewEventBus(10, 1, nil)
	bus.Start(context.Background())
	defer bus.Stop()

	exec := &mockExecutorResumer{}
	unsub := WirePlanConfirmed(bus, exec)
	defer unsub()

	payload, _ := json.Marshal(PlanConfirmedEvent{
		PlanID:   "plan-wire",
		Approved: true,
	})

	bus.Publish(context.Background(), Event{
		Topic:   EventPlanConfirmed,
		Payload: payload,
		Source:  "internal",
	})

	// Give the worker time to deliver
	time.Sleep(50 * time.Millisecond)

	if len(exec.resumed) != 1 {
		t.Errorf("expected 1 resume call via wiring, got %d", len(exec.resumed))
	}
}
