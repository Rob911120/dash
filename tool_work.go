package dash

import (
	"context"
	"encoding/json"
	"fmt"
)

// WorkProfile defines the type of work context to retrieve
type WorkProfile string

const (
	// ProfileWork focuses on working set: mission, context, active work
	ProfileWork WorkProfile = "work"
	// ProfileTasks focuses on tasks with dependencies and blockers
	ProfileTasks WorkProfile = "tasks"
	// ProfileSummary gives project overview: recent activity, touched files
	ProfileSummary WorkProfile = "summary"
	// ProfileFull combines all profiles for complete context
	ProfileFull WorkProfile = "full"
)

func defWork() *ToolDef {
	return &ToolDef{
		Name:        "work",
		Description: "Get unified work context: mission, tasks, recent activity, and project state. Replaces summary+tasks+working_set+activity with profile-based retrieval.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"profile": map[string]any{
					"type":        "string",
					"enum":        []string{"work", "tasks", "summary", "full"},
					"description": "Context profile: 'work' (mission+context+constraints), 'tasks' (active tasks with deps), 'summary' (recent activity+files), 'full' (everything)",
				},
				"hours": map[string]any{
					"type":        "integer",
					"description": "Time window in hours for recent activity (default: 24, only for summary/full profiles)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results for tasks/activity (default: 10)",
				},
			},
		},
		Tags: []string{"read"},
		Fn:   toolWork,
	}
}

func toolWork(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	profile := ProfileWork
	if p, ok := args["profile"].(string); ok && p != "" {
		profile = WorkProfile(p)
	}

	hours := 24
	if h, ok := args["hours"].(float64); ok {
		hours = int(h)
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	switch profile {
	case ProfileWork:
		return d.getWorkContext(ctx)
	case ProfileTasks:
		return d.getTasksContext(ctx, limit)
	case ProfileSummary:
		return d.getSummaryContext(ctx, hours, limit)
	case ProfileFull:
		return d.getFullContext(ctx, hours, limit)
	default:
		return d.getWorkContext(ctx)
	}
}

// getWorkContext returns mission, context frame, constraints, recent insights/decisions
func (d *Dash) getWorkContext(ctx context.Context) (any, error) {
	return d.AssembleWorkingSet(ctx)
}

// getTasksContext returns active tasks with dependencies and intent links
func (d *Dash) getTasksContext(ctx context.Context, limit int) (any, error) {
	tasks, err := d.GetActiveTasksWithDeps(ctx)
	if err != nil {
		return nil, err
	}

	type taskView struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Type        string   `json:"type"`
		Status      string   `json:"status"`
		Description string   `json:"description,omitempty"`
		Intent      string   `json:"intent,omitempty"`
		BlockedBy   []string `json:"blocked_by,omitempty"`
		Blocks      []string `json:"blocks,omitempty"`
		IsBlocked   bool     `json:"is_blocked"`
	}

	var views []taskView
	count := 0
	for _, t := range tasks {
		if count >= limit {
			break
		}

		desc := ""
		if t.Node.Data != nil {
			var d map[string]any
			if json.Unmarshal(t.Node.Data, &d) == nil {
				desc, _ = d["description"].(string)
				if desc == "" {
					desc, _ = d["statement"].(string)
				}
			}
		}

		views = append(views, taskView{
			ID:          t.Node.ID.String(),
			Name:        t.Node.Name,
			Type:        t.Node.Type,
			Status:      t.Status,
			Description: desc,
			Intent:      t.Intent,
			BlockedBy:   t.BlockedBy,
			Blocks:      t.Blocks,
			IsBlocked:   t.IsBlocked,
		})
		count++
	}

	return map[string]any{
		"tasks":       views,
		"count":       len(views),
		"total_active": len(tasks),
		"profile":     "tasks",
	}, nil
}

// getSummaryContext returns recent sessions and most touched files
func (d *Dash) getSummaryContext(ctx context.Context, hours, limit int) (any, error) {
	result := make(map[string]any)
	result["profile"] = "summary"
	result["hours"] = hours

	// Recent sessions
	sessions, err := d.RecentActivity(ctx, limit)
	if err == nil {
		result["recent_sessions"] = sessions
		result["session_count"] = len(sessions)
	}

	// Most touched files
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
		LIMIT $2
	`, hours, limit*2)

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
		result["file_count"] = len(files)
	}

	return result, nil
}

// getFullContext combines all profiles for complete project overview
func (d *Dash) getFullContext(ctx context.Context, hours, limit int) (any, error) {
	work, err := d.getWorkContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("work context: %w", err)
	}

	tasks, err := d.getTasksContext(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("tasks context: %w", err)
	}

	summary, err := d.getSummaryContext(ctx, hours, limit)
	if err != nil {
		return nil, fmt.Errorf("summary context: %w", err)
	}

	return map[string]any{
		"profile": "full",
		"work":    work,
		"tasks":   tasks,
		"summary": summary,
	}, nil
}
