package dash

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
)

const (
	queryInsertEdgeEvent = `
		INSERT INTO edge_events (source_id, target_id, relation, success, duration_ms, data, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, NOW()))
		RETURNING id, occurred_at`

	queryListEdgeEventsBySource = `
		SELECT id, source_id, target_id, relation, success, duration_ms, data, occurred_at
		FROM edge_events
		WHERE source_id = $1
		  AND occurred_at >= $2
		  AND occurred_at < $3
		ORDER BY occurred_at DESC`

	queryListEdgeEventsByTarget = `
		SELECT id, source_id, target_id, relation, success, duration_ms, data, occurred_at
		FROM edge_events
		WHERE target_id = $1
		  AND occurred_at >= $2
		  AND occurred_at < $3
		ORDER BY occurred_at DESC`

	queryListEdgeEventsBetween = `
		SELECT id, source_id, target_id, relation, success, duration_ms, data, occurred_at
		FROM edge_events
		WHERE source_id = $1 AND target_id = $2
		  AND occurred_at >= $3
		  AND occurred_at < $4
		ORDER BY occurred_at DESC`

	queryListEdgeEventsByRelation = `
		SELECT id, source_id, target_id, relation, success, duration_ms, data, occurred_at
		FROM edge_events
		WHERE relation = $1
		  AND occurred_at >= $2
		  AND occurred_at < $3
		ORDER BY occurred_at DESC
		LIMIT $4`

	queryCountEdgeEventsBySource = `
		SELECT COUNT(*)
		FROM edge_events
		WHERE source_id = $1
		  AND occurred_at >= $2
		  AND occurred_at < $3`
)

// CreateEdgeEvent creates a new edge event.
// If OccurredAt is zero, the current time will be used.
func (d *Dash) CreateEdgeEvent(ctx context.Context, event *EdgeEvent) error {
	if event.Data == nil {
		event.Data = json.RawMessage(`{}`)
	}

	var occurredAt any
	if !event.OccurredAt.IsZero() {
		occurredAt = event.OccurredAt
	}

	var durationMs any
	if event.DurationMs != nil {
		durationMs = *event.DurationMs
	}

	err := d.db.QueryRowContext(
		ctx,
		queryInsertEdgeEvent,
		event.SourceID,
		event.TargetID,
		event.Relation,
		event.Success,
		durationMs,
		event.Data,
		occurredAt,
	).Scan(&event.ID, &event.OccurredAt)

	return err
}

// ListEdgeEventsBySource retrieves edge events where a node is the source.
// IMPORTANT: Always specify a time range for partition pruning.
func (d *Dash) ListEdgeEventsBySource(ctx context.Context, sourceID uuid.UUID, timeRange TimeRange) ([]*EdgeEvent, error) {
	rows, err := d.db.QueryContext(ctx, queryListEdgeEventsBySource, sourceID, timeRange.Start, timeRange.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEdgeEvents(rows)
}

// ListEdgeEventsByTarget retrieves edge events where a node is the target.
// IMPORTANT: Always specify a time range for partition pruning.
func (d *Dash) ListEdgeEventsByTarget(ctx context.Context, targetID uuid.UUID, timeRange TimeRange) ([]*EdgeEvent, error) {
	rows, err := d.db.QueryContext(ctx, queryListEdgeEventsByTarget, targetID, timeRange.Start, timeRange.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEdgeEvents(rows)
}

// ListEdgeEventsBetween retrieves edge events between two specific nodes.
func (d *Dash) ListEdgeEventsBetween(ctx context.Context, sourceID, targetID uuid.UUID, timeRange TimeRange) ([]*EdgeEvent, error) {
	rows, err := d.db.QueryContext(ctx, queryListEdgeEventsBetween, sourceID, targetID, timeRange.Start, timeRange.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEdgeEvents(rows)
}

// ListEdgeEventsByRelation retrieves edge events with a specific relation type.
func (d *Dash) ListEdgeEventsByRelation(ctx context.Context, relation EventRelation, timeRange TimeRange, limit int) ([]*EdgeEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := d.db.QueryContext(ctx, queryListEdgeEventsByRelation, relation, timeRange.Start, timeRange.End, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEdgeEvents(rows)
}

// CountEdgeEventsBySource counts edge events from a source node in a time range.
func (d *Dash) CountEdgeEventsBySource(ctx context.Context, sourceID uuid.UUID, timeRange TimeRange) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, queryCountEdgeEventsBySource, sourceID, timeRange.Start, timeRange.End).Scan(&count)
	return count, err
}

// scanEdgeEvents scans multiple edge events from rows.
func scanEdgeEvents(rows *sql.Rows) ([]*EdgeEvent, error) {
	var events []*EdgeEvent
	for rows.Next() {
		event, err := scanEdgeEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
