package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func defForget() *ToolDef {
	return &ToolDef{
		Name:        "forget",
		Description: "Soft-delete remembered nodes (insight, decision, todo) from the context. Search by name or content text.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]any{
				"query":       map[string]any{"type": "string", "description": "Search term to match node name or content"},
				"type":        map[string]any{"type": "string", "enum": []string{"insight", "decision", "todo"}, "description": "Optional: only match nodes of this type"},
				"confirm_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional: specific node IDs to delete (skips search)"},
				"dry_run":     map[string]any{"type": "boolean", "description": "If true, show what would be deleted without actually deleting", "default": false},
			},
		},
		Tags: []string{"write", "admin"},
		Fn:   toolForget,
		ChallengeFunc: func(ctx context.Context, d *Dash, args map[string]any) *Challenge {
			// Skip challenge for dry_run (it's read-only)
			if dry, _ := args["dry_run"].(bool); dry {
				return nil
			}
			// Skip challenge if specific IDs provided (explicit intent)
			if ids, ok := args["confirm_ids"].([]any); ok && len(ids) > 0 {
				return nil
			}
			return &Challenge{
				ID:       "forget-confirm",
				Question: "This will soft-delete matching remembered nodes. Set confirm=true to proceed.",
				Options:  []string{"confirm=true", "cancel"},
			}
		},
	}
}

func toolForget(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	noteType, _ := args["type"].(string)
	dryRun, _ := args["dry_run"].(bool)
	confirmIDs, _ := args["confirm_ids"].([]any)

	// Validate type if provided
	if noteType != "" && noteType != "insight" && noteType != "decision" && noteType != "todo" {
		return nil, fmt.Errorf("type must be 'insight', 'decision', or 'todo'")
	}

	var matches []ForgetMatch

	// If specific IDs provided, fetch those nodes
	if len(confirmIDs) > 0 {
		for _, idAny := range confirmIDs {
			idStr, ok := idAny.(string)
			if !ok {
				continue
			}
			id, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			node, err := d.GetNode(ctx, id)
			if err != nil {
				continue
			}
			// Only include if it's a rememberable type
			if node.Type != "insight" && node.Type != "decision" && node.Type != "todo" {
				continue
			}
			// Filter by type if specified
			if noteType != "" && node.Type != noteType {
				continue
			}
			matches = append(matches, nodeToForgetMatch(node))
		}
	} else {
		// Search by query
		searchQuery := strings.ToLower(query)
		
		// Determine which types to search
		types := []string{"insight", "decision", "todo"}
		if noteType != "" {
			types = []string{noteType}
		}

		for _, t := range types {
			nodes, err := d.ListNodesByLayerType(ctx, LayerContext, t)
			if err != nil {
				continue
			}
			for _, node := range nodes {
				// Parse data to check text content
				var data map[string]any
				if err := json.Unmarshal(node.Data, &data); err != nil {
					data = make(map[string]any)
				}
				
				// Match on name or text content
				nameLower := strings.ToLower(node.Name)
				textLower := ""
				if text, ok := data["text"].(string); ok {
					textLower = strings.ToLower(text)
				}
				
				if strings.Contains(nameLower, searchQuery) || strings.Contains(textLower, searchQuery) {
					matches = append(matches, nodeToForgetMatch(node))
				}
			}
		}
	}

	if len(matches) == 0 {
		return map[string]any{
			"found":     0,
			"deleted":   0,
			"matches":   []ForgetMatch{},
			"dry_run":   dryRun,
			"message":   "No matching nodes found",
		}, nil
	}

	// If dry run, just return what would be deleted
	if dryRun {
		return map[string]any{
			"found":     len(matches),
			"deleted":   0,
			"matches":   matches,
			"dry_run":   true,
			"message":   fmt.Sprintf("Would delete %d node(s). Set dry_run=false to confirm.", len(matches)),
		}, nil
	}

	// Perform soft-deletes
	deleted := 0
	failed := 0
	for _, match := range matches {
		id, err := uuid.Parse(match.ID)
		if err != nil {
			failed++
			continue
		}
		if err := d.SoftDeleteNode(ctx, id); err != nil {
			failed++
			continue
		}
		deleted++
	}

	result := map[string]any{
		"found":     len(matches),
		"deleted":   deleted,
		"failed":    failed,
		"matches":   matches,
		"dry_run":   false,
		"message":   fmt.Sprintf("Soft-deleted %d of %d node(s)", deleted, len(matches)),
	}

	return result, nil
}

// ForgetMatch represents a node that matches forget criteria
type ForgetMatch struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

func nodeToForgetMatch(node *Node) ForgetMatch {
	// Parse data to extract text and created_at
	var data map[string]any
	if err := json.Unmarshal(node.Data, &data); err != nil {
		data = make(map[string]any)
	}
	
	var text string
	if t, ok := data["text"].(string); ok {
		text = t
	}
	
	createdAt := node.CreatedAt.Format(time.RFC3339)

	return ForgetMatch{
		ID:        node.ID.String(),
		Type:      node.Type,
		Name:      node.Name,
		Text:      text,
		CreatedAt: createdAt,
	}
}
