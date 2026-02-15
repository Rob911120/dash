package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// CoEditPattern represents a pair of files frequently modified in the same session.
type CoEditPattern struct {
	Files     []string `json:"files"`
	Frequency int      `json:"frequency"`
	Sessions  []string `json:"sessions"`
}

const queryCoEditingPatterns = `
	WITH modified_files AS (
		SELECT source_id, target_id
		FROM edge_events
		WHERE relation = 'modified'
		AND occurred_at > NOW() - INTERVAL '90 days'
	)
	SELECT
		n1.name AS file1,
		n2.name AS file2,
		COUNT(DISTINCT a.source_id) AS co_edits,
		ARRAY_AGG(DISTINCT a.source_id::text) AS session_ids
	FROM modified_files a
	JOIN modified_files b ON a.source_id = b.source_id AND a.target_id < b.target_id
	JOIN nodes n1 ON n1.id = a.target_id AND n1.deleted_at IS NULL
	JOIN nodes n2 ON n2.id = b.target_id AND n2.deleted_at IS NULL
	GROUP BY n1.name, n2.name
	HAVING COUNT(DISTINCT a.source_id) >= $1
	ORDER BY co_edits DESC
	LIMIT 20`

// DetectCoEditingPatterns finds files that are frequently modified together in the same session.
func (d *Dash) DetectCoEditingPatterns(ctx context.Context, minCoEdits int) ([]CoEditPattern, error) {
	if minCoEdits < 1 {
		minCoEdits = 2
	}

	rows, err := d.db.QueryContext(ctx, queryCoEditingPatterns, minCoEdits)
	if err != nil {
		return nil, fmt.Errorf("query co-editing patterns: %w", err)
	}
	defer rows.Close()

	var patterns []CoEditPattern
	for rows.Next() {
		var file1, file2 string
		var frequency int
		var sessionIDs pq.StringArray

		if err := rows.Scan(&file1, &file2, &frequency, &sessionIDs); err != nil {
			return nil, fmt.Errorf("scan co-editing pattern: %w", err)
		}

		patterns = append(patterns, CoEditPattern{
			Files:     []string{file1, file2},
			Frequency: frequency,
			Sessions:  []string(sessionIDs),
		})
	}

	return patterns, rows.Err()
}

// FileChurn represents a file with high modification frequency.
type FileChurn struct {
	FilePath     string `json:"file_path"`
	ModifyCount  int    `json:"modify_count"`
	SessionCount int    `json:"session_count"`
}

// ToolSequence represents a common tool usage pattern.
type ToolSequence struct {
	Tool1     string `json:"tool1"`
	Tool2     string `json:"tool2"`
	Frequency int    `json:"frequency"`
}

const queryFileChurn = `
	SELECT n.name, COUNT(*) as modifications, COUNT(DISTINCT ee.source_id) as sessions
	FROM edge_events ee
	JOIN nodes n ON n.id = ee.target_id AND n.deleted_at IS NULL
	WHERE ee.relation = 'modified'
	  AND ee.occurred_at > NOW() - INTERVAL '30 days'
	GROUP BY n.name
	HAVING COUNT(*) >= $1
	ORDER BY modifications DESC
	LIMIT 20`

const queryToolSequences = `
	WITH tool_pairs AS (
		SELECT
			o1.data->>'tool_name' as tool1,
			o2.data->>'tool_name' as tool2
		FROM observations o1
		JOIN observations o2 ON o1.node_id = o2.node_id
			AND o2.observed_at > o1.observed_at
			AND o2.observed_at < o1.observed_at + INTERVAL '2 minutes'
		WHERE o1.type = 'tool_event' AND o2.type = 'tool_event'
			AND o1.data->>'tool_name' IS NOT NULL
			AND o2.data->>'tool_name' IS NOT NULL
			AND o1.observed_at > NOW() - INTERVAL '7 days'
	)
	SELECT tool1, tool2, COUNT(*) as freq
	FROM tool_pairs
	WHERE tool1 != tool2
	GROUP BY tool1, tool2
	HAVING COUNT(*) >= $1
	ORDER BY freq DESC
	LIMIT 15`

// DetectFileChurn finds files with high modification frequency.
func (d *Dash) DetectFileChurn(ctx context.Context, minModifications int) ([]FileChurn, error) {
	if minModifications < 2 {
		minModifications = 2
	}
	rows, err := d.db.QueryContext(ctx, queryFileChurn, minModifications)
	if err != nil {
		return nil, fmt.Errorf("query file churn: %w", err)
	}
	defer rows.Close()

	var results []FileChurn
	for rows.Next() {
		var fc FileChurn
		if err := rows.Scan(&fc.FilePath, &fc.ModifyCount, &fc.SessionCount); err != nil {
			continue
		}
		results = append(results, fc)
	}
	return results, rows.Err()
}

// DetectToolSequences finds common tool usage patterns.
func (d *Dash) DetectToolSequences(ctx context.Context, minFrequency int) ([]ToolSequence, error) {
	if minFrequency < 2 {
		minFrequency = 2
	}
	rows, err := d.db.QueryContext(ctx, queryToolSequences, minFrequency)
	if err != nil {
		return nil, fmt.Errorf("query tool sequences: %w", err)
	}
	defer rows.Close()

	var results []ToolSequence
	for rows.Next() {
		var ts ToolSequence
		if err := rows.Scan(&ts.Tool1, &ts.Tool2, &ts.Frequency); err != nil {
			continue
		}
		results = append(results, ts)
	}
	return results, rows.Err()
}

// StorePatterns saves detected patterns as AUTOMATION.pattern nodes.
func (d *Dash) StorePatterns(ctx context.Context, patterns []CoEditPattern) error {
	now := time.Now().UTC().Format(time.RFC3339)

	for _, p := range patterns {
		if len(p.Files) < 2 {
			continue
		}
		name := fmt.Sprintf("co-edit:%s+%s", p.Files[0], p.Files[1])

		// Check if pattern already exists
		existing, _ := d.GetNodeByName(ctx, LayerAutomation, "pattern", name)

		data := map[string]any{
			"type":            "co-editing",
			"files":           p.Files,
			"frequency":       p.Frequency,
			"sessions":        p.Sessions,
			"detected_at":     now,
			"last_updated_at": now,
		}

		if existing != nil {
			data["first_detected_at"] = existing.CreatedAt.Format(time.RFC3339)
			if err := d.UpdateNodeData(ctx, existing, data); err != nil {
				return fmt.Errorf("update pattern %s: %w", name, err)
			}
		} else {
			data["first_detected_at"] = now
			dataJSON, err := json.Marshal(data)
			if err != nil {
				return fmt.Errorf("marshal pattern data: %w", err)
			}
			node := &Node{
				Layer: LayerAutomation,
				Type:  "pattern",
				Name:  name,
				Data:  dataJSON,
			}
			if err := d.CreateNode(ctx, node); err != nil {
				return fmt.Errorf("create pattern %s: %w", name, err)
			}
		}
	}

	return nil
}
