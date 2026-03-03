// Package doctor provides the DoctorCheck interface and runner for
// chandra doctor and chandra init's final verification step.
package doctor

import (
	"context"
	"sync"
	"time"
)

// Status represents the outcome of a check.
type Status int

const (
	Pass Status = iota
	Warn
	Fail
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "pass"
	case Warn:
		return "warn"
	default:
		return "fail"
	}
}

// Result is returned by a Check.
type Result struct {
	Status Status
	Detail string
	Fix    string // human-readable remediation hint
}

// CheckResult pairs a result with the check name.
type CheckResult struct {
	Name   string
	Result Result
	// convenience accessors
	Status Status
	Detail string
	Fix    string
}

// Check is the interface that every doctor check implements.
type Check interface {
	Name() string
	Run(ctx context.Context) Result
}

// RunAll runs all checks in parallel, each with a per-check timeout derived
// from the timeoutSec argument. Results are returned in the same order as
// checks. A check that times out is reported as Warn with a retry suggestion.
func RunAll(ctx context.Context, checks []Check, timeoutSec int) []CheckResult {
	results := make([]CheckResult, len(checks))
	var wg sync.WaitGroup

	for i, ch := range checks {
		wg.Add(1)
		go func(idx int, c Check) {
			defer wg.Done()

			timeout := time.Duration(timeoutSec) * time.Second
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			done := make(chan Result, 1)
			go func() {
				done <- c.Run(checkCtx)
			}()

			var r Result
			select {
			case r = <-done:
			case <-checkCtx.Done():
				r = Result{
					Status: Warn,
					Detail: "timed out after " + timeout.String(),
					Fix:    "retry with: chandra doctor",
				}
			}

			results[idx] = CheckResult{
				Name:   c.Name(),
				Result: r,
				Status: r.Status,
				Detail: r.Detail,
				Fix:    r.Fix,
			}
		}(i, ch)
	}

	wg.Wait()
	return results
}

// AnyFailed returns true if any result has Fail status.
func AnyFailed(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == Fail {
			return true
		}
	}
	return false
}

// AnyWarned returns true if any result has Warn status.
func AnyWarned(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == Warn {
			return true
		}
	}
	return false
}
