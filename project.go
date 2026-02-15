package dash

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// GetOrCreateProject retrieves or creates a CONTEXT.project node.
// Projects group tasks, intents, and sessions under a named scope.
func (d *Dash) GetOrCreateProject(ctx context.Context, name, path string) (*Node, error) {
	node, err := d.GetOrCreateNode(ctx, LayerContext, "project", name, map[string]any{
		"path":        path,
		"status":      "active",
		"description": name + " project",
	})
	if err != nil {
		return nil, err
	}
	return node, nil
}

// GetProjectTasks returns all active tasks linked to a project via child_of edges.
// Falls back to returning all active tasks if no project-specific tasks are found.
func (d *Dash) GetProjectTasks(ctx context.Context, projectID uuid.UUID) ([]TaskWithDeps, error) {
	// Get task IDs linked to this project
	rows, err := d.db.QueryContext(ctx, `
		SELECT e.source_id
		FROM edges e
		JOIN nodes n ON n.id = e.source_id
		WHERE e.target_id = $1
		  AND e.relation = 'child_of'
		  AND e.deprecated_at IS NULL
		  AND n.layer = 'CONTEXT' AND n.type = 'task'
		  AND n.deleted_at IS NULL
		ORDER BY n.created_at DESC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var taskIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			continue
		}
		taskIDs = append(taskIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// If no project-linked tasks, fall back to all active tasks
	if len(taskIDs) == 0 {
		return d.GetActiveTasksWithDeps(ctx)
	}

	// Build TaskWithDeps for each task
	allTasks, err := d.GetActiveTasksWithDeps(ctx)
	if err != nil {
		return nil, err
	}

	// Filter to only project tasks
	idSet := make(map[uuid.UUID]bool, len(taskIDs))
	for _, id := range taskIDs {
		idSet[id] = true
	}

	var result []TaskWithDeps
	for _, t := range allTasks {
		if idSet[t.Node.ID] {
			result = append(result, t)
		}
	}
	return result, nil
}

// LinkTaskToProject creates a child_of edge from a task to a project.
func (d *Dash) LinkTaskToProject(ctx context.Context, taskID, projectID uuid.UUID) error {
	// Check if edge already exists
	var count int
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM edges
		WHERE source_id = $1 AND target_id = $2
		  AND relation = 'child_of'
		  AND deprecated_at IS NULL
	`, taskID, projectID).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil // Already linked
	}

	return d.CreateEdge(ctx, &Edge{
		SourceID: taskID,
		TargetID: projectID,
		Relation: RelationChildOf,
	})
}

// GetProjectByName retrieves a project node by name.
func (d *Dash) GetProjectByName(ctx context.Context, name string) (*Node, error) {
	return d.GetNodeByName(ctx, LayerContext, "project", name)
}

// ProjectSummary contains a project with its task counts.
type ProjectSummary struct {
	Node         *Node
	ActiveTasks  int
	PendingTasks int
	TotalTasks   int
}

// GetProjectSummary returns a project with task statistics.
func (d *Dash) GetProjectSummary(ctx context.Context, projectID uuid.UUID) (*ProjectSummary, error) {
	node, err := d.GetNodeActive(ctx, projectID)
	if err != nil {
		return nil, err
	}

	var active, pending, total int
	err = d.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE n.data->>'status' = 'active'),
			COUNT(*) FILTER (WHERE n.data->>'status' = 'pending'),
			COUNT(*)
		FROM edges e
		JOIN nodes n ON n.id = e.source_id
		WHERE e.target_id = $1
		  AND e.relation = 'child_of'
		  AND e.deprecated_at IS NULL
		  AND n.layer = 'CONTEXT' AND n.type = 'task'
		  AND n.deleted_at IS NULL
	`, projectID).Scan(&active, &pending, &total)
	if err != nil {
		// Non-fatal: return project without counts
		return &ProjectSummary{Node: node}, nil
	}

	return &ProjectSummary{
		Node:         node,
		ActiveTasks:  active,
		PendingTasks: pending,
		TotalTasks:   total,
	}, nil
}

// EnsureProjectDefaults checks if a "dash" project exists and creates it if not.
// Called at startup to ensure project context is available.
func (d *Dash) EnsureProjectDefaults(ctx context.Context) (*Node, error) {
	node, err := d.GetNodeByName(ctx, LayerContext, "project", "dash")
	if err == nil {
		return node, nil
	}

	// Create default project
	data := map[string]any{
		"path":        "/dash",
		"status":      "active",
		"description": "Dash grafsystem - självförbättrande grafarkitektur",
	}
	dataJSON, _ := json.Marshal(data)
	node = &Node{
		Layer: LayerContext,
		Type:  "project",
		Name:  "dash",
		Data:  dataJSON,
	}
	if err := d.CreateNode(ctx, node); err != nil {
		// Race condition: try to get it again
		if existing, getErr := d.GetNodeByName(ctx, LayerContext, "project", "dash"); getErr == nil {
			return existing, nil
		}
		return nil, err
	}
	return node, nil
}
