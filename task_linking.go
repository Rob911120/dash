package dash

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
)

const (
	queryGetActiveTask = `
		SELECT id FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'task'
		AND data->>'status' IN ('active', 'in_progress')
		AND deleted_at IS NULL
		ORDER BY
			CASE WHEN data->>'priority' = 'critical' THEN 0
				 WHEN data->>'priority' = 'high' THEN 1
				 WHEN data->>'priority' = 'medium' THEN 2
				 ELSE 3 END
		LIMIT 1`

	queryCheckAffectsEdge = `
		SELECT id FROM edges
		WHERE source_id = $1 AND target_id = $2 AND relation = 'affects'
		AND deprecated_at IS NULL
		LIMIT 1`
)

// LinkActiveTaskToFile creates an edge from the highest-priority active task
// to a modified file. If no active task exists, this is a no-op.
func (d *Dash) LinkActiveTaskToFile(ctx context.Context, fileNodeID uuid.UUID) error {
	var taskID uuid.UUID
	err := d.db.QueryRowContext(ctx, queryGetActiveTask).Scan(&taskID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}

	// Check if edge already exists
	var existingID uuid.UUID
	err = d.db.QueryRowContext(ctx, queryCheckAffectsEdge, taskID, fileNodeID).Scan(&existingID)
	if err == nil {
		return nil // already linked
	}
	if err != sql.ErrNoRows {
		return err
	}

	return d.CreateEdge(ctx, &Edge{
		SourceID: taskID,
		TargetID: fileNodeID,
		Relation: RelationAffects,
		Data:     json.RawMessage(`{"auto_linked":true}`),
	})
}
