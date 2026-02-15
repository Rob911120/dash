package dash

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	queryInsertObservation = `
		INSERT INTO observations (node_id, type, value, data, observed_at)
		VALUES ($1, $2, $3, $4, COALESCE($5, NOW()))
		RETURNING id, observed_at`

	queryGetPreToolUseTime = `
		SELECT observed_at
		FROM observations
		WHERE type = 'tool_event'
		  AND data->'claude_code'->>'tool_use_id' = $1
		  AND data->'normalized'->>'event' = 'tool.pre'
		ORDER BY observed_at DESC
		LIMIT 1`

	queryListObservationsByNode = `
		SELECT id, node_id, type, value, data, observed_at
		FROM observations
		WHERE node_id = $1
		  AND observed_at >= $2
		  AND observed_at < $3
		ORDER BY observed_at DESC`

	queryListObservationsByNodeType = `
		SELECT id, node_id, type, value, data, observed_at
		FROM observations
		WHERE node_id = $1 AND type = $2
		  AND observed_at >= $3
		  AND observed_at < $4
		ORDER BY observed_at DESC`

	queryListObservationsByType = `
		SELECT id, node_id, type, value, data, observed_at
		FROM observations
		WHERE type = $1
		  AND observed_at >= $2
		  AND observed_at < $3
		ORDER BY observed_at DESC
		LIMIT $4`

	queryGetLatestObservation = `
		SELECT id, node_id, type, value, data, observed_at
		FROM observations
		WHERE node_id = $1 AND type = $2
		ORDER BY observed_at DESC
		LIMIT 1`

	queryCountObservationsByNode = `
		SELECT COUNT(*)
		FROM observations
		WHERE node_id = $1
		  AND observed_at >= $2
		  AND observed_at < $3`

	queryAggregateObservations = `
		SELECT
			COUNT(*) AS count,
			AVG(value) AS avg_value,
			MIN(value) AS min_value,
			MAX(value) AS max_value,
			SUM(value) AS sum_value
		FROM observations
		WHERE node_id = $1 AND type = $2
		  AND observed_at >= $3
		  AND observed_at < $4`
)

// ObservationAggregates holds aggregate statistics for observations.
type ObservationAggregates struct {
	Count    int
	AvgValue *float64
	MinValue *float64
	MaxValue *float64
	SumValue *float64
}

// CreateObservation creates a new observation.
// If ObservedAt is zero, the current time will be used.
func (d *Dash) CreateObservation(ctx context.Context, obs *Observation) error {
	if obs.Data == nil {
		obs.Data = json.RawMessage(`{}`)
	}

	var observedAt any
	if !obs.ObservedAt.IsZero() {
		observedAt = obs.ObservedAt
	}

	err := d.db.QueryRowContext(
		ctx,
		queryInsertObservation,
		obs.NodeID,
		obs.Type,
		obs.Value,
		obs.Data,
		observedAt,
	).Scan(&obs.ID, &obs.ObservedAt)

	return err
}

// StoreObservation is a convenience method that resolves a session name to its
// node ID and creates an observation. Used by TUI chat to log reasoning etc.
func (d *Dash) StoreObservation(ctx context.Context, sessionName, obsType string, data map[string]any) error {
	node, err := d.GetNodeByName(ctx, LayerContext, "session", sessionName)
	if err != nil {
		return err
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return d.CreateObservation(ctx, &Observation{
		NodeID: node.ID,
		Type:   obsType,
		Data:   dataJSON,
	})
}

// ListObservationsByNode retrieves observations for a node within a time range.
// IMPORTANT: Always specify a time range for partition pruning.
func (d *Dash) ListObservationsByNode(ctx context.Context, nodeID uuid.UUID, timeRange TimeRange) ([]*Observation, error) {
	rows, err := d.db.QueryContext(ctx, queryListObservationsByNode, nodeID, timeRange.Start, timeRange.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanObservations(rows)
}

// ListObservationsByNodeType retrieves observations for a node with a specific type.
func (d *Dash) ListObservationsByNodeType(ctx context.Context, nodeID uuid.UUID, obsType string, timeRange TimeRange) ([]*Observation, error) {
	rows, err := d.db.QueryContext(ctx, queryListObservationsByNodeType, nodeID, obsType, timeRange.Start, timeRange.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanObservations(rows)
}

// ListObservationsByType retrieves observations of a specific type across all nodes.
func (d *Dash) ListObservationsByType(ctx context.Context, obsType string, timeRange TimeRange, limit int) ([]*Observation, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := d.db.QueryContext(ctx, queryListObservationsByType, obsType, timeRange.Start, timeRange.End, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanObservations(rows)
}

// GetLatestObservation retrieves the most recent observation of a type for a node.
func (d *Dash) GetLatestObservation(ctx context.Context, nodeID uuid.UUID, obsType string) (*Observation, error) {
	row := d.db.QueryRowContext(ctx, queryGetLatestObservation, nodeID, obsType)
	obs, err := scanObservation(row)
	if err == sql.ErrNoRows {
		return nil, nil // No observation found is not an error
	}
	return obs, err
}

// CountObservationsByNode counts observations for a node in a time range.
func (d *Dash) CountObservationsByNode(ctx context.Context, nodeID uuid.UUID, timeRange TimeRange) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, queryCountObservationsByNode, nodeID, timeRange.Start, timeRange.End).Scan(&count)
	return count, err
}

// AggregateObservations calculates aggregate statistics for observations.
func (d *Dash) AggregateObservations(ctx context.Context, nodeID uuid.UUID, obsType string, timeRange TimeRange) (*ObservationAggregates, error) {
	var agg ObservationAggregates
	var avgValue, minValue, maxValue, sumValue sql.NullFloat64

	err := d.db.QueryRowContext(
		ctx,
		queryAggregateObservations,
		nodeID,
		obsType,
		timeRange.Start,
		timeRange.End,
	).Scan(&agg.Count, &avgValue, &minValue, &maxValue, &sumValue)

	if err != nil {
		return nil, err
	}

	if avgValue.Valid {
		agg.AvgValue = &avgValue.Float64
	}
	if minValue.Valid {
		agg.MinValue = &minValue.Float64
	}
	if maxValue.Valid {
		agg.MaxValue = &maxValue.Float64
	}
	if sumValue.Valid {
		agg.SumValue = &sumValue.Float64
	}

	return &agg, nil
}

// GetPreToolUseTime retrieves the timestamp of a PreToolUse event by tool_use_id.
// This is used to calculate tool execution duration.
// Returns zero time if not found.
func (d *Dash) GetPreToolUseTime(ctx context.Context, toolUseID string) (time.Time, error) {
	var observedAt time.Time
	err := d.db.QueryRowContext(ctx, queryGetPreToolUseTime, toolUseID).Scan(&observedAt)
	if err == sql.ErrNoRows {
		return time.Time{}, nil // Not found is not an error
	}
	return observedAt, err
}

// scanObservations scans multiple observations from rows.
func scanObservations(rows *sql.Rows) ([]*Observation, error) {
	var observations []*Observation
	for rows.Next() {
		obs, err := scanObservation(rows)
		if err != nil {
			return nil, err
		}
		observations = append(observations, obs)
	}
	return observations, rows.Err()
}
