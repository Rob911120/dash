package dash

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	queryListNodeVersions = `
		SELECT id, node_id, version, layer, type, name, data, created_at
		FROM node_versions
		WHERE node_id = $1
		ORDER BY version DESC`

	queryGetNodeVersion = `
		SELECT id, node_id, version, layer, type, name, data, created_at
		FROM node_versions
		WHERE node_id = $1 AND version = $2`

	queryGetNodeAtTime = `
		SELECT id, node_id, version, layer, type, name, data, created_at
		FROM node_versions
		WHERE node_id = $1 AND created_at <= $2
		ORDER BY version DESC
		LIMIT 1`

	queryGetNodeCreatedAt = `
		SELECT created_at FROM nodes WHERE id = $1`

	queryCountNodeVersions = `
		SELECT COUNT(*) FROM node_versions WHERE node_id = $1`
)

// GetNodeVersions retrieves all versions of a node, ordered by version descending.
func (d *Dash) GetNodeVersions(ctx context.Context, nodeID uuid.UUID) ([]*NodeVersion, error) {
	rows, err := d.db.QueryContext(ctx, queryListNodeVersions, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodeVersions(rows)
}

// GetNodeVersion retrieves a specific version of a node.
func (d *Dash) GetNodeVersion(ctx context.Context, nodeID uuid.UUID, version int) (*NodeVersion, error) {
	row := d.db.QueryRowContext(ctx, queryGetNodeVersion, nodeID, version)
	v, err := scanNodeVersion(row)
	if err == sql.ErrNoRows {
		return nil, ErrNodeNotFound
	}
	return v, err
}

// GetNodeAtTime retrieves the state of a node at a specific point in time.
// Returns the version that was active at that time, or the current state if no
// versions existed before that time.
func (d *Dash) GetNodeAtTime(ctx context.Context, nodeID uuid.UUID, timestamp time.Time) (*Node, error) {
	// First check if the node existed at that time
	var createdAt time.Time
	err := d.db.QueryRowContext(ctx, queryGetNodeCreatedAt, nodeID).Scan(&createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, err
	}

	// If timestamp is before node creation, return not found
	if timestamp.Before(createdAt) {
		return nil, ErrNodeNotFound
	}

	// Try to find a version from that time
	row := d.db.QueryRowContext(ctx, queryGetNodeAtTime, nodeID, timestamp)
	v, err := scanNodeVersion(row)
	if err == sql.ErrNoRows {
		// No version found - return current state (it was valid at that time)
		return d.GetNode(ctx, nodeID)
	}
	if err != nil {
		return nil, err
	}

	// Convert version to node
	return &Node{
		ID:        v.NodeID,
		Layer:     v.Layer,
		Type:      v.Type,
		Name:      v.Name,
		Data:      v.Data,
		CreatedAt: createdAt,
		UpdatedAt: v.CreatedAt,
	}, nil
}

// CountNodeVersions returns the number of versions for a node.
func (d *Dash) CountNodeVersions(ctx context.Context, nodeID uuid.UUID) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, queryCountNodeVersions, nodeID).Scan(&count)
	return count, err
}

// NodeDiff represents the difference between two node states.
type NodeDiff struct {
	OldVersion int             `json:"old_version"`
	NewVersion int             `json:"new_version"`
	LayerDiff  *StringDiff     `json:"layer_diff,omitempty"`
	TypeDiff   *StringDiff     `json:"type_diff,omitempty"`
	NameDiff   *StringDiff     `json:"name_diff,omitempty"`
	DataDiff   *json.RawMessage `json:"data_diff,omitempty"`
}

// StringDiff represents a change in a string field.
type StringDiff struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// DiffNodeVersions compares two versions of a node and returns the differences.
func (d *Dash) DiffNodeVersions(ctx context.Context, nodeID uuid.UUID, oldVersion, newVersion int) (*NodeDiff, error) {
	old, err := d.GetNodeVersion(ctx, nodeID, oldVersion)
	if err != nil {
		return nil, err
	}

	new, err := d.GetNodeVersion(ctx, nodeID, newVersion)
	if err != nil {
		return nil, err
	}

	diff := &NodeDiff{
		OldVersion: oldVersion,
		NewVersion: newVersion,
	}

	if string(old.Layer) != string(new.Layer) {
		diff.LayerDiff = &StringDiff{Old: string(old.Layer), New: string(new.Layer)}
	}
	if old.Type != new.Type {
		diff.TypeDiff = &StringDiff{Old: old.Type, New: new.Type}
	}
	if old.Name != new.Name {
		diff.NameDiff = &StringDiff{Old: old.Name, New: new.Name}
	}

	// Compare JSON data
	if string(old.Data) != string(new.Data) {
		// For now, just include the new data as the diff
		// A more sophisticated diff could be implemented
		diff.DataDiff = &new.Data
	}

	return diff, nil
}

// scanNodeVersions scans multiple node versions from rows.
func scanNodeVersions(rows *sql.Rows) ([]*NodeVersion, error) {
	var versions []*NodeVersion
	for rows.Next() {
		v, err := scanNodeVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}
