package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Proposal represents a suggested improvement for human review.
type Proposal struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Reason      string `json:"reason"`       // why the system suggests this
	Source      string `json:"source"`        // what triggered the suggestion (pattern, gap, churn, etc.)
	Intent      string `json:"intent"`        // best matching intent
	Alignment   int    `json:"alignment_pct"` // 0-100% alignment with intents
}

// GenerateProposals analyzes the current graph state and proposes improvements.
// All proposals are queued as suggestions for human review.
func (d *Dash) GenerateProposals(ctx context.Context) ([]Proposal, error) {
	var proposals []Proposal

	// 1. Analyze high-churn files that lack test coverage or documentation tasks
	churnProposals, _ := d.proposeFromChurn(ctx)
	proposals = append(proposals, churnProposals...)

	// 2. Analyze co-editing patterns for missing dependency edges
	patternProposals, _ := d.proposeFromPatterns(ctx)
	proposals = append(proposals, patternProposals...)

	// 3. Check for stale active tasks
	staleProposals, _ := d.proposeFromStaleTasks(ctx)
	proposals = append(proposals, staleProposals...)

	// 4. Check mission success criteria gaps
	missionProposals, _ := d.proposeFromMissionGaps(ctx)
	proposals = append(proposals, missionProposals...)

	// Score and process each proposal
	var results []Proposal
	for _, p := range proposals {
		// Skip duplicates - check if task with same name already exists
		if existing, _ := d.GetNodeByName(ctx, LayerContext, "task", p.Name); existing != nil {
			continue
		}

		// Score alignment against intents
		matches, err := d.MatchTaskToIntents(ctx, p.Name, p.Description)
		if err != nil || len(matches) == 0 {
			p.Alignment = 0
			p.Intent = ""
		} else {
			// Normalize score to percentage (max observed score ~15-20)
			maxScore := 20
			pct := (matches[0].Score * 100) / maxScore
			if pct > 100 {
				pct = 100
			}
			p.Alignment = pct
			p.Intent = matches[0].IntentName
		}

		// Queue all proposals as suggestions for human review
		d.queueSuggestion(ctx, p)

		results = append(results, p)
	}

	return results, nil
}

// proposeFromChurn suggests tasks for high-churn files.
func (d *Dash) proposeFromChurn(ctx context.Context) ([]Proposal, error) {
	churn, err := d.DetectFileChurn(ctx, 5) // files modified 5+ times
	if err != nil {
		return nil, err
	}

	var proposals []Proposal
	for _, fc := range churn {
		// Skip plan files and non-source files
		if !isSourceFile(fc.FilePath) {
			continue
		}
		if fc.ModifyCount >= 10 && fc.SessionCount >= 3 {
			proposals = append(proposals, Proposal{
				Name:        fmt.Sprintf("refactor-%s", baseFileName(fc.FilePath)),
				Description: fmt.Sprintf("Filen %s har modifierats %d gånger över %d sessioner. Överväg refaktorering för att minska churn - bryt ut delar eller förenkla.", fc.FilePath, fc.ModifyCount, fc.SessionCount),
				Reason:      fmt.Sprintf("Hög churn: %d ändringar, %d sessioner", fc.ModifyCount, fc.SessionCount),
				Source:      "file-churn",
			})
		}
	}

	// Limit to top 2
	if len(proposals) > 2 {
		proposals = proposals[:2]
	}
	return proposals, nil
}

// proposeFromPatterns suggests tasks based on co-editing patterns without edges.
func (d *Dash) proposeFromPatterns(ctx context.Context) ([]Proposal, error) {
	patterns, err := d.DetectCoEditingPatterns(ctx, 4) // files edited together 4+ times
	if err != nil {
		return nil, err
	}

	var proposals []Proposal
	for _, p := range patterns {
		if len(p.Files) < 2 || p.Frequency < 4 {
			continue
		}

		// Check if a depends_on edge exists between the file nodes
		f1, _ := d.GetNodeByName(ctx, LayerSystem, "file", p.Files[0])
		f2, _ := d.GetNodeByName(ctx, LayerSystem, "file", p.Files[1])
		if f1 != nil && f2 != nil {
			has, _ := d.hasEdge(ctx, f1.ID, f2.ID, RelationDependsOn)
			hasReverse, _ := d.hasEdge(ctx, f2.ID, f1.ID, RelationDependsOn)
			if !has && !hasReverse {
				proposals = append(proposals, Proposal{
					Name:        fmt.Sprintf("link-%s-%s", baseFileName(p.Files[0]), baseFileName(p.Files[1])),
					Description: fmt.Sprintf("Filerna %s och %s ändras alltid tillsammans (%dx) men saknar en depends_on-edge i grafen. Skapa relationen för bättre systemförståelse.", p.Files[0], p.Files[1], p.Frequency),
					Reason:      fmt.Sprintf("Co-edit pattern utan edge: %dx tillsammans", p.Frequency),
					Source:      "co-editing-pattern",
				})
			}
		}
	}

	if len(proposals) > 2 {
		proposals = proposals[:2]
	}
	return proposals, nil
}

// proposeFromStaleTasks suggests action on tasks that have been active too long.
func (d *Dash) proposeFromStaleTasks(ctx context.Context) ([]Proposal, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT name, data->>'description', updated_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'task'
		  AND deleted_at IS NULL
		  AND COALESCE(data->>'status', 'pending') = 'active'
		  AND updated_at < NOW() - INTERVAL '7 days'
		ORDER BY updated_at
		LIMIT 3`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var proposals []Proposal
	for rows.Next() {
		var name string
		var desc *string
		var updatedAt time.Time
		if err := rows.Scan(&name, &desc, &updatedAt); err != nil {
			continue
		}
		days := int(time.Since(updatedAt).Hours() / 24)
		proposals = append(proposals, Proposal{
			Name:        fmt.Sprintf("review-stale-%s", name),
			Description: fmt.Sprintf("Task '%s' har varit aktiv i %d dagar utan uppdatering. Bör den slutföras, brytas ner, eller avbrytas?", name, days),
			Reason:      fmt.Sprintf("Aktiv i %d dagar utan uppdatering", days),
			Source:      "stale-task",
		})
	}
	return proposals, rows.Err()
}

// proposeFromMissionGaps checks mission success criteria and suggests tasks to close gaps.
func (d *Dash) proposeFromMissionGaps(ctx context.Context) ([]Proposal, error) {
	mission, err := d.GetNodeByName(ctx, LayerContext, "mission", "dash-mission")
	if err != nil {
		return nil, err
	}

	var mData map[string]any
	if err := json.Unmarshal(mission.Data, &mData); err != nil {
		return nil, err
	}

	criteria, ok := mData["success_criteria"].([]any)
	if !ok {
		return nil, nil
	}

	// Check each criterion against existing completed tasks
	var proposals []Proposal
	for _, c := range criteria {
		criterion, ok := c.(string)
		if !ok {
			continue
		}

		// Check if there's a completed task that addresses this criterion
		matches, _ := d.MatchTaskToIntents(ctx, criterion, "")
		hasTask := false

		// Look for a task with relevant keywords
		rows, err := d.db.QueryContext(ctx, `
			SELECT COUNT(*) FROM nodes
			WHERE layer = 'CONTEXT' AND type = 'task'
			  AND deleted_at IS NULL
			  AND COALESCE(data->>'status', 'pending') IN ('active', 'pending')
			  AND (name ILIKE '%' || $1 || '%' OR data->>'description' ILIKE '%' || $1 || '%')`,
			extractKeyword(criterion))
		if err == nil {
			var count int
			if rows.Next() {
				rows.Scan(&count)
				hasTask = count > 0
			}
			rows.Close()
		}

		if !hasTask && len(matches) > 0 {
			proposals = append(proposals, Proposal{
				Name:        fmt.Sprintf("mission-gap-%s", slugify(criterion, 40)),
				Description: fmt.Sprintf("Mission-kriterium saknar aktiv task: '%s'. Skapa en task som adresserar detta.", criterion),
				Reason:      fmt.Sprintf("Mission success criteria gap"),
				Source:      "mission-gap",
			})
		}
	}

	if len(proposals) > 2 {
		proposals = proposals[:2]
	}
	return proposals, nil
}

// queueSuggestion stores a low-confidence suggestion for human review.
func (d *Dash) queueSuggestion(ctx context.Context, p Proposal) {
	data := map[string]any{
		"name":          p.Name,
		"description":   p.Description,
		"reason":        p.Reason,
		"source":        p.Source,
		"intent":        p.Intent,
		"alignment_pct": p.Alignment,
		"status":        "pending_review",
		"created_at":    time.Now().Format(time.RFC3339),
	}
	dataJSON, _ := json.Marshal(data)

	node := &Node{
		Layer: LayerContext,
		Type:  "suggestion",
		Name:  p.Name,
		Data:  dataJSON,
	}
	d.CreateNode(ctx, node)
	go d.EmbedNode(context.Background(), node)
}

// Helper functions

func isSourceFile(path string) bool {
	for _, ext := range []string{".go", ".sql", ".sh", ".md", ".json", ".yaml", ".toml"} {
		if len(path) > len(ext) && path[len(path)-len(ext):] == ext {
			return true
		}
	}
	return false
}

func baseFileName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			name := path[i+1:]
			// Strip extension
			for j := len(name) - 1; j >= 0; j-- {
				if name[j] == '.' {
					return name[:j]
				}
			}
			return name
		}
	}
	return path
}

func extractKeyword(text string) string {
	// Extract the most significant word for ILIKE matching
	words := significantWords(text)
	best := ""
	for w := range words {
		if len(w) > len(best) {
			best = w
		}
	}
	if len(best) > 20 {
		best = best[:20]
	}
	return best
}

func slugify(text string, maxLen int) string {
	result := make([]byte, 0, maxLen)
	for _, c := range []byte(text) {
		if len(result) >= maxLen {
			break
		}
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			result = append(result, c)
		} else if c >= 'A' && c <= 'Z' {
			result = append(result, c+32)
		} else if c == ' ' || c == '-' || c == '_' {
			if len(result) > 0 && result[len(result)-1] != '-' {
				result = append(result, '-')
			}
		}
	}
	return string(result)
}
