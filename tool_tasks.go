package dash

import (
	"context"
	"encoding/json"
)

func defTasks() *ToolDef {
	return &ToolDef{
		Name:        "tasks",
		Description: "View active tasks with dependencies and intent links. Shows which tasks are blocked, what blocks them, and which intent motivates each task.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Tags: []string{"read"},
		Fn:   toolTasks,
	}
}

func toolTasks(ctx context.Context, d *Dash, _ map[string]any) (any, error) {
	tasks, err := d.GetActiveTasksWithDeps(ctx)
	if err != nil {
		return nil, err
	}

	type taskView struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Status      string   `json:"status"`
		Description string   `json:"description,omitempty"`
		Intent      string   `json:"intent,omitempty"`
		BlockedBy   []string `json:"blocked_by,omitempty"`
		Blocks      []string `json:"blocks,omitempty"`
		IsBlocked   bool     `json:"is_blocked"`
	}

	var views []taskView
	for _, t := range tasks {
		desc := ""
		if t.Node.Data != nil {
			var d map[string]any
			if json.Unmarshal(t.Node.Data, &d) == nil {
				desc, _ = d["description"].(string)
			}
		}
		views = append(views, taskView{
			ID:          t.Node.ID.String(),
			Name:        t.Node.Name,
			Status:      t.Status,
			Description: desc,
			Intent:      t.Intent,
			BlockedBy:   t.BlockedBy,
			Blocks:      t.Blocks,
			IsBlocked:   t.IsBlocked,
		})
	}

	return map[string]any{
		"tasks": views,
		"count": len(views),
	}, nil
}
