package executor

import (
	"strings"
	"sync"
	"time"
)

// AuditEntry records a single command execution for the audit trail.
type AuditEntry struct {
	PlanID    string
	StepIndex int
	Command   string
	Host      string
	User      string
	ExitCode  int
	StartedAt time.Time
	Duration  time.Duration
	Error     string
}

// AuditTrail maintains an in-memory ring buffer of command audit entries.
type AuditTrail struct {
	mu      sync.Mutex
	entries []AuditEntry
	max     int
}

// NewAuditTrail creates an audit trail with the given maximum entry count.
func NewAuditTrail(maxEntries int) *AuditTrail {
	return &AuditTrail{
		entries: make([]AuditEntry, 0, maxEntries),
		max:     maxEntries,
	}
}

// Record adds an entry to the audit trail. If the trail is full,
// the oldest entry is evicted.
func (a *AuditTrail) Record(entry AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.entries) >= a.max {
		a.entries = a.entries[1:]
	}
	a.entries = append(a.entries, entry)
}

// All returns a copy of all audit entries.
func (a *AuditTrail) All() []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make([]AuditEntry, len(a.entries))
	copy(result, a.entries)
	return result
}

// QueryByPlan returns all audit entries for a given plan ID.
func (a *AuditTrail) QueryByPlan(planID string) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	var result []AuditEntry
	for _, e := range a.entries {
		if e.PlanID == planID {
			result = append(result, e)
		}
	}
	return result
}

// QueryByHost returns all audit entries for a given host.
func (a *AuditTrail) QueryByHost(host string) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	var result []AuditEntry
	for _, e := range a.entries {
		if e.Host == host {
			result = append(result, e)
		}
	}
	return result
}

// ExtractSSHTarget parses an SSH command to extract user and host.
// Returns empty strings if the command is not an SSH command.
func ExtractSSHTarget(command string) (user, host string) {
	parts := strings.Fields(command)
	if len(parts) == 0 || parts[0] != "ssh" {
		return "", ""
	}

	// Find the user@host part — skip flags (tokens starting with -)
	// and their arguments.
	for i := 1; i < len(parts); i++ {
		if parts[i][0] == '-' {
			// Skip flag and its argument if the flag takes a value
			if len(parts[i]) == 2 && i+1 < len(parts) {
				i++ // skip flag argument
			}
			continue
		}

		// This should be the target: user@host or just host
		target := parts[i]
		if idx := strings.Index(target, "@"); idx >= 0 {
			return target[:idx], target[idx+1:]
		}
		return "", target
	}

	return "", ""
}
