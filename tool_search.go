package dash

import (
	"context"
	"fmt"
)

func defSearch() *ToolDef {
	return &ToolDef{
		Name:        "search",
		Description: "Semantic search over files using embeddings. Find files related to a concept or query.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural language query to search for",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results (default: 10)",
				},
			},
		},
		Tags: []string{"read"},
		Fn:   toolSearch,
	}
}

func toolSearch(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	results, err := d.SearchSimilar(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	// Format results with type info for mixed results
	var output []map[string]any
	for _, r := range results {
		item := map[string]any{
			"name":     r.Name,
			"layer":    r.Layer,
			"type":     r.Type,
			"distance": r.Distance,
		}
		if r.Path != "" {
			item["file_path"] = r.Path
		}
		output = append(output, item)
	}
	return output, nil
}
