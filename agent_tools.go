package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ActivitySummary represents a summary of recent activity.
type ActivitySummary struct {
	SessionID   string     `json:"session_id"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	Status      string     `json:"status"`
	FilesRead   int        `json:"files_read"`
	FilesWrote  int        `json:"files_wrote"`
	ToolsUsed   int        `json:"tools_used"`
	TopFiles    []string   `json:"top_files,omitempty"`
	Headline    string     `json:"headline,omitempty"`
	ProjectPath string     `json:"project_path,omitempty"`
}

// FileOperation represents a single file operation event.
type FileOperation struct {
	FilePath   string    `json:"file_path"`
	Operation  string    `json:"operation"` // "observed", "modified", "failed_with"
	ToolName   string    `json:"tool_name"`
	OccurredAt time.Time `json:"occurred_at"`
	Success    bool      `json:"success"`
	DurationMs *int      `json:"duration_ms,omitempty"`
}

// FileEvent represents an event related to a specific file.
type FileEvent struct {
	SessionID  string    `json:"session_id"`
	Operation  string    `json:"operation"`
	ToolName   string    `json:"tool_name"`
	OccurredAt time.Time `json:"occurred_at"`
	Success    bool      `json:"success"`
}

// RecentActivity returns a summary of recent sessions.
func (d *Dash) RecentActivity(ctx context.Context, limit int) ([]ActivitySummary, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	rows, err := d.db.QueryContext(ctx, `
		WITH session_stats AS (
			SELECT
				n.id as session_id,
				n.name as session_name,
				n.data->>'status' as status,
				(n.data->>'started_at')::timestamptz as started_at,
				(n.data->>'ended_at')::timestamptz as ended_at,
				n.data->>'cwd' as cwd,
				COUNT(CASE WHEN ee.relation = 'observed' THEN 1 END) as files_read,
				COUNT(CASE WHEN ee.relation = 'modified' THEN 1 END) as files_wrote,
				COUNT(*) as total_events
			FROM nodes n
			LEFT JOIN edge_events ee ON ee.source_id = n.id
			WHERE n.layer = 'CONTEXT' AND n.type = 'session'
			  AND n.deleted_at IS NULL
			GROUP BY n.id, n.name, n.data
			ORDER BY started_at DESC NULLS LAST
			LIMIT $1
		)
		SELECT
			ss.session_name,
			ss.started_at,
			ss.ended_at,
			ss.status,
			ss.files_read,
			ss.files_wrote,
			ss.total_events,
			ss.cwd,
			COALESCE(tf.top_files, '{}')
		FROM session_stats ss
		LEFT JOIN LATERAL (
			SELECT array_agg(sub.file_name) as top_files
			FROM (
				SELECT n.name as file_name,
					COUNT(CASE WHEN ee.relation = 'modified' THEN 1 END) as mod_cnt,
					COUNT(*) as total_cnt
				FROM edge_events ee
				JOIN nodes n ON n.id = ee.target_id
					AND n.layer = 'SYSTEM' AND n.type = 'file'
				WHERE ee.source_id = ss.session_id
				GROUP BY n.name
				ORDER BY mod_cnt DESC, total_cnt DESC
				LIMIT 5
			) sub
		) tf ON true
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ActivitySummary
	for rows.Next() {
		var a ActivitySummary
		var startedAt, endedAt *time.Time
		var status, cwd *string
		var topFiles pq.StringArray
		err := rows.Scan(&a.SessionID, &startedAt, &endedAt, &status, &a.FilesRead, &a.FilesWrote, &a.ToolsUsed, &cwd, &topFiles)
		if err != nil {
			return nil, err
		}
		if startedAt != nil {
			a.StartedAt = *startedAt
		}
		a.EndedAt = endedAt
		if status != nil {
			a.Status = *status
		}
		if cwd != nil {
			a.ProjectPath = *cwd
		}
		a.TopFiles = []string(topFiles)
		a.Headline = generateHeadline(a.TopFiles)
		results = append(results, a)
	}

	return results, rows.Err()
}

// SessionHistory returns the file operations for a specific session.
func (d *Dash) SessionHistory(ctx context.Context, sessionID string) ([]FileOperation, error) {
	// Find session node
	session, err := d.GetNodeByName(ctx, LayerContext, "session", sessionID)
	if err != nil {
		return nil, err
	}

	rows, err := d.db.QueryContext(ctx, `
		SELECT
			n.name as file_path,
			ee.relation as operation,
			ee.data->>'tool_name' as tool_name,
			ee.occurred_at,
			ee.success,
			ee.duration_ms
		FROM edge_events ee
		JOIN nodes n ON n.id = ee.target_id
		WHERE ee.source_id = $1
		  AND n.layer = 'SYSTEM' AND n.type = 'file'
		ORDER BY ee.occurred_at ASC
	`, session.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FileOperation
	for rows.Next() {
		var f FileOperation
		var toolName *string
		err := rows.Scan(&f.FilePath, &f.Operation, &toolName, &f.OccurredAt, &f.Success, &f.DurationMs)
		if err != nil {
			return nil, err
		}
		if toolName != nil {
			f.ToolName = *toolName
		}
		results = append(results, f)
	}

	return results, rows.Err()
}

// FileHistory returns the event history for a specific file path.
func (d *Dash) FileHistory(ctx context.Context, filePath string) ([]FileEvent, error) {
	// Find file node
	fileNode, err := d.GetNodeByName(ctx, LayerSystem, "file", filePath)
	if err != nil {
		return nil, err
	}

	rows, err := d.db.QueryContext(ctx, `
		SELECT
			n.name as session_id,
			ee.relation as operation,
			ee.data->>'tool_name' as tool_name,
			ee.occurred_at,
			ee.success
		FROM edge_events ee
		JOIN nodes n ON n.id = ee.source_id
		WHERE ee.target_id = $1
		  AND n.layer = 'CONTEXT' AND n.type = 'session'
		ORDER BY ee.occurred_at DESC
		LIMIT 100
	`, fileNode.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FileEvent
	for rows.Next() {
		var f FileEvent
		var toolName *string
		err := rows.Scan(&f.SessionID, &f.Operation, &toolName, &f.OccurredAt, &f.Success)
		if err != nil {
			return nil, err
		}
		if toolName != nil {
			f.ToolName = *toolName
		}
		results = append(results, f)
	}

	return results, rows.Err()
}

// ContextSearchResult combines semantic search with activity data.
type ContextSearchResult struct {
	FilePath       string     `json:"file_path"`
	Distance       float64    `json:"distance"`
	LastModified   *time.Time `json:"last_modified,omitempty"`
	ModifyCount    int        `json:"modify_count"`
	LastObserved   *time.Time `json:"last_observed,omitempty"`
	RecentSessions []string   `json:"recent_sessions,omitempty"`
}

// ContextSearch performs semantic search and enriches with activity context.
func (d *Dash) ContextSearch(ctx context.Context, query string, limit int) ([]ContextSearchResult, error) {
	// First do semantic search
	searchResults, err := d.SearchSimilarFiles(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	if len(searchResults) == 0 {
		return nil, nil
	}

	// Collect file IDs
	fileIDs := make([]uuid.UUID, len(searchResults))
	fileIDToResult := make(map[uuid.UUID]*ContextSearchResult)
	results := make([]ContextSearchResult, len(searchResults))

	for i, sr := range searchResults {
		fileIDs[i] = sr.ID
		results[i] = ContextSearchResult{
			FilePath: sr.Path,
			Distance: sr.Distance,
		}
		fileIDToResult[sr.ID] = &results[i]
	}

	// Enrich with activity data (last modification, counts)
	// This is a simplified query - could be optimized with a single CTE
	for _, id := range fileIDs {
		row := d.db.QueryRowContext(ctx, `
			SELECT
				MAX(CASE WHEN relation = 'modified' THEN occurred_at END) as last_modified,
				COUNT(CASE WHEN relation = 'modified' THEN 1 END) as modify_count,
				MAX(CASE WHEN relation = 'observed' THEN occurred_at END) as last_observed
			FROM edge_events
			WHERE target_id = $1
		`, id)

		var lastMod, lastObs *time.Time
		var modCount int
		if err := row.Scan(&lastMod, &modCount, &lastObs); err == nil {
			if r, ok := fileIDToResult[id]; ok {
				r.LastModified = lastMod
				r.ModifyCount = modCount
				r.LastObserved = lastObs
			}
		}
	}

	return results, nil
}

// GetFileNode returns the file node for a given path, creating it if needed.
func (d *Dash) GetFileNode(ctx context.Context, filePath string) (*Node, error) {
	return d.GetOrCreateNode(ctx, LayerSystem, "file", filePath, map[string]any{
		"path": filePath,
	})
}

// GetFileWithEmbeddingStatus returns file info including embedding status.
type FileEmbeddingStatus struct {
	ID          uuid.UUID  `json:"id"`
	Path        string     `json:"path"`
	ContentHash string     `json:"content_hash,omitempty"`
	HasEmbed    bool       `json:"has_embedding"`
	EmbeddingAt *time.Time `json:"embedding_at,omitempty"`
}

// GetFileEmbeddingStatus returns embedding status for a file.
func (d *Dash) GetFileEmbeddingStatus(ctx context.Context, filePath string) (*FileEmbeddingStatus, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT id, name, content_hash, embedding IS NOT NULL, embedding_at
		FROM nodes
		WHERE layer = 'SYSTEM' AND type = 'file' AND name = $1
		  AND deleted_at IS NULL
	`, filePath)

	var s FileEmbeddingStatus
	var contentHash *string
	var embeddingAt *time.Time
	err := row.Scan(&s.ID, &s.Path, &contentHash, &s.HasEmbed, &embeddingAt)
	if err != nil {
		return nil, err
	}
	if contentHash != nil {
		s.ContentHash = *contentHash
	}
	s.EmbeddingAt = embeddingAt
	return &s, nil
}

// generateHeadline creates a short summary from the top files in a session.
// Shows actual filenames grouped by component for clarity.
// Examples:
//   ["/dash/cmd/dashtui/tool_render.go", "/dash/cmd/dashtui/model.go"] → "dashtui: tool_render, model"
//   ["/dash/dash/agent_tools.go", "/dash/cmd/dashtui/view_work.go"]    → "agent_tools, view_work"
//   ["/dash/dash/mcp.go"]                                              → "mcp"
func generateHeadline(topFiles []string) string {
	if len(topFiles) == 0 {
		return ""
	}

	// Count files per component to decide grouping
	type fileInfo struct {
		component string
		stem      string
	}
	var files []fileInfo
	compCount := make(map[string]int)

	for _, path := range topFiles {
		comp, stem := splitFilePath(path)
		if stem == "" {
			continue
		}
		files = append(files, fileInfo{comp, stem})
		compCount[comp]++
	}

	if len(files) == 0 {
		return ""
	}

	// If all files share one component, prefix with it
	// "dashtui: tool_render, model"
	if len(compCount) == 1 && files[0].component != "" {
		var stems []string
		for i, f := range files {
			if i >= 3 {
				break
			}
			stems = append(stems, f.stem)
		}
		result := files[0].component + ": " + strings.Join(stems, ", ")
		if len(files) > 3 {
			result += fmt.Sprintf(" +%d", len(files)-3)
		}
		return result
	}

	// Mixed components: just show filenames
	// "agent_tools, view_work, mcp"
	var stems []string
	for i, f := range files {
		if i >= 3 {
			break
		}
		stems = append(stems, f.stem)
	}
	result := strings.Join(stems, ", ")
	if len(files) > 3 {
		result += fmt.Sprintf(" +%d", len(files)-3)
	}
	return result
}

// splitFilePath extracts a component name and filename stem from a path.
// /dash/cmd/dashtui/model.go → ("dashtui", "model")
// /dash/dash/mcp.go → ("", "mcp")
// /dash/sql/migrations/010.sql → ("sql", "010")
func splitFilePath(filePath string) (component, stem string) {
	name := filepath.Base(filePath)
	ext := filepath.Ext(name)
	stem = strings.TrimSuffix(name, ext)

	path := strings.TrimPrefix(filePath, "/dash/")
	if path == filePath {
		return "", stem
	}

	parts := strings.Split(path, "/")

	// cmd/X/... → component is X
	if len(parts) >= 2 && parts[0] == "cmd" {
		return parts[1], stem
	}

	// dash/file.go → no component prefix (it's the core package)
	if parts[0] == "dash" {
		return "", stem
	}

	// sql/, scripts/, .claude/ etc → use as component
	if len(parts) >= 2 {
		return parts[0], stem
	}

	return "", stem
}

// Helper to extract data from node JSON.
func extractNodeData(node *Node) map[string]any {
	var data map[string]any
	if node.Data != nil {
		json.Unmarshal(node.Data, &data)
	}
	if data == nil {
		data = make(map[string]any)
	}
	return data
}
