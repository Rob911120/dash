package dash

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
)

var (
	// ErrNodeNotFound is returned when a node is not found.
	ErrNodeNotFound = errors.New("node not found")

	// ErrNodeDeleted is returned when attempting to access a soft-deleted node.
	ErrNodeDeleted = errors.New("node has been deleted")
)

const (
	queryGetNode = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE id = $1`

	queryGetNodeActive = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE id = $1 AND deleted_at IS NULL`

	queryListNodes = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE deleted_at IS NULL
		ORDER BY created_at DESC`

	queryListNodesByLayer = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC`

	queryListNodesByLayerType = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = $1 AND type = $2 AND deleted_at IS NULL
		ORDER BY created_at DESC`

	queryGetNodeByName = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = $1 AND type = $2 AND name = $3 AND deleted_at IS NULL`

	queryInsertNode = `
		INSERT INTO nodes (layer, type, name, data)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`

	queryUpdateNode = `
		UPDATE nodes
		SET layer = $2, type = $3, name = $4, data = $5
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING updated_at`

	querySoftDeleteNode = `
		UPDATE nodes
		SET deleted_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING deleted_at`

	querySearchNodes = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE deleted_at IS NULL
		  AND ($1::dash_layer IS NULL OR layer = $1)
		  AND ($2::text IS NULL OR type = $2)
		  AND ($3::text IS NULL OR name ILIKE $3)
		  AND ($4::jsonb IS NULL OR data @> $4)
		ORDER BY created_at DESC
		LIMIT $5`
)

// GetNode retrieves a node by ID, including soft-deleted nodes.
func (d *Dash) GetNode(ctx context.Context, id uuid.UUID) (*Node, error) {
	row := d.db.QueryRowContext(ctx, queryGetNode, id)
	node, err := scanNode(row)
	if err == sql.ErrNoRows {
		return nil, ErrNodeNotFound
	}
	return node, err
}

// GetNodeActive retrieves an active (not soft-deleted) node by ID.
func (d *Dash) GetNodeActive(ctx context.Context, id uuid.UUID) (*Node, error) {
	row := d.db.QueryRowContext(ctx, queryGetNodeActive, id)
	node, err := scanNode(row)
	if err == sql.ErrNoRows {
		return nil, ErrNodeNotFound
	}
	return node, err
}

// GetNodeByName retrieves an active node by layer, type, and name.
func (d *Dash) GetNodeByName(ctx context.Context, layer Layer, nodeType, name string) (*Node, error) {
	row := d.db.QueryRowContext(ctx, queryGetNodeByName, layer, nodeType, name)
	node, err := scanNode(row)
	if err == sql.ErrNoRows {
		return nil, ErrNodeNotFound
	}
	return node, err
}

// GetNodeByPath finds a SYSTEM.file node by path. It normalizes the path,
// tries an exact match first, then falls back to matching by basename.
func (d *Dash) GetNodeByPath(ctx context.Context, path string) (*Node, error) {
	cleaned := filepath.Clean(path)

	// Exact match
	node, err := d.GetNodeByName(ctx, LayerSystem, "file", cleaned)
	if err == nil {
		return node, nil
	}

	// Fallback: search by basename (e.g. "mcp.go" finds "/dash/dash/mcp.go")
	base := filepath.Base(cleaned)
	if base != cleaned {
		// Only do fallback if the input wasn't already just a basename
		return nil, ErrNodeNotFound
	}
	pattern := "%/" + base
	results, err := d.SearchNodes(ctx, NodeFilter{
		Layer:       layerPtr(LayerSystem),
		Type:        strPtr("file"),
		NamePattern: &pattern,
		Limit:       1,
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, ErrNodeNotFound
	}
	return results[0], nil
}

func layerPtr(l Layer) *Layer { return &l }
func strPtr(s string) *string { return &s }

// ListNodes retrieves all active nodes.
func (d *Dash) ListNodes(ctx context.Context) ([]*Node, error) {
	rows, err := d.db.QueryContext(ctx, queryListNodes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodes(rows)
}

// ListNodesByLayer retrieves all active nodes in a specific layer.
func (d *Dash) ListNodesByLayer(ctx context.Context, layer Layer) ([]*Node, error) {
	rows, err := d.db.QueryContext(ctx, queryListNodesByLayer, layer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodes(rows)
}

// ListNodesByLayerType retrieves all active nodes with a specific layer and type.
func (d *Dash) ListNodesByLayerType(ctx context.Context, layer Layer, nodeType string) ([]*Node, error) {
	rows, err := d.db.QueryContext(ctx, queryListNodesByLayerType, layer, nodeType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodes(rows)
}

// NodeFilter defines filters for searching nodes.
type NodeFilter struct {
	Layer       *Layer
	Type        *string
	NamePattern *string // ILIKE pattern
	DataFilter  map[string]any
	Limit       int
}

// SearchNodes searches for nodes matching the given filter.
func (d *Dash) SearchNodes(ctx context.Context, filter NodeFilter) ([]*Node, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	var layerArg, typeArg, nameArg, dataArg any

	if filter.Layer != nil {
		layerArg = *filter.Layer
	}
	if filter.Type != nil {
		typeArg = *filter.Type
	}
	if filter.NamePattern != nil {
		nameArg = *filter.NamePattern
	}
	if filter.DataFilter != nil {
		dataBytes, err := json.Marshal(filter.DataFilter)
		if err != nil {
			return nil, fmt.Errorf("invalid data filter: %w", err)
		}
		dataArg = dataBytes
	}

	rows, err := d.db.QueryContext(ctx, querySearchNodes, layerArg, typeArg, nameArg, dataArg, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodes(rows)
}

// CreateNode creates a new node and returns it with generated fields populated.
func (d *Dash) CreateNode(ctx context.Context, node *Node) error {
	if node.Data == nil {
		node.Data = json.RawMessage(`{}`)
	}

	err := d.db.QueryRowContext(
		ctx,
		queryInsertNode,
		node.Layer,
		node.Type,
		node.Name,
		node.Data,
	).Scan(&node.ID, &node.CreatedAt, &node.UpdatedAt)

	return err
}

// UpdateNode updates an existing active node.
func (d *Dash) UpdateNode(ctx context.Context, node *Node) error {
	if node.Data == nil {
		node.Data = json.RawMessage(`{}`)
	}

	err := d.db.QueryRowContext(
		ctx,
		queryUpdateNode,
		node.ID,
		node.Layer,
		node.Type,
		node.Name,
		node.Data,
	).Scan(&node.UpdatedAt)

	if err == sql.ErrNoRows {
		return ErrNodeNotFound
	}
	return err
}

// SoftDeleteNode soft-deletes a node by setting deleted_at.
// This also cascades to deprecate related edges via trigger.
func (d *Dash) SoftDeleteNode(ctx context.Context, id uuid.UUID) error {
	var deletedAt sql.NullTime
	err := d.db.QueryRowContext(ctx, querySoftDeleteNode, id).Scan(&deletedAt)

	if err == sql.ErrNoRows {
		return ErrNodeNotFound
	}
	return err
}

// UpdateTaskStatus updates the status field in a task node's data.
func (d *Dash) UpdateTaskStatus(ctx context.Context, id uuid.UUID, newStatus string) error {
	node, err := d.GetNodeActive(ctx, id)
	if err != nil {
		return err
	}

	var data map[string]any
	if node.Data != nil {
		if err := json.Unmarshal(node.Data, &data); err != nil {
			data = map[string]any{}
		}
	} else {
		data = map[string]any{}
	}

	data["status"] = newStatus
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return err
	}
	node.Data = dataJSON

	return d.UpdateNode(ctx, node)
}

// scanNodes scans multiple nodes from rows.
func scanNodes(rows *sql.Rows) ([]*Node, error) {
	var nodes []*Node
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

const queryUpdateNodeEmbedding = `
	UPDATE nodes
	SET embedding = $2, content_hash = $3, embedding_at = NOW()
	WHERE id = $1 AND deleted_at IS NULL
	RETURNING embedding_at`

const queryGetNodeWithEmbedding = `
	SELECT id, layer, type, name, data, content_hash, embedding_at, created_at, updated_at, deleted_at
	FROM nodes
	WHERE id = $1 AND deleted_at IS NULL`

// UpdateNodeEmbedding updates the embedding, content hash, and embedding timestamp for a node.
func (d *Dash) UpdateNodeEmbedding(ctx context.Context, id uuid.UUID, embedding []float32, contentHash string) error {
	// Convert []float32 to pgvector format string: [0.1,0.2,...]
	var embeddingArg any
	if embedding != nil {
		embeddingArg = float32SliceToVector(embedding)
	}

	var embeddingAt sql.NullTime
	err := d.db.QueryRowContext(ctx, queryUpdateNodeEmbedding, id, embeddingArg, contentHash).Scan(&embeddingAt)
	if err == sql.ErrNoRows {
		return ErrNodeNotFound
	}
	return err
}

// GetNodeContentHash returns the current content_hash for a node.
// Returns empty string if node doesn't exist or has no hash.
func (d *Dash) GetNodeContentHash(ctx context.Context, id uuid.UUID) (string, error) {
	var hash sql.NullString
	err := d.db.QueryRowContext(ctx, `
		SELECT content_hash FROM nodes WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return hash.String, nil
}

// float32SliceToVector converts a float32 slice to pgvector format string.
func float32SliceToVector(v []float32) string {
	if len(v) == 0 {
		return ""
	}
	// Format: [0.1,0.2,0.3,...]
	var buf []byte
	buf = append(buf, '[')
	for i, f := range v {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, fmt.Sprintf("%g", f)...)
	}
	buf = append(buf, ']')
	return string(buf)
}
