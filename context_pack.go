package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// RetrievalProfile controls how many results and which signals dominate.
type RetrievalProfile string

const (
	ProfileTask    RetrievalProfile = "task"    // narrow, top-5
	ProfilePlan    RetrievalProfile = "plan"    // broad, top-15
	ProfileDefault RetrievalProfile = "default" // balanced, top-8
)

// PackItem is a single ranked result in a context pack.
type PackItem struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Path           string    `json:"path"`
	Layer          string    `json:"layer"`
	Type           string    `json:"type"`
	Summary        string    `json:"summary,omitempty"`   // description/text for CONTEXT nodes
	Score          float64   `json:"score"`               // unified 0-1, higher = better
	Similarity     float64   `json:"similarity"`          // normalized 0-1 (from distance)
	Recency        float64   `json:"recency"`             // 0-1, exponential decay
	Frequency      float64   `json:"frequency"`           // 0-1, log-normalized
	GraphProximity float64   `json:"graph_proximity"`     // 0-1, connected to task?
	WhySelected    string    `json:"why_selected"`        // top signal explanation
}

// ConstraintItem holds a constraint for inclusion in a context pack.
type ConstraintItem struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Text string    `json:"text"`
}

// ContextPack is a ranked retrieval artifact combining multiple signals.
type ContextPack struct {
	Profile     RetrievalProfile `json:"profile"`
	Query       string           `json:"query"`
	Items       []PackItem       `json:"items"`
	Constraints []ConstraintItem `json:"constraints,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
}

// RerankWeights controls how signals are combined into a unified score.
type RerankWeights struct {
	Similarity float64
	Recency    float64
	Frequency  float64
	GraphProx  float64
}

// profileWeights returns the reranking weights for a profile.
func profileWeights(p RetrievalProfile) RerankWeights {
	switch p {
	case ProfileTask:
		return RerankWeights{0.45, 0.20, 0.10, 0.25}
	case ProfilePlan:
		return RerankWeights{0.30, 0.20, 0.20, 0.30}
	default:
		return RerankWeights{0.40, 0.25, 0.15, 0.20}
	}
}

// profileLimit returns the max items for a profile.
func profileLimit(p RetrievalProfile) int {
	switch p {
	case ProfileTask:
		return 5
	case ProfilePlan:
		return 15
	default:
		return 8
	}
}

// PackActivity holds batch-fetched activity data for a node.
type PackActivity struct {
	LastModified *time.Time
	ModifyCount  int
	LastObserved *time.Time
}

// BatchGetPackActivity fetches activity data for mixed node types in batch.
// SYSTEM.file nodes use edge_events; CONTEXT nodes use updated_at from nodes table.
func (d *Dash) BatchGetPackActivity(ctx context.Context, results []*SearchResult) (map[uuid.UUID]PackActivity, error) {
	if len(results) == 0 {
		return nil, nil
	}

	// Partition by type
	var fileIDs []uuid.UUID
	var contextIDs []uuid.UUID
	for _, r := range results {
		if r.Layer == "SYSTEM" && r.Type == "file" {
			fileIDs = append(fileIDs, r.ID)
		} else {
			contextIDs = append(contextIDs, r.ID)
		}
	}

	activity := make(map[uuid.UUID]PackActivity)

	// File activity from edge_events
	if len(fileIDs) > 0 {
		rows, err := d.db.QueryContext(ctx, `
			SELECT target_id,
				MAX(CASE WHEN relation = 'modified' THEN occurred_at END) as last_modified,
				COUNT(CASE WHEN relation = 'modified' THEN 1 END) as modify_count,
				MAX(CASE WHEN relation = 'observed' THEN occurred_at END) as last_observed
			FROM edge_events
			WHERE target_id = ANY($1)
			GROUP BY target_id
		`, pq.Array(fileIDs))
		if err != nil {
			return nil, fmt.Errorf("batch file activity: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var id uuid.UUID
			var fa PackActivity
			if err := rows.Scan(&id, &fa.LastModified, &fa.ModifyCount, &fa.LastObserved); err != nil {
				return nil, err
			}
			activity[id] = fa
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// CONTEXT node activity from nodes.updated_at
	if len(contextIDs) > 0 {
		rows, err := d.db.QueryContext(ctx, `
			SELECT id, updated_at
			FROM nodes
			WHERE id = ANY($1)
			  AND deleted_at IS NULL
		`, pq.Array(contextIDs))
		if err != nil {
			return nil, fmt.Errorf("batch context activity: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var id uuid.UUID
			var updatedAt time.Time
			if err := rows.Scan(&id, &updatedAt); err != nil {
				return nil, err
			}
			activity[id] = PackActivity{
				LastModified: &updatedAt,
				ModifyCount:  1,
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return activity, nil
}

// GetTaskProximity finds nodes connected to a task via direct edges (multiple relation types,
// bidirectional) and shared session activity.
func (d *Dash) GetTaskProximity(ctx context.Context, taskID uuid.UUID, nodeIDs []uuid.UUID) (map[uuid.UUID]float64, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}

	rows, err := d.db.QueryContext(ctx, `
		WITH direct_outgoing AS (
			SELECT target_id as node_id,
				CASE relation
					WHEN 'affects' THEN 1.0
					WHEN 'depends_on' THEN 0.9
					WHEN 'uses' THEN 0.8
					WHEN 'implements' THEN 0.9
					WHEN 'owns' THEN 0.8
					ELSE 0.6
				END as score
			FROM edges
			WHERE source_id = $1
			AND target_id = ANY($2)
			AND deprecated_at IS NULL
		),
		direct_incoming AS (
			SELECT source_id as node_id,
				CASE relation
					WHEN 'affects' THEN 0.8
					WHEN 'depends_on' THEN 0.7
					WHEN 'uses' THEN 0.6
					WHEN 'implements' THEN 0.7
					WHEN 'owns' THEN 0.6
					ELSE 0.5
				END as score
			FROM edges
			WHERE target_id = $1
			AND source_id = ANY($2)
			AND deprecated_at IS NULL
		),
		via_sessions AS (
			SELECT ee2.target_id as node_id,
				LEAST(COUNT(DISTINCT ee2.source_id)::float / 3.0, 1.0) as score
			FROM edge_events ee1
			JOIN edge_events ee2 ON ee1.source_id = ee2.source_id
			WHERE ee1.target_id = $1
			AND ee1.relation = 'triggered'
			AND ee2.target_id = ANY($2)
			AND ee2.relation IN ('modified', 'observed')
			GROUP BY ee2.target_id
		)
		SELECT node_id, MAX(score) as score FROM (
			SELECT node_id, score FROM direct_outgoing
			UNION ALL
			SELECT node_id, score FROM direct_incoming
			UNION ALL
			SELECT node_id, score FROM via_sessions
		) combined
		GROUP BY node_id
	`, taskID, pq.Array(nodeIDs))
	if err != nil {
		return nil, fmt.Errorf("task proximity: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]float64)
	for rows.Next() {
		var id uuid.UUID
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			return nil, err
		}
		result[id] = score
	}
	return result, rows.Err()
}

// BatchGetGraphNeighbors finds nodes connected to the given set via edges.
// Returns neighbor IDs with scores based on relation type.
// Excludes nodes already in the input set.
func (d *Dash) BatchGetGraphNeighbors(ctx context.Context, nodeIDs []uuid.UUID, limit int) (map[uuid.UUID]float64, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}

	rows, err := d.db.QueryContext(ctx, `
		SELECT neighbor_id, relation FROM (
			SELECT target_id as neighbor_id, relation
			FROM edges
			WHERE source_id = ANY($1)
			  AND target_id != ALL($1)
			  AND deprecated_at IS NULL
			UNION
			SELECT source_id as neighbor_id, relation
			FROM edges
			WHERE target_id = ANY($1)
			  AND source_id != ALL($1)
			  AND deprecated_at IS NULL
		) neighbors
	`, pq.Array(nodeIDs))
	if err != nil {
		return nil, fmt.Errorf("graph neighbors: %w", err)
	}
	defer rows.Close()

	// Aggregate: take max score per neighbor
	scores := make(map[uuid.UUID]float64)
	for rows.Next() {
		var id uuid.UUID
		var relation string
		if err := rows.Scan(&id, &relation); err != nil {
			return nil, err
		}
		var score float64
		switch relation {
		case "affects":
			score = 0.5
		case "depends_on":
			score = 0.5
		case "uses":
			score = 0.4
		case "implements":
			score = 0.5
		case "owns":
			score = 0.4
		default:
			score = 0.3
		}
		if existing, ok := scores[id]; !ok || score > existing {
			scores[id] = score
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Cap at limit — keep highest-scored neighbors
	if len(scores) > limit {
		type entry struct {
			id    uuid.UUID
			score float64
		}
		entries := make([]entry, 0, len(scores))
		for id, s := range scores {
			entries = append(entries, entry{id, s})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].score > entries[j].score })
		trimmed := make(map[uuid.UUID]float64, limit)
		for i := 0; i < limit; i++ {
			trimmed[entries[i].id] = entries[i].score
		}
		scores = trimmed
	}

	return scores, nil
}

// fetchPackConstraints retrieves CONTEXT.constraint nodes for inclusion in context packs.
func (d *Dash) fetchPackConstraints(ctx context.Context) ([]ConstraintItem, error) {
	rows, err := d.db.QueryContext(ctx, queryGetConstraints)
	if err != nil {
		return nil, fmt.Errorf("fetch constraints: %w", err)
	}
	defer rows.Close()

	nodes, err := scanNodes(rows)
	if err != nil {
		return nil, err
	}

	var constraints []ConstraintItem
	for _, n := range nodes {
		var data map[string]any
		if err := json.Unmarshal(n.Data, &data); err != nil {
			continue
		}
		text, _ := data["text"].(string)
		if text == "" {
			text, _ = data["description"].(string)
		}
		if text == "" {
			continue
		}
		constraints = append(constraints, ConstraintItem{
			ID:   n.ID,
			Name: n.Name,
			Text: text,
		})
	}
	return constraints, nil
}

// extractSummary extracts a human-readable summary from a CONTEXT node's data.
func extractSummary(sr *SearchResult) string {
	if sr.Layer == "SYSTEM" {
		return ""
	}
	if sr.Data == nil {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal(sr.Data, &data); err != nil {
		return ""
	}
	// Try common fields in order of preference
	for _, key := range []string{"description", "text", "content", "summary"} {
		if v, ok := data[key].(string); ok && v != "" {
			// Truncate long summaries
			if len(v) > 120 {
				return v[:117] + "..."
			}
			return v
		}
	}
	return ""
}

// normalizeDistance converts cosine distance (0=identical, 2=opposite) to similarity (1=identical, 0=opposite).
func normalizeDistance(distance float64) float64 {
	return 1.0 - distance/2.0
}

// computeRecency returns 0-1 score with exponential decay (7-day half-life).
func computeRecency(lastModified *time.Time) float64 {
	if lastModified == nil {
		return 0
	}
	daysSince := time.Since(*lastModified).Hours() / 24.0
	if daysSince < 0 {
		daysSince = 0
	}
	return math.Exp(-0.693 * daysSince / 7.0)
}

// normalizeFrequency returns 0-1 score via log normalization (caps at 32 modifications).
func normalizeFrequency(count int) float64 {
	if count <= 0 {
		return 0
	}
	return math.Min(math.Log2(float64(count)+1)/5.0, 1.0)
}

// computePackScore calculates a unified score from weighted signals.
func computePackScore(item PackItem, w RerankWeights) float64 {
	return w.Similarity*item.Similarity +
		w.Recency*item.Recency +
		w.Frequency*item.Frequency +
		w.GraphProx*item.GraphProximity
}

// generateWhySelected explains the dominant signal for an item.
func generateWhySelected(item PackItem) string {
	if item.Similarity > 0.7 {
		return fmt.Sprintf("semantiskt nära (%.0f%%)", item.Similarity*100)
	}
	if item.Recency > 0.8 {
		return "nyligen ändrad"
	}
	if item.GraphProximity > 0.5 {
		return "kopplad till aktiv task"
	}
	if item.Frequency > 0.6 {
		return "ofta modifierad"
	}
	// Fallback: top two signals
	type signal struct {
		name  string
		value float64
	}
	signals := []signal{
		{"semantisk", item.Similarity},
		{"nylig", item.Recency},
		{"frekvent", item.Frequency},
		{"graf-nära", item.GraphProximity},
	}
	sort.Slice(signals, func(i, j int) bool { return signals[i].value > signals[j].value })
	return signals[0].name + " + " + signals[1].name
}

// AssembleContextPack builds a ranked context pack from search + activity + graph signals.
func (d *Dash) AssembleContextPack(ctx context.Context, query string, profile RetrievalProfile, taskID *uuid.UUID) (*ContextPack, error) {
	limit := profileLimit(profile)
	weights := profileWeights(profile)

	// 1. Over-fetch: get 2x results from vector search across ALL node types
	searchResults, err := d.SearchSimilar(ctx, query, limit*2)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if len(searchResults) == 0 {
		return &ContextPack{Profile: profile, Query: query, CreatedAt: time.Now()}, nil
	}

	// Build ID set for deduplication
	idSet := make(map[uuid.UUID]bool, len(searchResults))
	for _, sr := range searchResults {
		idSet[sr.ID] = true
	}

	// 2. Graph neighborhood expansion
	nodeIDs := make([]uuid.UUID, len(searchResults))
	for i, sr := range searchResults {
		nodeIDs[i] = sr.ID
	}

	neighborScores, err := d.BatchGetGraphNeighbors(ctx, nodeIDs, limit)
	if err != nil {
		neighborScores = make(map[uuid.UUID]float64)
	}

	// Fetch metadata for neighbors not already in results
	var newNeighborIDs []uuid.UUID
	for id := range neighborScores {
		if !idSet[id] {
			newNeighborIDs = append(newNeighborIDs, id)
		}
	}

	if len(newNeighborIDs) > 0 {
		nRows, nErr := d.db.QueryContext(ctx, `
			SELECT id, layer, type, name, data
			FROM nodes
			WHERE id = ANY($1)
			  AND deleted_at IS NULL
		`, pq.Array(newNeighborIDs))
		if nErr == nil {
			defer nRows.Close()
			for nRows.Next() {
				var sr SearchResult
				if err := nRows.Scan(&sr.ID, &sr.Layer, &sr.Type, &sr.Name, &sr.Data); err != nil {
					continue
				}
				if sr.Layer == "SYSTEM" && sr.Type == "file" {
					sr.Path = sr.Name
				}
				sr.Distance = 2.0 // no similarity — pure graph signal
				searchResults = append(searchResults, &sr)
				idSet[sr.ID] = true
			}
		}
	}

	// Rebuild full ID list after expansion
	allIDs := make([]uuid.UUID, len(searchResults))
	for i, sr := range searchResults {
		allIDs[i] = sr.ID
	}

	// 3. Batch enrich with activity data (handles mixed types)
	activity, err := d.BatchGetPackActivity(ctx, searchResults)
	if err != nil {
		activity = make(map[uuid.UUID]PackActivity)
	}

	// 4. Graph proximity to task (if task context available)
	var proximity map[uuid.UUID]float64
	if taskID != nil {
		proximity, err = d.GetTaskProximity(ctx, *taskID, allIDs)
		if err != nil {
			proximity = make(map[uuid.UUID]float64)
		}
	}
	if proximity == nil {
		proximity = make(map[uuid.UUID]float64)
	}

	// 5. Merge neighbor proximity scores into graph proximity
	for id, nScore := range neighborScores {
		if existing, ok := proximity[id]; ok {
			if nScore > existing {
				proximity[id] = nScore
			}
		} else {
			proximity[id] = nScore
		}
	}

	// 6. Build PackItems with all normalized signals
	items := make([]PackItem, 0, len(searchResults))
	for _, sr := range searchResults {
		fa := activity[sr.ID]
		item := PackItem{
			ID:             sr.ID,
			Name:           sr.Name,
			Path:           sr.Path,
			Layer:          sr.Layer,
			Type:           sr.Type,
			Summary:        extractSummary(sr),
			Similarity:     normalizeDistance(sr.Distance),
			Recency:        computeRecency(fa.LastModified),
			Frequency:      normalizeFrequency(fa.ModifyCount),
			GraphProximity: proximity[sr.ID],
		}
		item.Score = computePackScore(item, weights)
		items = append(items, item)
	}

	// 7. Sort by unified score (descending)
	sort.Slice(items, func(i, j int) bool { return items[i].Score > items[j].Score })

	// 8. Trim to profile limit
	if len(items) > limit {
		items = items[:limit]
	}

	// 9. Generate WhySelected for each item
	for i := range items {
		items[i].WhySelected = generateWhySelected(items[i])
	}

	// 10. Fetch constraints
	constraints, err := d.fetchPackConstraints(ctx)
	if err != nil {
		constraints = nil
	}

	return &ContextPack{
		Profile:     profile,
		Query:       query,
		Items:       items,
		Constraints: constraints,
		CreatedAt:   time.Now(),
	}, nil
}

// RenderForPrompt produces a human-readable text block for system prompts.
func (cp *ContextPack) RenderForPrompt() string {
	if cp == nil || len(cp.Items) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("CONTEXT PACK (%s-mode, %d results):\n", cp.Profile, len(cp.Items)))
	for _, item := range cp.Items {
		// CONTEXT nodes show as [CONTEXT.type] name; files show path
		var label string
		if item.Layer == "SYSTEM" && item.Type == "file" {
			label = item.Path
			if label == "" {
				label = item.Name
			}
		} else {
			label = fmt.Sprintf("[%s.%s] %s", item.Layer, item.Type, item.Name)
		}
		b.WriteString(fmt.Sprintf("  - %-50s score:%.2f  \"%s\"\n", label, item.Score, item.WhySelected))
		if item.Summary != "" {
			b.WriteString(fmt.Sprintf("    %s\n", item.Summary))
		}
	}

	if len(cp.Constraints) > 0 {
		b.WriteString("\nCONSTRAINTS:\n")
		for _, c := range cp.Constraints {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", c.Name, c.Text))
		}
	}

	b.WriteString("\n")
	return b.String()
}

// ToMap returns a structured map for JSON/MCP output.
func (cp *ContextPack) ToMap() map[string]any {
	items := make([]map[string]any, len(cp.Items))
	for i, item := range cp.Items {
		m := map[string]any{
			"id":              item.ID.String(),
			"name":            item.Name,
			"path":            item.Path,
			"layer":           item.Layer,
			"type":            item.Type,
			"score":           item.Score,
			"similarity":      item.Similarity,
			"recency":         item.Recency,
			"frequency":       item.Frequency,
			"graph_proximity": item.GraphProximity,
			"why_selected":    item.WhySelected,
		}
		if item.Summary != "" {
			m["summary"] = item.Summary
		}
		items[i] = m
	}

	result := map[string]any{
		"profile":    string(cp.Profile),
		"query":      cp.Query,
		"items":      items,
		"count":      len(cp.Items),
		"created_at": cp.CreatedAt.Format(time.RFC3339),
	}

	if len(cp.Constraints) > 0 {
		cList := make([]map[string]any, len(cp.Constraints))
		for i, c := range cp.Constraints {
			cList[i] = map[string]any{
				"id":   c.ID.String(),
				"name": c.Name,
				"text": c.Text,
			}
		}
		result["constraints"] = cList
	}

	return result
}
