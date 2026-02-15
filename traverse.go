package dash

import (
	"context"
	"database/sql"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	queryGetDependencies = `
		SELECT id, layer, type, name, data, depth, path
		FROM get_dependencies($1, $2)`

	queryGetDependents = `
		SELECT id, layer, type, name, data, depth, path
		FROM get_dependents($1, $2)`

	queryTraceLineage = `
		SELECT event_id, source_id, source_name, target_id, target_name,
		       relation, success, duration_ms, occurred_at, depth
		FROM trace_lineage($1, $2)`
)

// TraversalNode represents a node found during graph traversal.
type TraversalNode struct {
	Node  *Node
	Depth int
	Path  []uuid.UUID
}

// LineageEvent represents an event in a lineage trace.
type LineageEvent struct {
	EventID    uuid.UUID     `json:"event_id"`
	SourceID   uuid.UUID     `json:"source_id"`
	SourceName string        `json:"source_name"`
	TargetID   uuid.UUID     `json:"target_id"`
	TargetName string        `json:"target_name"`
	Relation   EventRelation `json:"relation"`
	Success    bool          `json:"success"`
	DurationMs *int          `json:"duration_ms,omitempty"`
	OccurredAt sql.NullTime  `json:"occurred_at"`
	Depth      int           `json:"depth"`
}

// GetDependencies traverses the graph to find all dependencies of a node.
// Uses the database function get_dependencies() for efficient recursive traversal.
func (d *Dash) GetDependencies(ctx context.Context, nodeID uuid.UUID, maxDepth int) ([]*TraversalNode, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}

	rows, err := d.db.QueryContext(ctx, queryGetDependencies, nodeID, maxDepth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanTraversalNodes(rows)
}

// GetDependents traverses the graph to find all nodes that depend on this node.
// Uses the database function get_dependents() for efficient recursive traversal.
func (d *Dash) GetDependents(ctx context.Context, nodeID uuid.UUID, maxDepth int) ([]*TraversalNode, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}

	rows, err := d.db.QueryContext(ctx, queryGetDependents, nodeID, maxDepth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanTraversalNodes(rows)
}

// TraceLineage follows edge_events to build a causal chain from a node.
func (d *Dash) TraceLineage(ctx context.Context, nodeID uuid.UUID, maxDepth int) ([]*LineageEvent, error) {
	if maxDepth <= 0 {
		maxDepth = 20
	}

	rows, err := d.db.QueryContext(ctx, queryTraceLineage, nodeID, maxDepth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*LineageEvent
	for rows.Next() {
		var e LineageEvent
		var durationMs sql.NullInt32

		err := rows.Scan(
			&e.EventID,
			&e.SourceID,
			&e.SourceName,
			&e.TargetID,
			&e.TargetName,
			&e.Relation,
			&e.Success,
			&durationMs,
			&e.OccurredAt,
			&e.Depth,
		)
		if err != nil {
			return nil, err
		}

		if durationMs.Valid {
			d := int(durationMs.Int32)
			e.DurationMs = &d
		}

		events = append(events, &e)
	}

	return events, rows.Err()
}

// GetConnectedNodes returns all nodes connected to a given node (both directions).
func (d *Dash) GetConnectedNodes(ctx context.Context, nodeID uuid.UUID) ([]*Node, error) {
	query := `
		SELECT DISTINCT n.id, n.layer, n.type, n.name, n.data, n.created_at, n.updated_at, n.deleted_at
		FROM nodes n
		WHERE n.deleted_at IS NULL AND (
			n.id IN (SELECT target_id FROM edges WHERE source_id = $1 AND deprecated_at IS NULL)
			OR n.id IN (SELECT source_id FROM edges WHERE target_id = $1 AND deprecated_at IS NULL)
		)`

	rows, err := d.db.QueryContext(ctx, query, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodes(rows)
}

// FindPath finds a path between two nodes using BFS.
func (d *Dash) FindPath(ctx context.Context, fromID, toID uuid.UUID, maxDepth int) ([]uuid.UUID, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}

	// Use a CTE for BFS path finding
	query := `
		WITH RECURSIVE path_finder AS (
			SELECT
				source_id,
				target_id,
				ARRAY[source_id, target_id] AS path,
				1 AS depth
			FROM edges
			WHERE source_id = $1
			  AND deprecated_at IS NULL

			UNION ALL

			SELECT
				e.source_id,
				e.target_id,
				pf.path || e.target_id,
				pf.depth + 1
			FROM path_finder pf
			JOIN edges e ON e.source_id = pf.target_id
			WHERE e.deprecated_at IS NULL
			  AND NOT (e.target_id = ANY(pf.path))
			  AND pf.depth < $3
		)
		SELECT path
		FROM path_finder
		WHERE target_id = $2
		ORDER BY array_length(path, 1)
		LIMIT 1`

	var pathStrings pq.StringArray
	err := d.db.QueryRowContext(ctx, query, fromID, toID, maxDepth).Scan(&pathStrings)
	if err == sql.ErrNoRows {
		return nil, nil // No path found
	}
	if err != nil {
		return nil, err
	}

	var pathArray []uuid.UUID
	for _, s := range pathStrings {
		if id, err := uuid.Parse(strings.TrimSpace(s)); err == nil {
			pathArray = append(pathArray, id)
		}
	}
	return pathArray, nil
}

// scanTraversalNodes scans traversal results from rows.
func scanTraversalNodes(rows *sql.Rows) ([]*TraversalNode, error) {
	var nodes []*TraversalNode
	for rows.Next() {
		var tn TraversalNode
		var node Node
		var pathStrings pq.StringArray

		err := rows.Scan(
			&node.ID,
			&node.Layer,
			&node.Type,
			&node.Name,
			&node.Data,
			&tn.Depth,
			&pathStrings,
		)
		if err != nil {
			return nil, err
		}

		// Parse UUID strings
		for _, s := range pathStrings {
			if id, err := uuid.Parse(strings.TrimSpace(s)); err == nil {
				tn.Path = append(tn.Path, id)
			}
		}

		tn.Node = &node
		nodes = append(nodes, &tn)
	}

	return nodes, rows.Err()
}
