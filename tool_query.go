package dash

import (
	"context"
	"fmt"
)

func defQuery() *ToolDef {
	return &ToolDef{
		Name:        "query",
		Description: "Execute a read-only SQL query against the dash graph database. Tables: nodes, edges, edge_events, observations.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "SQL SELECT query to execute",
				},
			},
		},
		Tags: []string{"read"},
		Fn:   toolQuery,
	}
}

func toolQuery(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required")
	}

	if !isSelectQuery(query) {
		return nil, fmt.Errorf("only SELECT queries are allowed")
	}

	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		row := make(map[string]any)
		for i, col := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		results = append(results, row)
	}

	return map[string]any{
		"columns": cols,
		"rows":    results,
		"count":   len(results),
	}, rows.Err()
}
