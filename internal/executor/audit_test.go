package executor

import (
	"testing"
	"time"
)

func TestAuditEntry_Fields(t *testing.T) {
	entry := AuditEntry{
		PlanID:    "plan-1",
		StepIndex: 2,
		Command:   "ssh deploy@prod uptime",
		Host:      "prod",
		User:      "deploy",
		ExitCode:  0,
		StartedAt: time.Now(),
		Duration:  3 * time.Second,
	}

	if entry.PlanID != "plan-1" {
		t.Errorf("expected plan-1, got %q", entry.PlanID)
	}
	if entry.Host != "prod" {
		t.Errorf("expected host prod, got %q", entry.Host)
	}
	if entry.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", entry.ExitCode)
	}
}

func TestNewAuditTrail(t *testing.T) {
	trail := NewAuditTrail(100)
	if trail == nil {
		t.Fatal("expected non-nil trail")
	}
}

func TestAuditTrail_RecordAndQuery(t *testing.T) {
	trail := NewAuditTrail(10)

	trail.Record(AuditEntry{
		PlanID:    "plan-1",
		StepIndex: 0,
		Command:   "ssh deploy@prod uptime",
		Host:      "prod",
		User:      "deploy",
		ExitCode:  0,
		StartedAt: time.Now(),
		Duration:  1 * time.Second,
	})
	trail.Record(AuditEntry{
		PlanID:    "plan-1",
		StepIndex: 1,
		Command:   "ssh deploy@staging ls",
		Host:      "staging",
		User:      "deploy",
		ExitCode:  0,
		StartedAt: time.Now(),
		Duration:  500 * time.Millisecond,
	})
	trail.Record(AuditEntry{
		PlanID:    "plan-2",
		StepIndex: 0,
		Command:   "ssh root@prod reboot",
		Host:      "prod",
		User:      "root",
		ExitCode:  1,
		StartedAt: time.Now(),
		Duration:  2 * time.Second,
	})

	all := trail.QueryByPlan("plan-1")
	if len(all) != 2 {
		t.Errorf("expected 2 entries for plan-1, got %d", len(all))
	}

	byHost := trail.QueryByHost("prod")
	if len(byHost) != 2 {
		t.Errorf("expected 2 entries for host prod, got %d", len(byHost))
	}

	allEntries := trail.All()
	if len(allEntries) != 3 {
		t.Errorf("expected 3 total entries, got %d", len(allEntries))
	}
}

func TestAuditTrail_MaxEntries(t *testing.T) {
	trail := NewAuditTrail(2)

	for i := 0; i < 5; i++ {
		trail.Record(AuditEntry{
			PlanID:    "plan-1",
			StepIndex: i,
			Command:   "ssh test@host cmd",
			Host:      "host",
			StartedAt: time.Now(),
		})
	}

	all := trail.All()
	if len(all) != 2 {
		t.Errorf("expected max 2 entries, got %d", len(all))
	}
}

func TestExtractSSHTarget(t *testing.T) {
	tests := []struct {
		command  string
		wantUser string
		wantHost string
	}{
		{"ssh deploy@prod uptime", "deploy", "prod"},
		{"ssh root@staging", "root", "staging"},
		{"ssh -i key.pem user@host ls", "user", "host"},
		{"ls -la", "", ""},
		{"ssh host-only", "", "host-only"},
	}

	for _, tt := range tests {
		user, host := ExtractSSHTarget(tt.command)
		if user != tt.wantUser || host != tt.wantHost {
			t.Errorf("ExtractSSHTarget(%q) = (%q, %q), want (%q, %q)",
				tt.command, user, host, tt.wantUser, tt.wantHost)
		}
	}
}
