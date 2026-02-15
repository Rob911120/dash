package dash

import (
	"context"
	"encoding/json"
	"fmt"
)

func defSuggestImprovement() *ToolDef {
	return &ToolDef{
		Name:        "suggest_improvement",
		Description: "Call this when you identify a concrete improvement, gap, or architectural flaw in the system during our conversation. Don't just mention it in text - record it.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":              map[string]any{"type": "string", "description": "Concise title of the suggestion"},
				"description":        map[string]any{"type": "string", "description": "Detailed technical description"},
				"rationale":          map[string]any{"type": "string", "description": "Why is this important? Relate to mission/intents if possible."},
				"priority":           map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "critical"}},
				"affected_component": map[string]any{"type": "string", "description": "File path or component name involved"},
			},
			"required": []string{"title", "description", "rationale"},
		},
		Tags: []string{"automation", "write"},
		Fn:   toolSuggestImprovement,
	}
}

func toolSuggestImprovement(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	title, _ := args["title"].(string)
	desc, _ := args["description"].(string)
	rationale, _ := args["rationale"].(string)
	priority, _ := args["priority"].(string)
	component, _ := args["affected_component"].(string)

	if title == "" {
		return nil, fmt.Errorf("title required")
	}

	// Create suggestion node
	nodeData := map[string]any{
		"title":       title,
		"description": desc,
		"rationale":   rationale,
		"priority":    priority,
		"status":      "pending", // pending -> accepted (task) | rejected
		"created_by":  "agent",
	}

	dataBytes, _ := json.Marshal(nodeData)
	node := &Node{
		Layer: LayerAutomation,
		Type:  "suggestion",
		Name:  title,
		Data:  dataBytes,
	}
	if len(node.Name) > 255 {
		node.Name = node.Name[:252] + "..."
	}
	if err := d.CreateNode(ctx, node); err != nil {
		return nil, fmt.Errorf("create node: %w", err)
	}

	go d.EmbedNode(context.Background(), node)

	// Link to affected component if found
	var linkedComponent string
	if component != "" {
		target, err := d.GetNodeByPath(ctx, component)
		if err == nil {
			d.CreateEdge(ctx, &Edge{
				SourceID: node.ID,
				TargetID: target.ID,
				Relation: RelationAffects,
			})
			linkedComponent = target.Name
		}
	}

	// Auto-link to intent using existing logic
	var intentName string
	if name, err := d.AutoLinkTaskToIntent(ctx, node.ID, title, desc+" "+rationale); err == nil {
		intentName = name
	}

	msg := fmt.Sprintf("Suggestion '%s' recorded", title)
	if intentName != "" {
		msg += fmt.Sprintf(" and linked to intent '%s'", intentName)
	}
	if linkedComponent != "" {
		msg += fmt.Sprintf(". Affects: %s", linkedComponent)
	}

	return map[string]any{
		"id":      node.ID.String(),
		"message": msg,
		"status":  "created",
	}, nil
}
