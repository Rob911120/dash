package dash

import "context"

func defGC() *ToolDef {
	return &ToolDef{
		Name:        "gc",
		Description: "Run garbage collection on old sessions. Only soft-deletes sessions past retention.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_retention_days":    map[string]any{"type": "integer", "description": "Days to keep non-compressed sessions (default: 14)"},
				"compressed_retention_days": map[string]any{"type": "integer", "description": "Days to keep compressed sessions (default: 30)"},
				"dry_run":                   map[string]any{"type": "boolean", "description": "If true, report without deleting (default: false)"},
			},
		},
		Tags: []string{"admin"},
		Fn:   toolGC,
	}
}

func toolGC(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	policy := GCPolicy{}
	if dp, ok := args["session_retention_days"].(float64); ok {
		policy.SessionRetentionDays = int(dp)
	}
	if dp, ok := args["compressed_retention_days"].(float64); ok {
		policy.CompressedRetentionDays = int(dp)
	}
	if dr, ok := args["dry_run"].(bool); ok {
		policy.DryRun = dr
	}
	return d.RunGC(ctx, policy)
}
