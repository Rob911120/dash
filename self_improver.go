package dash

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SelfImprover analyzes patterns and suggestions to improve the system.
type SelfImprover struct {
	dash *Dash
}

// NewSelfImprover creates a new self-improvement engine.
func NewSelfImprover(d *Dash) *SelfImprover {
	return &SelfImprover{dash: d}
}

// Improvement represents a potential system improvement.
type Improvement struct {
	Type        string // "tool_add", "tool_modify", "prompt_update", "process_change"
	Component   string // file, tool, or system component
	Description string
	Priority    string // "low", "medium", "high", "critical"
	Rationale   string
	Evidence    []string // supporting data
}

// AnalyzeAndSuggest analyzes patterns and suggestions to generate improvements.
func (s *SelfImprover) AnalyzeAndSuggest(ctx context.Context) ([]Improvement, error) {
	var improvements []Improvement

	// 1. Analyze tool usage patterns
	toolImprovements, err := s.analyzeToolUsage(ctx)
	if err != nil {
		fmt.Printf("Warning: tool analysis failed: %v\n", err)
	}
	improvements = append(improvements, toolImprovements...)

	// 2. Analyze code patterns
	patternImprovements, err := s.analyzeCodePatterns(ctx)
	if err != nil {
		fmt.Printf("Warning: pattern analysis failed: %v\n", err)
	}
	improvements = append(improvements, patternImprovements...)

	// 3. Check pending suggestions
	suggestionImprovements, err := s.analyzeSuggestions(ctx)
	if err != nil {
		fmt.Printf("Warning: suggestion analysis failed: %v\n", err)
	}
	improvements = append(improvements, suggestionImprovements...)

	// 4. Analyze recent failures
	failureImprovements, err := s.analyzeFailures(ctx)
	if err != nil {
		fmt.Printf("Warning: failure analysis failed: %v\n", err)
	}
	improvements = append(improvements, failureImprovements...)

	return improvements, nil
}

// analyzeToolUsage finds tools that could be improved based on usage.
func (s *SelfImprover) analyzeToolUsage(ctx context.Context) ([]Improvement, error) {
	var improvements []Improvement

	// Find most used tools
	rows, err := s.dash.db.QueryContext(ctx, `
		SELECT e.relation, COUNT(*) as cnt
		FROM edge_events e
		WHERE e.occurred_at > NOW() - INTERVAL '7 days'
		GROUP BY e.relation
		ORDER BY cnt DESC
		LIMIT 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	toolUsage := make(map[string]int)
	for rows.Next() {
		var relation string
		var count int
		if err := rows.Scan(&relation, &count); err != nil {
			continue
		}
		toolUsage[relation] = count
	}

	// Find unused tools (in code but not used in last 30 days)
	rows2, err := s.dash.db.QueryContext(ctx, `
		SELECT name FROM nodes
		WHERE layer = 'AUTOMATION' AND type = 'tool'
		  AND deleted_at IS NULL
		  AND created_at < NOW() - INTERVAL '30 days'`)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()

	unusedTools := make(map[string]bool)
	for rows2.Next() {
		var name string
		if err := rows2.Scan(&name); err != nil {
			continue
		}
		unusedTools[name] = true
	}

	// If tool is unused for 90+ days, suggest deprecation
	for toolName := range unusedTools {
		improvements = append(improvements, Improvement{
			Type:        "tool_deprecate",
			Component:   "tool:" + toolName,
			Description: fmt.Sprintf("Tool '%s' has not been used in 30+ days", toolName),
			Priority:    "low",
			Rationale:   "Reduce maintenance by removing unused tools",
			Evidence:    []string{"Not used in last 30 days"},
		})
	}

	return improvements, nil
}

// analyzeCodePatterns finds patterns in code that suggest improvements.
func (s *SelfImprover) analyzeCodePatterns(ctx context.Context) ([]Improvement, error) {
	var improvements []Improvement

	// Get co-editing patterns
	patterns, err := s.dash.DetectCoEditingPatterns(ctx, 3)
	if err != nil {
		return nil, err
	}

	// Find patterns that suggest tool consolidation
	// (e.g., if two tools are always used together, maybe they should be combined)
	for _, p := range patterns {
		if len(p.Files) >= 2 && p.Frequency > 5 {
			improvements = append(improvements, Improvement{
				Type:        "process_change",
				Component:   "workflow",
				Description: fmt.Sprintf("Files %v are frequently edited together (%d times)", p.Files, p.Frequency),
				Priority:    "low",
				Rationale:   "Consider creating a macro or combined tool for these operations",
				Evidence:    []string{fmt.Sprintf("Co-edited %d times across %d sessions", p.Frequency, len(p.Sessions))},
			})
		}
	}

	return improvements, nil
}

// analyzeSuggestions processes pending suggestions.
func (s *SelfImprover) analyzeSuggestions(ctx context.Context) ([]Improvement, error) {
	var improvements []Improvement

	rows, err := s.dash.db.QueryContext(ctx, `
		SELECT id, name, data->>'title' as title, data->>'description' as desc,
		       data->>'priority' as priority, data->>'rationale' as rationale
		FROM nodes
		WHERE layer = 'AUTOMATION' AND type = 'suggestion'
		  AND deleted_at IS NULL
		  AND data->>'status' = 'pending'
		ORDER BY 
		  CASE data->>'priority'
		    WHEN 'critical' THEN 1
		    WHEN 'high' THEN 2
		    WHEN 'medium' THEN 3
		    ELSE 4
		  END
		LIMIT 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, title, desc, priority, rationale string
		if err := rows.Scan(&id, &title, &desc, &priority, &rationale); err != nil {
			continue
		}
		improvements = append(improvements, Improvement{
			Type:        "suggestion",
			Component:   id,
			Description: desc,
			Priority:    priority,
			Rationale:   rationale,
		})
	}

	return improvements, nil
}

// analyzeFailures looks for recurring failures that could be addressed.
func (s *SelfImprover) analyzeFailures(ctx context.Context) ([]Improvement, error) {
	var improvements []Improvement

	// Find tool failures in recent sessions
	rows, err := s.dash.db.QueryContext(ctx, `
		SELECT e.data->>'tool_name' as tool, COUNT(*) as failures
		FROM edge_events e
		WHERE e.relation = 'tool_error'
		  AND e.occurred_at > NOW() - INTERVAL '7 days'
		GROUP BY e.data->>'tool_name'
		HAVING COUNT(*) > 3
		ORDER BY failures DESC
		LIMIT 5`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var tool string
		var failures int
		if err := rows.Scan(&tool, &failures); err != nil {
			continue
		}
		improvements = append(improvements, Improvement{
			Type:        "tool_fix",
			Component:   "tool:" + tool,
			Description: fmt.Sprintf("Tool '%s' has failed %d times in the last 7 days", tool, failures),
			Priority:    "high",
			Rationale:   "High failure rate indicates usability issues",
			Evidence:    []string{fmt.Sprintf("%d failures", failures)},
		})
	}

	return improvements, nil
}

// ApplyImprovement applies an improvement to the system.
func (s *SelfImprover) ApplyImprovement(ctx context.Context, imp Improvement) error {
	switch imp.Type {
	case "tool_deprecate":
		// Mark tool as deprecated in DB
		_, err := s.dash.db.ExecContext(ctx, `
			UPDATE nodes
			SET data = data || '{"status": "deprecated"}'::jsonb,
			    updated_at = $1
			WHERE layer = 'AUTOMATION' AND type = 'tool' AND name = $2`,
			time.Now(), imp.Component)
		return err

	case "suggestion":
		// Mark suggestion as processed
		_, err := s.dash.db.ExecContext(ctx, `
			UPDATE nodes
			SET data = data || '{"status": "applied", "applied_at": $1}'::jsonb,
			    updated_at = $1
			WHERE id = $2`,
			time.Now(), imp.Component)
		return err

	default:
		// Log the improvement for manual review
		fmt.Printf("Improvement requires manual action: %s - %s\n", imp.Type, imp.Description)
		return nil
	}
}

// RunSelfImprovementLoop runs the self-improvement analysis and optionally applies suggestions.
func (s *SelfImprover) RunSelfImprovementLoop(ctx context.Context, autoApply bool) error {
	improvements, err := s.AnalyzeAndSuggest(ctx)
	if err != nil {
		return fmt.Errorf("analysis failed: %w", err)
	}

	fmt.Printf("SelfImprover: found %d potential improvements\n", len(improvements))

	// Store improvements as nodes
	for _, imp := range improvements {
		node := &Node{
			ID:   uuid.New(),
			Layer: "AUTOMATION",
			Type: "improvement",
			Name: fmt.Sprintf("improvement-%s", imp.Type),
			Data: mapToJSON(map[string]any{
				"type":        imp.Type,
				"component":   imp.Component,
				"description": imp.Description,
				"priority":    imp.Priority,
				"rationale":   imp.Rationale,
				"evidence":    imp.Evidence,
				"status":      "identified",
				"identified":  time.Now().Format(time.RFC3339),
			}),
		}

		if err := s.dash.CreateNode(ctx, node); err != nil {
			fmt.Printf("Warning: failed to create improvement node: %v\n", err)
			continue
		}

		// Auto-apply if enabled and priority is high+
		if autoApply && (imp.Priority == "high" || imp.Priority == "critical") {
			if err := s.ApplyImprovement(ctx, imp); err != nil {
				fmt.Printf("Warning: failed to apply improvement: %v\n", err)
			} else {
				fmt.Printf("Applied improvement: %s\n", imp.Description)
			}
		}
	}

	return nil
}

// PeriodicSelfImprovement runs self-improvement on a schedule (called by cron/watch).
func (s *SelfImprover) PeriodicSelfImprovement(ctx context.Context) error {
	// Run every 24 hours
	return s.RunSelfImprovementLoop(ctx, false) // Don't auto-apply, just identify
}
