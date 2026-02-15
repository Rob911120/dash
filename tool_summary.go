package dash

import "context"

func defSummary() *ToolDef {
	return &ToolDef{
		Name:        "summary",
		Description: "Get project overview: active tasks, recent sessions, most touched files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scope": map[string]any{"type": "string", "enum": []string{"all", "tasks", "recent", "files"}, "description": "What to include (default: all)"},
				"hours": map[string]any{"type": "integer", "description": "Time window in hours (default: 24)"},
			},
		},
		Tags: []string{"read"},
		Fn:   toolSummary,
	}
}

func toolSummary(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	scope, _ := args["scope"].(string)
	if scope == "" {
		scope = "all"
	}

	hours := 24
	if h, ok := args["hours"].(float64); ok {
		hours = int(h)
	}

	result := make(map[string]any)

	if scope == "all" || scope == "tasks" {
		rows, err := d.db.QueryContext(ctx, `
			SELECT id, name, type,
			       COALESCE(data->>'statement', data->>'description', name) as description,
			       COALESCE(data->>'status', 'active') as status
			FROM nodes
			WHERE layer = 'CONTEXT'
			  AND type IN ('intent', 'plan', 'task')
			  AND COALESCE(data->>'status', 'active') IN ('active', 'in_progress', 'pending')
			  AND deleted_at IS NULL
			ORDER BY updated_at DESC
			LIMIT 10
		`)
		if err == nil {
			defer rows.Close()
			var tasks []map[string]any
			for rows.Next() {
				var id, name, nodeType, desc, status string
				if err := rows.Scan(&id, &name, &nodeType, &desc, &status); err == nil {
					tasks = append(tasks, map[string]any{
						"id":          id,
						"name":        name,
						"type":        nodeType,
						"description": desc,
						"status":      status,
					})
				}
			}
			result["tasks"] = tasks
		}
	}

	if scope == "all" || scope == "recent" {
		sessions, err := d.RecentActivity(ctx, 5)
		if err == nil {
			result["recent_sessions"] = sessions
		}
	}

	if scope == "all" || scope == "files" {
		rows, err := d.db.QueryContext(ctx, `
			SELECT tn.name as file_path,
			       COUNT(*) as touch_count,
			       MAX(ee.occurred_at) as last_touched,
			       COUNT(CASE WHEN ee.relation = 'modified' THEN 1 END) as modify_count
			FROM edge_events ee
			JOIN nodes tn ON tn.id = ee.target_id
			WHERE tn.layer = 'SYSTEM' AND tn.type = 'file'
			  AND ee.occurred_at > NOW() - ($1 || ' hours')::interval
			GROUP BY tn.name
			ORDER BY last_touched DESC
			LIMIT 20
		`, hours)
		if err == nil {
			defer rows.Close()
			var files []map[string]any
			for rows.Next() {
				var path string
				var touchCount, modifyCount int
				var lastTouched interface{}
				if err := rows.Scan(&path, &touchCount, &lastTouched, &modifyCount); err == nil {
					files = append(files, map[string]any{
						"path":         path,
						"touch_count":  touchCount,
						"modify_count": modifyCount,
						"last_touched": lastTouched,
					})
				}
			}
			result["files"] = files
		}
	}

	return result, nil
}
