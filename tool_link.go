package dash

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

func defLink() *ToolDef {
	return &ToolDef{
		Name:        "link",
		Description: "Manage edges (stable relationships) between nodes. Operations: create, list (by source or target), deprecate.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]any{
				"op":       map[string]any{"type": "string", "enum": []string{"create", "list", "deprecate"}, "description": "Operation to perform"},
				"id":       map[string]any{"type": "string", "description": "Edge UUID (for deprecate)"},
				"source":   map[string]any{"type": "string", "description": "Source node UUID"},
				"target":   map[string]any{"type": "string", "description": "Target node UUID"},
				"relation": map[string]any{"type": "string", "enum": []string{"depends_on", "owns", "uses", "generated_by", "instance_of", "child_of", "configured_by", "implements", "affects", "derived_from", "justifies", "based_on", "points_to", "supersedes"}, "description": "Relationship type"},
				"data":     map[string]any{"type": "object", "description": "Edge data"},
			},
		},
		Tags: []string{"graph", "write"},
		Fn:   toolLink,
	}
}

func toolLink(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	op, _ := args["op"].(string)
	if op == "" {
		return nil, fmt.Errorf("op is required")
	}

	switch op {
	case "create":
		sourceStr, _ := args["source"].(string)
		targetStr, _ := args["target"].(string)
		relationStr, _ := args["relation"].(string)

		if sourceStr == "" || targetStr == "" || relationStr == "" {
			return nil, fmt.Errorf("source, target, and relation are required for create")
		}

		sourceID, err := uuid.Parse(sourceStr)
		if err != nil {
			return nil, fmt.Errorf("invalid source UUID: %w", err)
		}
		targetID, err := uuid.Parse(targetStr)
		if err != nil {
			return nil, fmt.Errorf("invalid target UUID: %w", err)
		}

		edge := &Edge{
			SourceID: sourceID,
			TargetID: targetID,
			Relation: Relation(relationStr),
		}

		if data, ok := args["data"].(map[string]any); ok {
			dataBytes, err := json.Marshal(data)
			if err != nil {
				return nil, fmt.Errorf("invalid data: %w", err)
			}
			edge.Data = dataBytes
		}

		if err := d.CreateEdge(ctx, edge); err != nil {
			return nil, err
		}
		return edge, nil

	case "list":
		sourceStr, _ := args["source"].(string)
		targetStr, _ := args["target"].(string)

		if sourceStr != "" {
			sourceID, err := uuid.Parse(sourceStr)
			if err != nil {
				return nil, fmt.Errorf("invalid source UUID: %w", err)
			}
			return d.ListEdgesBySource(ctx, sourceID)
		}
		if targetStr != "" {
			targetID, err := uuid.Parse(targetStr)
			if err != nil {
				return nil, fmt.Errorf("invalid target UUID: %w", err)
			}
			return d.ListEdgesByTarget(ctx, targetID)
		}
		return nil, fmt.Errorf("provide either 'source' or 'target' for list")

	case "deprecate":
		idStr, ok := args["id"].(string)
		if !ok || idStr == "" {
			return nil, fmt.Errorf("id is required for deprecate")
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("invalid UUID: %w", err)
		}

		if err := d.DeprecateEdge(ctx, id); err != nil {
			return nil, err
		}
		return map[string]any{"deprecated": true, "id": idStr}, nil

	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
}
