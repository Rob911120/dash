package dash

import (
	"context"
	"fmt"
	"time"
)

func defGiveToPlanner() *ToolDef {
	return &ToolDef{
		Name:        "give_to_planner",
		Description: "Fire-and-forget delegering till planner-agenten. Skapar en plan-request observation som planner-agenten plockar upp.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"description"},
			"properties": map[string]any{
				"description": map[string]any{
					"type":        "string",
					"description": "Vad som ska planeras",
				},
				"context": map[string]any{
					"type":        "string",
					"description": "Extra kontext för planeringen",
				},
				"priority": map[string]any{
					"type":        "string",
					"enum":        []string{"low", "medium", "high"},
					"description": "Prioritet (default: medium)",
				},
				"affected_files": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filer som berörs av planeringen",
				},
			},
		},
		Fn: handleGiveToPlanner,
	}
}

func handleGiveToPlanner(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	description, _ := args["description"].(string)
	if description == "" {
		return nil, fmt.Errorf("description is required")
	}

	extraCtx, _ := args["context"].(string)
	priority, _ := args["priority"].(string)
	if priority == "" {
		priority = "medium"
	}

	var affectedFiles []string
	if files, ok := args["affected_files"].([]any); ok {
		for _, f := range files {
			if s, ok := f.(string); ok {
				affectedFiles = append(affectedFiles, s)
			}
		}
	}

	requestID := fmt.Sprintf("plan-req-%d", time.Now().UnixMilli())

	obsData := map[string]any{
		"request_id":     requestID,
		"description":    description,
		"context":        extraCtx,
		"priority":       priority,
		"affected_files": affectedFiles,
		"status":         "pending",
	}

	// Store as observation (routed via TUI to planner tab)
	if err := d.StoreObservation(ctx, "", "plan_request", obsData); err != nil {
		// Non-fatal: observation storage may fail if no session, still return accepted
		_ = err
	}

	return map[string]any{
		"request_id": requestID,
		"status":     "accepted",
		"target":     "planner-agent",
	}, nil
}
