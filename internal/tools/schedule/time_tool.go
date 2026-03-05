package scheduletool

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jrimmer/chandra/pkg"
)

type getCurrentTimeTool struct{}

// NewGetCurrentTimeTool returns a tool that reports the current UTC time.
func NewGetCurrentTimeTool() pkg.Tool {
	return &getCurrentTimeTool{}
}

func (t *getCurrentTimeTool) Definition() pkg.ToolDef {
	return pkg.ToolDef{
		Name:        "get_current_time",
		Description: "Returns the current UTC date and time as an ISO 8601 string. Use this before calling schedule_reminder so you can compute the correct due_at timestamp.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func (t *getCurrentTimeTool) Execute(_ context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	return pkg.ToolResult{
		ID:      call.ID,
		Content: time.Now().UTC().Format(time.RFC3339),
	}, nil
}
