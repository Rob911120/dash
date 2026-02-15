package dash

import "context"

func defActivity() *ToolDef {
	return &ToolDef{
		Name:        "activity",
		Description: "Get recent Claude Code session activity. Shows sessions with file counts, duration, and project paths.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of sessions to return (default: 10, max: 50)",
				},
			},
		},
		Tags: []string{"read"},
		Fn:   toolActivity,
	}
}

func toolActivity(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}
	return d.RecentActivity(ctx, limit)
}
