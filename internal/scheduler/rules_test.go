package scheduler

import (
	"fmt"
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/memory/intent"
)

// intentWith returns an intent.Intent with the given Condition field set.
func intentWith(condition string) intent.Intent {
	return intent.Intent{
		ID:        "test-id",
		Condition: condition,
	}
}

func TestEvaluateCondition_EmptyIsTrue(t *testing.T) {
	if !evaluateCondition(intentWith("")) {
		t.Error("empty condition should be true")
	}
}

func TestEvaluateCondition_OnScheduleIsTrue(t *testing.T) {
	for _, cond := range []string{"on_schedule", "ON_SCHEDULE", "  on_schedule  "} {
		if !evaluateCondition(intentWith(cond)) {
			t.Errorf("condition %q should be true", cond)
		}
	}
}

func TestEvaluateCondition_UnknownIsTrue(t *testing.T) {
	if !evaluateCondition(intentWith("some_nl_condition")) {
		t.Error("unknown condition should pass through as true in v1")
	}
}

func TestEvaluateCondition_TimeWindow_InsideRange(t *testing.T) {
	// Build a window that covers the entire day (00:00-23:59) — always true.
	cond := "time:00:00-23:59"
	if !evaluateCondition(intentWith(cond)) {
		t.Errorf("condition %q should be true for any current time", cond)
	}
}

func TestEvaluateCondition_TimeWindow_OutsideRange(t *testing.T) {
	// Build a window covering only minute 0 of hour 0 (very unlikely to match).
	// We want a range that definitively excludes now. Use a zero-width window
	// at an hour that wraps so that startMins > endMins but is still exclusive.
	// Easiest: use a window that is in the past relative to a fixed known time.
	// Instead, test evaluateTimeWindow directly for determinism.

	// Window "01:00-01:01" — covers 1 minute at 1 AM.
	// Force "not inside" by providing a time that is well outside. Since
	// evaluateTimeWindow uses time.Now() internally we cannot fix the clock here.
	// For a deterministic test use a closed window that we know cannot match
	// any real clock: "01:00-01:00" has startMins == endMins, so the condition
	// startMins <= endMins is true, and nowMins >= startMins && nowMins < endMins
	// is an empty half-open interval [61,61) which is always false.
	result := evaluateTimeWindow("01:00-01:00")
	if result {
		t.Error("empty interval [01:00-01:00) should always be false")
	}
}

func TestEvaluateTimeWindow_MalformedInput(t *testing.T) {
	cases := []string{
		"",        // no dash
		"badtime", // no dash, no colon
		"99:00-10:00", // out-of-range hour
		"09:00-10:60", // out-of-range minute
	}
	for _, c := range cases {
		result := evaluateTimeWindow(c)
		// All malformed inputs should fail open (return true).
		if !result {
			t.Errorf("evaluateTimeWindow(%q): expected true (fail open), got false", c)
		}
	}
}

func TestEvaluateTimeWindow_OvernightWindow(t *testing.T) {
	// An overnight window like "22:00-06:00" should cover 22:00–23:59 and 00:00–05:59.
	// We verify the logic by testing boundary values rather than relying on time.Now().
	now := time.Now()
	h, m := now.Hour(), now.Minute()
	nowMins := h*60 + m

	startMins := 22 * 60 // 22:00
	endMins := 6 * 60    // 06:00

	// Compute expected result using the same overnight logic.
	var expected bool
	// startMins(1320) > endMins(360) → overnight branch.
	expected = nowMins >= startMins || nowMins < endMins

	got := evaluateTimeWindow("22:00-06:00")
	if got != expected {
		t.Errorf("evaluateTimeWindow(\"22:00-06:00\") at %02d:%02d: want %v, got %v", h, m, expected, got)
	}
}

// TestParseHHMM tests the internal parser directly.
func TestParseHHMM(t *testing.T) {
	cases := []struct {
		input   string
		wantH   int
		wantM   int
		wantErr bool
	}{
		{"09:00", 9, 0, false},
		{"23:59", 23, 59, false},
		{"00:00", 0, 0, false},
		{"9:5", 9, 5, false},
		{"24:00", 0, 0, true},  // out of range
		{"09:60", 0, 0, true},  // out of range
		{"abc", 0, 0, true},    // malformed
		{"-1:00", 0, 0, true},  // negative hour (sscanf parses -1, then range check fails)
	}
	for _, tc := range cases {
		h, m, err := parseHHMM(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseHHMM(%q): expected error, got h=%d m=%d", tc.input, h, m)
			}
		} else {
			if err != nil {
				t.Errorf("parseHHMM(%q): unexpected error: %v", tc.input, err)
			}
			if h != tc.wantH || m != tc.wantM {
				t.Errorf("parseHHMM(%q): want h=%d m=%d, got h=%d m=%d", tc.input, tc.wantH, tc.wantM, h, m)
			}
		}
	}
	_ = fmt.Sprintf // ensure fmt is used
}
