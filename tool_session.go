package dash

import (
	"context"
	"fmt"
)

func defSession() *ToolDef {
	return &ToolDef{
		Name:        "session",
		Description: "Get file operations for a specific session. Shows all files read/written with timestamps.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session_id"},
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "The session ID to get history for",
				},
			},
		},
		Tags: []string{"read"},
		Fn:   toolSession,
	}
}

func toolSession(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	sessionID, ok := args["session_id"].(string)
	if !ok || sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	return d.SessionHistory(ctx, sessionID)
}
