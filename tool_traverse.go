package dash

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func defTraverse() *ToolDef {
	return &ToolDef{
		Name:        "traverse",
		Description: "Navigate the graph following relationships. Find dependencies, dependents, lineage, or paths between nodes.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"id"},
			"properties": map[string]any{
				"id":        map[string]any{"type": "string", "description": "Starting node UUID"},
				"direction": map[string]any{"type": "string", "enum": []string{"dependencies", "dependents", "lineage"}, "description": "Traversal direction (default: dependencies)"},
				"depth":     map[string]any{"type": "integer", "description": "Maximum traversal depth (default: 10)"},
				"to":        map[string]any{"type": "string", "description": "Target node UUID (for path finding)"},
			},
		},
		Tags: []string{"read", "graph"},
		Fn:   toolTraverse,
	}
}

func toolTraverse(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	idStr, ok := args["id"].(string)
	if !ok || idStr == "" {
		return nil, fmt.Errorf("id is required")
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("invalid UUID: %w", err)
	}

	direction, _ := args["direction"].(string)
	if direction == "" {
		direction = "dependencies"
	}

	depth := 10
	if dp, ok := args["depth"].(float64); ok {
		depth = int(dp)
	}

	if toStr, ok := args["to"].(string); ok && toStr != "" {
		toID, err := uuid.Parse(toStr)
		if err != nil {
			return nil, fmt.Errorf("invalid 'to' UUID: %w", err)
		}
		path, err := d.FindPath(ctx, id, toID, depth)
		if err != nil {
			return nil, err
		}
		if path == nil {
			return map[string]any{"found": false, "message": "no path found"}, nil
		}
		return map[string]any{"found": true, "path": path}, nil
	}

	switch direction {
	case "dependencies":
		return d.GetDependencies(ctx, id, depth)
	case "dependents":
		return d.GetDependents(ctx, id, depth)
	case "lineage":
		return d.TraceLineage(ctx, id, depth)
	default:
		return nil, fmt.Errorf("unknown direction: %s (use: dependencies, dependents, lineage)", direction)
	}
}
