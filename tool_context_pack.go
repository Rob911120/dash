package dash

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func defContextPack() *ToolDef {
	return &ToolDef{
		Name:        "context_pack",
		Description: "Assemble a ranked context pack with reranked results combining semantic similarity, recency, frequency, and graph proximity signals.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural language query to search for",
				},
				"profile": map[string]any{
					"type":        "string",
					"description": "Retrieval profile: 'task' (narrow, top-5), 'plan' (broad, top-15), 'default' (balanced, top-8)",
					"enum":        []string{"task", "plan", "default"},
				},
				"task_name": map[string]any{
					"type":        "string",
					"description": "Optional task name for graph proximity boosting",
				},
			},
		},
		Tags: []string{"read"},
		Fn:   toolContextPack,
	}
}

func toolContextPack(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required")
	}

	profile := ProfileDefault
	if p, ok := args["profile"].(string); ok {
		switch RetrievalProfile(p) {
		case ProfileTask, ProfilePlan, ProfileDefault:
			profile = RetrievalProfile(p)
		}
	}

	var taskID *uuid.UUID
	if taskName, ok := args["task_name"].(string); ok && taskName != "" {
		// Search all tasks (not just active) - completed tasks still have useful affects-edges
		layer := LayerContext
		typ := "task"
		nodes, err := d.SearchNodes(ctx, NodeFilter{Layer: &layer, Type: &typ})
		if err == nil {
			for _, n := range nodes {
				if n.Name == taskName {
					id := n.ID
					taskID = &id
					break
				}
			}
		}
	}

	pack, err := d.AssembleContextPack(ctx, query, profile, taskID)
	if err != nil {
		return nil, err
	}

	return pack.ToMap(), nil
}
