package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/jrimmer/chandra/internal/memory/intent"
)

// evaluateCondition checks whether an intent's condition is currently satisfied.
// In v1, only structural time-based rules are evaluated. NL conditions
// (requiring LLM evaluation) are treated as satisfied until v2.
func evaluateCondition(in intent.Intent) bool {
	cond := strings.TrimSpace(strings.ToLower(in.Condition))
	if cond == "" || cond == "on_schedule" {
		return true
	}
	if strings.HasPrefix(cond, "time:") {
		return evaluateTimeWindow(strings.TrimPrefix(cond, "time:"))
	}
	// Unknown condition: pass through (v2 will add LLM evaluation)
	return true
}

// evaluateTimeWindow checks if the current time falls within a HH:MM-HH:MM window.
// Returns true on any parse error (fail open) or if no window configured.
func evaluateTimeWindow(window string) bool {
	parts := strings.SplitN(window, "-", 2)
	if len(parts) != 2 {
		return true // fail open on malformed input
	}

	startH, startM, err := parseHHMM(parts[0])
	if err != nil {
		return true
	}
	endH, endM, err := parseHHMM(parts[1])
	if err != nil {
		return true
	}

	now := time.Now()
	nowMins := now.Hour()*60 + now.Minute()
	startMins := startH*60 + startM
	endMins := endH*60 + endM

	if startMins <= endMins {
		// Simple same-day window, e.g. 09:00-17:00.
		return nowMins >= startMins && nowMins < endMins
	}
	// Overnight window, e.g. 22:00-06:00.
	return nowMins >= startMins || nowMins < endMins
}

// parseHHMM parses a "HH:MM" string and returns hours and minutes.
func parseHHMM(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	var h, m int
	_, err := fmt.Sscanf(s, "%d:%d", &h, &m)
	if err != nil {
		return 0, 0, fmt.Errorf("scheduler: parse HH:MM %q: %w", s, err)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("scheduler: out-of-range time %q", s)
	}
	return h, m, nil
}
