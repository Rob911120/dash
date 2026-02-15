package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	queryCountModifiedFiles = `
		SELECT COUNT(DISTINCT target_id)
		FROM edge_events
		WHERE source_id = $1 AND relation = 'modified'`

	queryCountObservedFiles = `
		SELECT COUNT(DISTINCT target_id)
		FROM edge_events
		WHERE source_id = $1 AND relation = 'observed'`

	queryCountUniqueTools = `
		SELECT COUNT(DISTINCT data->>'tool_name')
		FROM observations
		WHERE node_id = $1 AND type = 'tool_event'
		AND data->>'tool_name' IS NOT NULL`

	queryCountSessionEdgeEvents = `
		SELECT COUNT(*)
		FROM edge_events
		WHERE source_id = $1`
)

// CalculateRichnessScore computes a quality score (0-100) for a session.
// Scoring rubric:
//   - Modified files:  max 30 (1=5, 2-3=10, 4-6=20, 7+=30)
//   - Observed files:  max 15 (1-2=5, 3-5=10, 6+=15)
//   - Unique tools:    max 20 (4 per tool, capped)
//   - Duration:        max 15 (<5min=5, 5-15=10, 15+=15)
//   - Edge events:     max 20 (1-5=5, 6-15=10, 16-30=15, 31+=20)
func (d *Dash) CalculateRichnessScore(ctx context.Context, sessionNodeID uuid.UUID) (int, map[string]any, error) {
	// Get session node for started_at
	session, err := d.GetNode(ctx, sessionNodeID)
	if err != nil {
		return 0, nil, fmt.Errorf("get session: %w", err)
	}

	var sessionData map[string]any
	if err := json.Unmarshal(session.Data, &sessionData); err != nil {
		sessionData = map[string]any{}
	}

	// Query metrics
	var modifiedFiles, observedFiles, uniqueTools, edgeEvents int

	if err := d.db.QueryRowContext(ctx, queryCountModifiedFiles, sessionNodeID).Scan(&modifiedFiles); err != nil {
		modifiedFiles = 0
	}
	if err := d.db.QueryRowContext(ctx, queryCountObservedFiles, sessionNodeID).Scan(&observedFiles); err != nil {
		observedFiles = 0
	}
	if err := d.db.QueryRowContext(ctx, queryCountUniqueTools, sessionNodeID).Scan(&uniqueTools); err != nil {
		uniqueTools = 0
	}
	if err := d.db.QueryRowContext(ctx, queryCountSessionEdgeEvents, sessionNodeID).Scan(&edgeEvents); err != nil {
		edgeEvents = 0
	}

	// Calculate duration
	var durationMin float64
	if startedAt, ok := sessionData["started_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, startedAt); err == nil {
			durationMin = time.Since(t).Minutes()
		}
	}

	// Score components
	modScore := scoreModifiedFiles(modifiedFiles)
	obsScore := scoreObservedFiles(observedFiles)
	toolScore := scoreUniqueTools(uniqueTools)
	durScore := scoreDuration(durationMin)
	evtScore := scoreEdgeEvents(edgeEvents)

	total := modScore + obsScore + toolScore + durScore + evtScore

	breakdown := map[string]any{
		"modified_files":       modifiedFiles,
		"modified_files_score": modScore,
		"observed_files":       observedFiles,
		"observed_files_score": obsScore,
		"unique_tools":         uniqueTools,
		"unique_tools_score":   toolScore,
		"duration_min":         int(durationMin),
		"duration_score":       durScore,
		"edge_events":          edgeEvents,
		"edge_events_score":    evtScore,
	}

	return total, breakdown, nil
}

func scoreModifiedFiles(n int) int {
	switch {
	case n >= 7:
		return 30
	case n >= 4:
		return 20
	case n >= 2:
		return 10
	case n >= 1:
		return 5
	default:
		return 0
	}
}

func scoreObservedFiles(n int) int {
	switch {
	case n >= 6:
		return 15
	case n >= 3:
		return 10
	case n >= 1:
		return 5
	default:
		return 0
	}
}

func scoreUniqueTools(n int) int {
	s := n * 4
	if s > 20 {
		return 20
	}
	return s
}

func scoreDuration(min float64) int {
	switch {
	case min >= 15:
		return 15
	case min >= 5:
		return 10
	case min >= 1:
		return 5
	default:
		return 0
	}
}

func scoreEdgeEvents(n int) int {
	switch {
	case n >= 31:
		return 20
	case n >= 16:
		return 15
	case n >= 6:
		return 10
	case n >= 1:
		return 5
	default:
		return 0
	}
}

const querySessionModifiedFiles = `
	SELECT n.name
	FROM edge_events ee
	JOIN nodes n ON n.id = ee.target_id AND n.deleted_at IS NULL
	WHERE ee.source_id = $1 AND ee.relation = 'modified'
	ORDER BY ee.occurred_at DESC`

// SuggestInsights generates insight suggestions for a session based on what happened.
func (d *Dash) SuggestInsights(ctx context.Context, sessionNodeID uuid.UUID) ([]map[string]string, error) {
	rows, err := d.db.QueryContext(ctx, querySessionModifiedFiles, sessionNodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			files = append(files, name)
		}
	}
	if len(files) == 0 {
		return nil, nil
	}

	var suggestions []map[string]string

	// Suggest insight about what files were modified
	if len(files) >= 3 {
		suggestions = append(suggestions, map[string]string{
			"type":    "multi-file-change",
			"text":    fmt.Sprintf("Session modifierade %d filer: %s", len(files), summarizeFiles(files)),
			"context": "Vilken feature/fix var detta? Dokumentera intentionen.",
		})
	}

	// Check for new file patterns
	newPkgFiles := filterByContains(files, "/")
	if len(newPkgFiles) >= 2 {
		dirs := uniqueDirs(newPkgFiles)
		if len(dirs) >= 2 {
			suggestions = append(suggestions, map[string]string{
				"type":    "cross-package",
				"text":    fmt.Sprintf("Ändringar spänner över %d paket/kataloger", len(dirs)),
				"context": "Var detta en arkitekturell ändring? Dokumentera designbeslutet.",
			})
		}
	}

	// Suggest if hook/mcp files were modified (meta-improvement)
	for _, f := range files {
		if contains(f, "hook") || contains(f, "mcp") {
			suggestions = append(suggestions, map[string]string{
				"type":    "meta-improvement",
				"text":    "Sessionen modifierade systemets egna hooks/MCP",
				"context": "Systemet förbättrade sig själv. Dokumentera vad som ändrades och varför.",
			})
			break
		}
	}

	if len(suggestions) > 3 {
		suggestions = suggestions[:3]
	}
	return suggestions, nil
}

func summarizeFiles(files []string) string {
	if len(files) <= 3 {
		return fmt.Sprintf("%v", files)
	}
	return fmt.Sprintf("%s och %d till", files[0], len(files)-1)
}

func filterByContains(items []string, substr string) []string {
	var out []string
	for _, s := range items {
		if contains(s, substr) {
			out = append(out, s)
		}
	}
	return out
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func uniqueDirs(paths []string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		idx := lastIndex(p, '/')
		if idx > 0 {
			dir := p[:idx]
			seen[dir] = true
		}
	}
	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	return dirs
}

func lastIndex(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
