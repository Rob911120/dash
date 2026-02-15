package dash

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// ContextData holds assembled context data (used by suggestion generation).
type ContextData struct {
	RecentFiles []FileActivity
}

// Suggestion is a context-aware recommendation for the session.
type Suggestion struct {
	Text   string
	Reason string
}

// FileActivity represents recent file operations.
type FileActivity struct {
	FilePath   string
	Relation   string
	OccurredAt time.Time
}

// SessionSummary contains summary of a previous session.
type SessionSummary struct {
	EndedAt    time.Time
	StartedAt  time.Time
	FilesCount int
}

const (
	queryGetProjectByPath = `
		SELECT name, data->>'path' as path
		FROM nodes
		WHERE layer = 'SYSTEM' AND type = 'project'
		  AND $1 LIKE data->>'path' || '%'
		  AND deleted_at IS NULL
		ORDER BY LENGTH(data->>'path') DESC
		LIMIT 1`

	queryGetRecentFileActivity = `
		SELECT DISTINCT ON (tn.name)
		    tn.name as file_path,
		    ee.relation,
		    ee.occurred_at
		FROM edge_events ee
		JOIN nodes sn ON sn.id = ee.source_id
		JOIN nodes tn ON tn.id = ee.target_id
		WHERE sn.layer = 'CONTEXT' AND sn.type = 'session'
		  AND tn.layer = 'SYSTEM' AND tn.type = 'file'
		  AND ee.occurred_at > NOW() - INTERVAL '2 hours'
		ORDER BY tn.name, ee.occurred_at DESC
		LIMIT 10`

	queryGetPreviousSession = `
		SELECT
		    data->>'ended_at' as ended_at,
		    data->>'started_at' as started_at,
		    (SELECT COUNT(DISTINCT target_id) FROM edge_events WHERE source_id = n.id) as files_count
		FROM nodes n
		WHERE layer = 'CONTEXT' AND type = 'session'
		  AND name != $1 AND data->>'status' = 'ended'
		  AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 1`
)

func (d *Dash) getRecentFileActivity(ctx context.Context) ([]FileActivity, error) {
	rows, err := d.db.QueryContext(ctx, queryGetRecentFileActivity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []FileActivity
	for rows.Next() {
		var f FileActivity
		if err := rows.Scan(&f.FilePath, &f.Relation, &f.OccurredAt); err != nil {
			continue
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (d *Dash) getPreviousSession(ctx context.Context, currentSessionID string) (*SessionSummary, error) {
	var endedAtStr, startedAtStr sql.NullString
	var filesCount int

	err := d.db.QueryRowContext(ctx, queryGetPreviousSession, currentSessionID).Scan(
		&endedAtStr,
		&startedAtStr,
		&filesCount,
	)
	if err != nil {
		return nil, err
	}

	summary := &SessionSummary{FilesCount: filesCount}

	if endedAtStr.Valid {
		if t, err := time.Parse(time.RFC3339, endedAtStr.String); err == nil {
			summary.EndedAt = t
		}
	}
	if startedAtStr.Valid {
		if t, err := time.Parse(time.RFC3339, startedAtStr.String); err == nil {
			summary.StartedAt = t
		}
	}

	return summary, nil
}

const querySuggestRelatedFiles = `
	SELECT DISTINCT n2.name
	FROM edge_events ee1
	JOIN edge_events ee2 ON ee1.source_id = ee2.source_id AND ee1.target_id != ee2.target_id
	JOIN nodes n1 ON n1.id = ee1.target_id AND n1.deleted_at IS NULL
	JOIN nodes n2 ON n2.id = ee2.target_id AND n2.deleted_at IS NULL
	WHERE ee1.relation = 'modified' AND ee2.relation = 'modified'
	  AND n1.name = $1
	  AND ee1.occurred_at > NOW() - INTERVAL '30 days'
	ORDER BY n2.name
	LIMIT 3`

func (d *Dash) generateSuggestions(ctx context.Context, sc *ContextData) ([]Suggestion, error) {
	var suggestions []Suggestion
	seen := map[string]bool{}

	// Collect recently active file paths for matching
	recentModified := map[string]bool{}
	for _, f := range sc.RecentFiles {
		if f.Relation == "modified" {
			recentModified[f.FilePath] = true
		}
	}

	// 1. Use stored AUTOMATION.pattern nodes (higher quality, frequency-ranked)
	if len(recentModified) > 0 {
		patterns, err := d.getStoredPatternsForFiles(ctx, recentModified)
		if err == nil {
			for _, p := range patterns {
				if len(suggestions) >= 3 {
					break
				}
				if !isInRecentFiles(p.suggested, sc.RecentFiles) && !seen[p.suggested] {
					seen[p.suggested] = true
					suggestions = append(suggestions, Suggestion{
						Text:   p.suggested,
						Reason: fmt.Sprintf("co-edit pattern med %s (%dx)", filepath.Base(p.trigger), p.frequency),
					})
				}
			}
		}
	}

	// 2. Fall back to raw edge_event query for additional suggestions
	if len(suggestions) < 3 && len(sc.RecentFiles) > 0 {
		for _, f := range sc.RecentFiles {
			if f.Relation != "modified" {
				continue
			}
			rows, err := d.db.QueryContext(ctx, querySuggestRelatedFiles, f.FilePath)
			if err != nil {
				continue
			}
			for rows.Next() {
				var relatedFile string
				if err := rows.Scan(&relatedFile); err == nil && !isInRecentFiles(relatedFile, sc.RecentFiles) && !seen[relatedFile] {
					seen[relatedFile] = true
					suggestions = append(suggestions, Suggestion{
						Text:   relatedFile,
						Reason: fmt.Sprintf("ofta ändrad tillsammans med %s", filepath.Base(f.FilePath)),
					})
				}
			}
			rows.Close()
			if len(suggestions) >= 3 {
				break
			}
		}
	}

	if len(suggestions) > 3 {
		suggestions = suggestions[:3]
	}
	return suggestions, nil
}

type patternMatch struct {
	trigger   string // the file we already have
	suggested string // the file we suggest
	frequency int
}

const queryStoredPatterns = `
	SELECT name, data
	FROM nodes
	WHERE layer = 'AUTOMATION' AND type = 'pattern'
	  AND deleted_at IS NULL
	  AND data->>'type' = 'co-editing'
	ORDER BY (data->>'frequency')::int DESC`

// getStoredPatternsForFiles finds stored co-editing patterns relevant to the given files.
func (d *Dash) getStoredPatternsForFiles(ctx context.Context, activeFiles map[string]bool) ([]patternMatch, error) {
	rows, err := d.db.QueryContext(ctx, queryStoredPatterns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []patternMatch
	for rows.Next() {
		var name string
		var data []byte
		if err := rows.Scan(&name, &data); err != nil {
			continue
		}

		var pd struct {
			Files     []string `json:"files"`
			Frequency int      `json:"frequency"`
		}
		if err := json.Unmarshal(data, &pd); err != nil || len(pd.Files) < 2 {
			continue
		}

		// Check if either file in the pattern matches an active file
		for _, active := range []string{pd.Files[0], pd.Files[1]} {
			if activeFiles[active] {
				other := pd.Files[0]
				if other == active {
					other = pd.Files[1]
				}
				if !activeFiles[other] {
					matches = append(matches, patternMatch{
						trigger:   active,
						suggested: other,
						frequency: pd.Frequency,
					})
				}
				break
			}
		}
	}
	return matches, rows.Err()
}

func isInRecentFiles(path string, files []FileActivity) bool {
	for _, f := range files {
		if f.FilePath == path {
			return true
		}
	}
	return false
}

// formatTimeAgo formats a time as a human-readable "X ago" string.
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1min ago"
		}
		return fmt.Sprintf("%dmin ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", hours)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// formatDuration formats a duration as a human-readable string.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}

	switch {
	case d < time.Minute:
		return "<1min"
	case d < time.Hour:
		return fmt.Sprintf("%dmin", int(d.Minutes()))
	default:
		hours := int(d.Hours())
		mins := int(d.Minutes()) % 60
		if mins == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh %dmin", hours, mins)
	}
}
