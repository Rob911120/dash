package dash

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// GCPolicy controls garbage collection behavior.
type GCPolicy struct {
	SessionRetentionDays    int  `json:"session_retention_days"`    // Default 14
	CompressedRetentionDays int  `json:"compressed_retention_days"` // Default 30
	DryRun                  bool `json:"dry_run"`
}

// GCResult contains the results of a garbage collection run.
type GCResult struct {
	DryRun              bool        `json:"dry_run"`
	ExpiredSessions     []GCTarget  `json:"expired_sessions"`
	ExpiredCompressed   []GCTarget  `json:"expired_compressed"`
	TotalSoftDeleted    int         `json:"total_soft_deleted"`
}

// GCTarget represents a node that was or would be garbage collected.
type GCTarget struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Age  string    `json:"age"` // human-readable age
}

// RunGC performs garbage collection on old sessions.
// It NEVER touches: insights, decisions, tasks, mission, context_frame, constraints, SYSTEM.*, AUTOMATION.*
// It only soft-deletes sessions that are past their retention period.
func (d *Dash) RunGC(ctx context.Context, policy GCPolicy) (*GCResult, error) {
	if policy.SessionRetentionDays <= 0 {
		policy.SessionRetentionDays = 14
	}
	if policy.CompressedRetentionDays <= 0 {
		policy.CompressedRetentionDays = 30
	}

	result := &GCResult{DryRun: policy.DryRun}

	// 1. Find non-compressed sessions older than retention
	sessionCutoff := time.Now().AddDate(0, 0, -policy.SessionRetentionDays)
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, name, created_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'session'
		  AND deleted_at IS NULL
		  AND COALESCE(data->>'status', '') != 'compressed'
		  AND COALESCE(data->>'status', '') != 'active'
		  AND created_at < $1
		ORDER BY created_at ASC
	`, sessionCutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var name string
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &createdAt); err != nil {
			continue
		}
		result.ExpiredSessions = append(result.ExpiredSessions, GCTarget{
			ID:   id,
			Name: name,
			Age:  formatDuration(time.Since(createdAt)),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 2. Find compressed sessions older than compressed retention
	compressedCutoff := time.Now().AddDate(0, 0, -policy.CompressedRetentionDays)
	rows2, err := d.db.QueryContext(ctx, `
		SELECT id, name, created_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'session'
		  AND deleted_at IS NULL
		  AND data->>'status' = 'compressed'
		  AND created_at < $1
		ORDER BY created_at ASC
	`, compressedCutoff)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()

	for rows2.Next() {
		var id uuid.UUID
		var name string
		var createdAt time.Time
		if err := rows2.Scan(&id, &name, &createdAt); err != nil {
			continue
		}
		result.ExpiredCompressed = append(result.ExpiredCompressed, GCTarget{
			ID:   id,
			Name: name,
			Age:  formatDuration(time.Since(createdAt)),
		})
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	// 3. Soft-delete if not dry run
	if !policy.DryRun {
		for _, target := range result.ExpiredSessions {
			if err := d.SoftDeleteNode(ctx, target.ID); err == nil {
				result.TotalSoftDeleted++
			}
		}
		for _, target := range result.ExpiredCompressed {
			if err := d.SoftDeleteNode(ctx, target.ID); err == nil {
				result.TotalSoftDeleted++
			}
		}
	} else {
		result.TotalSoftDeleted = len(result.ExpiredSessions) + len(result.ExpiredCompressed)
	}

	return result, nil
}
