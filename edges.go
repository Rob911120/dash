package dash

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

var (
	// ErrEdgeNotFound is returned when an edge is not found.
	ErrEdgeNotFound = errors.New("edge not found")

	// ErrSelfLoop is returned when attempting to create an edge from a node to itself.
	ErrSelfLoop = errors.New("self-loops are not allowed")
)

const (
	queryGetEdge = `
		SELECT id, source_id, target_id, relation, data, created_at, deprecated_at
		FROM edges
		WHERE id = $1`

	queryGetEdgeActive = `
		SELECT id, source_id, target_id, relation, data, created_at, deprecated_at
		FROM edges
		WHERE id = $1 AND deprecated_at IS NULL`

	queryListEdgesBySource = `
		SELECT id, source_id, target_id, relation, data, created_at, deprecated_at
		FROM edges
		WHERE source_id = $1 AND deprecated_at IS NULL
		ORDER BY created_at DESC`

	queryListEdgesByTarget = `
		SELECT id, source_id, target_id, relation, data, created_at, deprecated_at
		FROM edges
		WHERE target_id = $1 AND deprecated_at IS NULL
		ORDER BY created_at DESC`

	queryListEdgesBySourceRelation = `
		SELECT id, source_id, target_id, relation, data, created_at, deprecated_at
		FROM edges
		WHERE source_id = $1 AND relation = $2 AND deprecated_at IS NULL
		ORDER BY created_at DESC`

	queryListEdgesBetween = `
		SELECT id, source_id, target_id, relation, data, created_at, deprecated_at
		FROM edges
		WHERE source_id = $1 AND target_id = $2 AND deprecated_at IS NULL
		ORDER BY created_at DESC`

	queryInsertEdge = `
		INSERT INTO edges (source_id, target_id, relation, data)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`

	queryDeprecateEdge = `
		UPDATE edges
		SET deprecated_at = NOW()
		WHERE id = $1 AND deprecated_at IS NULL
		RETURNING deprecated_at`

	queryDeprecateEdgesBetween = `
		UPDATE edges
		SET deprecated_at = NOW()
		WHERE source_id = $1 AND target_id = $2 AND deprecated_at IS NULL`
)

// GetEdge retrieves an edge by ID, including deprecated edges.
func (d *Dash) GetEdge(ctx context.Context, id uuid.UUID) (*Edge, error) {
	row := d.db.QueryRowContext(ctx, queryGetEdge, id)
	edge, err := scanEdge(row)
	if err == sql.ErrNoRows {
		return nil, ErrEdgeNotFound
	}
	return edge, err
}

// GetEdgeActive retrieves an active (not deprecated) edge by ID.
func (d *Dash) GetEdgeActive(ctx context.Context, id uuid.UUID) (*Edge, error) {
	row := d.db.QueryRowContext(ctx, queryGetEdgeActive, id)
	edge, err := scanEdge(row)
	if err == sql.ErrNoRows {
		return nil, ErrEdgeNotFound
	}
	return edge, err
}

// ListEdgesBySource retrieves all active edges from a source node.
func (d *Dash) ListEdgesBySource(ctx context.Context, sourceID uuid.UUID) ([]*Edge, error) {
	rows, err := d.db.QueryContext(ctx, queryListEdgesBySource, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEdges(rows)
}

// ListEdgesByTarget retrieves all active edges to a target node.
func (d *Dash) ListEdgesByTarget(ctx context.Context, targetID uuid.UUID) ([]*Edge, error) {
	rows, err := d.db.QueryContext(ctx, queryListEdgesByTarget, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEdges(rows)
}

// ListEdgesBySourceRelation retrieves all active edges from a source with a specific relation.
func (d *Dash) ListEdgesBySourceRelation(ctx context.Context, sourceID uuid.UUID, relation Relation) ([]*Edge, error) {
	rows, err := d.db.QueryContext(ctx, queryListEdgesBySourceRelation, sourceID, relation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEdges(rows)
}

// ListEdgesBetween retrieves all active edges between two specific nodes.
func (d *Dash) ListEdgesBetween(ctx context.Context, sourceID, targetID uuid.UUID) ([]*Edge, error) {
	rows, err := d.db.QueryContext(ctx, queryListEdgesBetween, sourceID, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEdges(rows)
}

// CreateEdge creates a new edge between two nodes.
func (d *Dash) CreateEdge(ctx context.Context, edge *Edge) error {
	if edge.SourceID == edge.TargetID {
		return ErrSelfLoop
	}

	if edge.Data == nil {
		edge.Data = json.RawMessage(`{}`)
	}

	err := d.db.QueryRowContext(
		ctx,
		queryInsertEdge,
		edge.SourceID,
		edge.TargetID,
		edge.Relation,
		edge.Data,
	).Scan(&edge.ID, &edge.CreatedAt)

	return err
}

// DeprecateEdge deprecates an edge by setting deprecated_at.
func (d *Dash) DeprecateEdge(ctx context.Context, id uuid.UUID) error {
	var deprecatedAt sql.NullTime
	err := d.db.QueryRowContext(ctx, queryDeprecateEdge, id).Scan(&deprecatedAt)

	if err == sql.ErrNoRows {
		return ErrEdgeNotFound
	}
	return err
}

// DeprecateEdgesBetween deprecates all edges between two nodes.
func (d *Dash) DeprecateEdgesBetween(ctx context.Context, sourceID, targetID uuid.UUID) error {
	_, err := d.db.ExecContext(ctx, queryDeprecateEdgesBetween, sourceID, targetID)
	return err
}

// scanEdges scans multiple edges from rows.
func scanEdges(rows *sql.Rows) ([]*Edge, error) {
	var edges []*Edge
	for rows.Next() {
		edge, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}
