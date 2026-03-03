package doctor_test

import (
	"context"
	"testing"

	"github.com/jrimmer/chandra/internal/doctor"
)

type mockCheck struct {
	name   string
	result doctor.Result
}

func (m *mockCheck) Name() string                              { return m.name }
func (m *mockCheck) Run(ctx context.Context) doctor.Result    { return m.result }

func TestRunAll_CollectsResults(t *testing.T) {
	checks := []doctor.Check{
		&mockCheck{name: "config", result: doctor.Result{Status: doctor.Pass, Detail: "ok"}},
		&mockCheck{name: "db", result: doctor.Result{Status: doctor.Fail, Detail: "not found", Fix: "run migrations"}},
	}

	results := doctor.RunAll(context.Background(), checks, 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	found := map[string]doctor.Status{}
	for _, r := range results {
		found[r.Name] = r.Status
	}
	if found["config"] != doctor.Pass {
		t.Errorf("expected config=Pass, got %v", found["config"])
	}
	if found["db"] != doctor.Fail {
		t.Errorf("expected db=Fail, got %v", found["db"])
	}
}
