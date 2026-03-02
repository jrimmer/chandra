package executor

import (
	"encoding/json"
	"fmt"
)

// Default state size limits.
const (
	DefaultMaxStepOutputBytes = 64 * 1024       // 64 KB per step output
	DefaultMaxPlanStateBytes  = 512 * 1024       // 512 KB for aggregate plan state
)

// EnforceStepOutputLimit returns an error if the step output exceeds the limit.
func EnforceStepOutputLimit(data json.RawMessage, maxBytes int) (json.RawMessage, error) {
	if data == nil {
		return nil, nil
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("step output size %d exceeds limit %d bytes", len(data), maxBytes)
	}
	return data, nil
}

// EnforcePlanStateLimit returns an error if the plan state exceeds the limit.
func EnforcePlanStateLimit(state json.RawMessage, maxBytes int) (json.RawMessage, error) {
	if state == nil {
		return nil, nil
	}
	if len(state) > maxBytes {
		return nil, fmt.Errorf("plan state size %d exceeds limit %d bytes", len(state), maxBytes)
	}
	return state, nil
}
