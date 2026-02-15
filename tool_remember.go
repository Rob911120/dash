package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func defRemember() *ToolDef {
	return &ToolDef{
		Name:        "remember",
		Description: "Save an insight, decision, or todo to the graph.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"type", "text"},
			"properties": map[string]any{
				"type":       map[string]any{"type": "string", "enum": []string{"insight", "decision", "todo"}, "description": "Type of note"},
				"text":       map[string]any{"type": "string", "description": "The content to remember"},
				"context":    map[string]any{"type": "string", "description": "Additional context"},
				"session_id": map[string]any{"type": "string", "description": "Session ID to link to"},
			},
		},
		Tags: []string{"write"},
		Fn:   toolRemember,
	}
}

func toolRemember(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	noteType, _ := args["type"].(string)
	text, _ := args["text"].(string)
	contextStr, _ := args["context"].(string)
	sessionID, _ := args["session_id"].(string)

	if noteType == "" || text == "" {
		return nil, fmt.Errorf("type and text are required")
	}

	if noteType != "insight" && noteType != "decision" && noteType != "todo" {
		return nil, fmt.Errorf("type must be 'insight', 'decision', or 'todo'")
	}

	data := map[string]any{
		"text":       text,
		"created_by": "mcp",
	}
	if contextStr != "" {
		data["context"] = contextStr
	}

	dataBytes, _ := json.Marshal(data)

	node := &Node{
		Layer: LayerContext,
		Type:  noteType,
		Name:  text,
		Data:  dataBytes,
	}

	if len(node.Name) > 255 {
		node.Name = node.Name[:252] + "..."
	}

	if err := d.CreateNode(ctx, node); err != nil {
		return nil, err
	}

	// Embed the node async (non-blocking, best-effort)
	go d.EmbedNode(context.Background(), node)

	if sessionID != "" {
		session, err := d.GetNodeByName(ctx, LayerContext, "session", sessionID)
		if err == nil {
			d.CreateEdge(ctx, &Edge{
				SourceID: session.ID,
				TargetID: node.ID,
				Relation: RelationOwns,
			})
			d.CreateEdgeEvent(ctx, &EdgeEvent{
				SourceID:   session.ID,
				TargetID:   node.ID,
				Relation:   EventRelationResultedIn,
				Success:    true,
				OccurredAt: time.Now(),
			})
		}
	}

	result := map[string]any{
		"saved":   true,
		"id":      node.ID,
		"type":    noteType,
		"text":    text,
		"context": contextStr,
	}

	// Auto-link todos to best matching intent
	if noteType == "todo" {
		if intentName, err := d.AutoLinkTaskToIntent(ctx, node.ID, text, contextStr); err == nil && intentName != "" {
			result["auto_linked_intent"] = intentName
		}
	}

	return result, nil
}
