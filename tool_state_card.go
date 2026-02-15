package dash

import (
	"context"
	"fmt"
	"time"
)

func defUpdateStateCard() *ToolDef {
	return &ToolDef{
		Name:        "update_state_card",
		Description: "Update the project state card. The agent writes a free-text summary (10-30 lines) of current focus, active work, and key context. This card is embedded and used as the default query for semantic context retrieval in future sessions.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"text"},
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Free-text project state card (10-30 lines). Include: current focus, active tasks, recent changes, key files, blockers.",
				},
			},
		},
		Tags: []string{"write"},
		Fn:   toolUpdateStateCard,
	}
}

func toolUpdateStateCard(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	text, _ := args["text"].(string)
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	// Get or create the context_frame node
	frame, err := d.GetOrCreateNode(ctx, LayerContext, "context_frame", "current", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get/create context_frame: %w", err)
	}

	// Update with card_text and timestamp
	err = d.UpdateNodeData(ctx, frame, map[string]any{
		"card_text":    text,
		"card_updated": time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update card_text: %w", err)
	}

	// Embed async for semantic search
	go d.EmbedNode(context.Background(), frame)

	return map[string]any{
		"status":       "ok",
		"card_updated": true,
		"node_id":      frame.ID.String(),
	}, nil
}
