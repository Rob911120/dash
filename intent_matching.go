package dash

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const queryActiveIntents = `
	SELECT id, name, data
	FROM nodes
	WHERE layer = 'CONTEXT' AND type = 'intent'
	  AND deleted_at IS NULL
	  AND COALESCE(data->>'status', 'active') = 'active'
	ORDER BY created_at`

// IntentMatch represents a scored match between a task and an intent.
type IntentMatch struct {
	IntentID   uuid.UUID
	IntentName string
	Score      int // higher = better match
}

// MatchTaskToIntents finds the best matching intent(s) for a task based on text similarity.
// Returns matches sorted by score (best first). Only returns matches with score > 0.
func (d *Dash) MatchTaskToIntents(ctx context.Context, taskName, taskDescription string) ([]IntentMatch, error) {
	rows, err := d.db.QueryContext(ctx, queryActiveIntents)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	text := strings.ToLower(taskName + " " + taskDescription)
	var matches []IntentMatch

	for rows.Next() {
		var id uuid.UUID
		var name string
		var data json.RawMessage

		if err := rows.Scan(&id, &name, &data); err != nil {
			continue
		}

		// Extract intent description
		var intentData map[string]any
		if err := json.Unmarshal(data, &intentData); err != nil {
			intentData = map[string]any{}
		}
		desc, _ := intentData["description"].(string)
		intentText := strings.ToLower(name + " " + desc)

		score := scoreTextOverlap(text, intentText)
		if score > 0 {
			matches = append(matches, IntentMatch{
				IntentID:   id,
				IntentName: name,
				Score:      score,
			})
		}
	}

	// Sort by score descending
	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].Score > matches[i].Score {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}

	return matches, rows.Err()
}

// AutoLinkTaskToIntent matches a task to its best intent and creates an implements edge.
// Returns the matched intent name, or empty string if no match found.
func (d *Dash) AutoLinkTaskToIntent(ctx context.Context, taskID uuid.UUID, taskName, taskDescription string) (string, error) {
	matches, err := d.MatchTaskToIntents(ctx, taskName, taskDescription)
	if err != nil || len(matches) == 0 {
		return "", err
	}

	best := matches[0]

	// Check if edge already exists
	existing, _ := d.hasEdge(ctx, taskID, best.IntentID, RelationImplements)
	if existing {
		return best.IntentName, nil
	}

	err = d.CreateEdge(ctx, &Edge{
		SourceID: taskID,
		TargetID: best.IntentID,
		Relation: RelationImplements,
	})
	if err != nil {
		return "", err
	}

	// Log the auto-linking as a triggered event
	d.CreateEdgeEvent(ctx, &EdgeEvent{
		SourceID:   taskID,
		TargetID:   best.IntentID,
		Relation:   EventRelationTriggered,
		Success:    true,
		OccurredAt: time.Now(),
	})

	return best.IntentName, nil
}

// LinkTaskDependency creates a depends_on edge between two tasks.
func (d *Dash) LinkTaskDependency(ctx context.Context, taskID, dependsOnID uuid.UUID) error {
	existing, _ := d.hasEdge(ctx, taskID, dependsOnID, RelationDependsOn)
	if existing {
		return nil
	}
	return d.CreateEdge(ctx, &Edge{
		SourceID: taskID,
		TargetID: dependsOnID,
		Relation: RelationDependsOn,
	})
}

// GetTasksWithDeps returns active/pending tasks with their dependency info.
type TaskWithDeps struct {
	Node       *Node     `json:"node"`
	Status     string    `json:"status"`
	Intent     string    `json:"intent,omitempty"`
	BlockedBy  []string  `json:"blocked_by,omitempty"`  // task names that block this
	Blocks     []string  `json:"blocks,omitempty"`       // task names this blocks
	IsBlocked  bool      `json:"is_blocked"`
}

const queryTaskDeps = `
	SELECT
		t.id, t.name, t.data,
		COALESCE(t.data->>'status', 'pending') as status,
		-- intent name via implements edge
		(SELECT n.name FROM edges e JOIN nodes n ON n.id = e.target_id AND n.deleted_at IS NULL
		 WHERE e.source_id = t.id AND e.relation = 'implements' AND e.deprecated_at IS NULL
		 AND n.type = 'intent' LIMIT 1) as intent_name,
		-- blocked_by: tasks this depends on that aren't completed
		(SELECT ARRAY_AGG(n.name) FROM edges e JOIN nodes n ON n.id = e.target_id AND n.deleted_at IS NULL
		 WHERE e.source_id = t.id AND e.relation = 'depends_on' AND e.deprecated_at IS NULL
		 AND n.type = 'task' AND COALESCE(n.data->>'status', 'pending') != 'completed') as blocked_by,
		-- blocks: tasks that depend on this
		(SELECT ARRAY_AGG(n.name) FROM edges e JOIN nodes n ON n.id = e.source_id AND n.deleted_at IS NULL
		 WHERE e.target_id = t.id AND e.relation = 'depends_on' AND e.deprecated_at IS NULL
		 AND n.type = 'task' AND COALESCE(n.data->>'status', 'pending') != 'completed') as blocks
	FROM nodes t
	WHERE t.layer = 'CONTEXT' AND t.type = 'task'
	  AND t.deleted_at IS NULL
	  AND COALESCE(t.data->>'status', 'pending') IN ('pending', 'active')
	ORDER BY
		CASE COALESCE(t.data->>'status', 'pending') WHEN 'active' THEN 0 ELSE 1 END,
		t.created_at`

// GetActiveTasksWithDeps returns all active/pending tasks enriched with dependency info.
func (d *Dash) GetActiveTasksWithDeps(ctx context.Context) ([]TaskWithDeps, error) {
	rows, err := d.db.QueryContext(ctx, queryTaskDeps)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []TaskWithDeps
	for rows.Next() {
		var (
			id         uuid.UUID
			name       string
			data       json.RawMessage
			status     string
			intentName *string
			blockedBy  pq.StringArray
			blocks     pq.StringArray
		)

		if err := rows.Scan(&id, &name, &data, &status, &intentName, &blockedBy, &blocks); err != nil {
			continue
		}

		t := TaskWithDeps{
			Node:      &Node{ID: id, Name: name, Data: data},
			Status:    status,
			BlockedBy: []string(blockedBy),
			Blocks:    []string(blocks),
			IsBlocked: len(blockedBy) > 0,
		}
		if intentName != nil {
			t.Intent = *intentName
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// hasEdge checks if a specific edge already exists.
func (d *Dash) hasEdge(ctx context.Context, sourceID, targetID uuid.UUID, relation Relation) (bool, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM edges WHERE source_id = $1 AND target_id = $2 AND relation = $3 AND deprecated_at IS NULL`,
		sourceID, targetID, relation,
	).Scan(&count)
	return count > 0, err
}

// scoreTextOverlap scores how well two texts match based on shared significant words.
func scoreTextOverlap(text, intentText string) int {
	// Extract significant words (skip short/common words)
	textWords := significantWords(text)
	intentWords := significantWords(intentText)

	score := 0
	for w := range textWords {
		if intentWords[w] {
			score += 2
		}
	}

	// Bonus for direct substring match
	if strings.Contains(intentText, "automation") && strings.Contains(text, "automat") {
		score += 3
	}
	if strings.Contains(intentText, "flow") && (strings.Contains(text, "tui") || strings.Contains(text, "ui") || strings.Contains(text, "gränssnitt")) {
		score += 3
	}
	if strings.Contains(intentText, "förbättr") && (strings.Contains(text, "mönster") || strings.Contains(text, "pattern") || strings.Contains(text, "själv")) {
		score += 3
	}
	if strings.Contains(intentText, "enkel") && (strings.Contains(text, "städa") || strings.Contains(text, "refactor") || strings.Contains(text, "förenk")) {
		score += 3
	}

	return score
}

// significantWords returns words with 4+ characters, lowercased.
func significantWords(text string) map[string]bool {
	stopWords := map[string]bool{
		"alla": true, "inte": true, "från": true, "vara": true, "till": true,
		"över": true, "under": true, "efter": true, "this": true, "that": true,
		"with": true, "from": true, "have": true, "will": true, "been": true,
		"them": true, "their": true, "which": true, "each": true, "should": true,
		"shall": true, "must": true, "some": true, "when": true,
	}

	words := map[string]bool{}
	for _, w := range strings.Fields(text) {
		w = strings.Trim(w, ".,;:!?()[]{}\"'")
		if len(w) >= 4 && !stopWords[w] {
			words[w] = true
		}
	}
	return words
}

// HierarchyTree represents the mission → intents → tasks tree structure.
type HierarchyTree struct {
	Mission  *Node          `json:"mission,omitempty"`
	Intents  []IntentBranch `json:"intents"`
	Unlinked []TaskWithDeps `json:"unlinked,omitempty"`
}

// IntentBranch represents an intent with its associated tasks.
type IntentBranch struct {
	Intent *Node          `json:"intent"`
	Status string         `json:"status"`
	Tasks  []TaskWithDeps `json:"tasks,omitempty"`
}

const queryAllIntentsForTree = `
	SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
	FROM nodes
	WHERE layer = 'CONTEXT' AND type = 'intent'
	  AND deleted_at IS NULL
	ORDER BY created_at`

// GetHierarchyTree builds the mission → intents → tasks tree.
func (d *Dash) GetHierarchyTree(ctx context.Context) (*HierarchyTree, error) {
	tree := &HierarchyTree{}

	// Get mission
	if node, err := d.querySingleNode(ctx, queryGetMission, 2*time.Second); err == nil {
		tree.Mission = node
	}

	// Get all intents
	rows, err := d.db.QueryContext(ctx, queryAllIntentsForTree)
	if err != nil {
		return tree, err
	}
	intents, err := scanNodes(rows)
	rows.Close()
	if err != nil {
		return tree, err
	}

	// Get all active tasks with deps
	tasks, err := d.GetActiveTasksWithDeps(ctx)
	if err != nil {
		return tree, err
	}

	// Build intent branches
	intentMap := make(map[string]*IntentBranch) // intent name → branch
	for _, intent := range intents {
		var data map[string]any
		if intent.Data != nil {
			json.Unmarshal(intent.Data, &data)
		}
		status, _ := data["status"].(string)
		if status == "" {
			status = "active"
		}
		branch := IntentBranch{
			Intent: intent,
			Status: status,
		}
		tree.Intents = append(tree.Intents, branch)
		intentMap[intent.Name] = &tree.Intents[len(tree.Intents)-1]
	}

	// Assign tasks to intents
	for _, t := range tasks {
		if t.Intent != "" {
			if branch, ok := intentMap[t.Intent]; ok {
				branch.Tasks = append(branch.Tasks, t)
				continue
			}
		}
		tree.Unlinked = append(tree.Unlinked, t)
	}

	return tree, nil
}

// CreateTaskWithAutoLink creates a CONTEXT.task node and auto-links it to the best matching intent.
// Returns the created node and the matched intent name.
func (d *Dash) CreateTaskWithAutoLink(ctx context.Context, name, description, status string) (*Node, string, error) {
	if status == "" {
		status = "pending"
	}

	data := map[string]any{
		"description": description,
		"status":      status,
		"created_by":  "agent",
		"created_at":  time.Now().Format(time.RFC3339),
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, "", err
	}

	node := &Node{
		Layer: LayerContext,
		Type:  "task",
		Name:  name,
		Data:  dataJSON,
	}

	if err := d.CreateNode(ctx, node); err != nil {
		return nil, "", err
	}

	go d.EmbedNode(context.Background(), node)

	// Auto-link to best matching intent
	intentName, _ := d.AutoLinkTaskToIntent(ctx, node.ID, name, description)

	return node, intentName, nil
}
