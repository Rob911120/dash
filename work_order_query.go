package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// GetActiveWorkOrderForAgent returns the most recent active (non-merged, non-rejected) work order assigned to the given agent.
func (d *Dash) GetActiveWorkOrderForAgent(ctx context.Context, agentKey string) (*WorkOrder, error) {
	if agentKey == "" {
		return nil, nil
	}

	query := `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'AUTOMATION' AND type = 'work_order'
		  AND deleted_at IS NULL
		  AND data->>'agent_key' = $1
		  AND data->>'status' NOT IN ('merged', 'rejected')
		ORDER BY updated_at DESC
		LIMIT 1
	`

	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	row := d.db.QueryRowContext(ctx2, query, agentKey)
	node, err := scanNode(row)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get active work order for agent %s: %w", agentKey, err)
	}
	if node == nil {
		return nil, nil
	}

	var wo WorkOrder
	if err := json.Unmarshal(node.Data, &wo); err != nil {
		return nil, fmt.Errorf("parse work_order data: %w", err)
	}
	wo.Node = node
	return &wo, nil
}
