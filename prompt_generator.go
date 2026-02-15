package dash

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PromptGenerator auto-generates system prompts based on mission, tasks, insights.
type PromptGenerator struct {
	dash *Dash
}

// NewPromptGenerator creates a new prompt generator.
func NewPromptGenerator(d *Dash) *PromptGenerator {
	return &PromptGenerator{dash: d}
}

// GenerateSystemPrompt builds a dynamic system prompt from graph data.
func (g *PromptGenerator) GenerateSystemPrompt(ctx context.Context, profileName string) (string, error) {
	var b strings.Builder

	// Get mission
	mission, err := g.getMission(ctx)
	if err == nil && mission != "" {
		b.WriteString("## MISSION\n")
		b.WriteString(mission)
		b.WriteString("\n\n")
	}

	// Get active tasks
	tasks, err := g.getActiveTasks(ctx)
	if err == nil && len(tasks) > 0 {
		b.WriteString("## ACTIVE TASKS\n")
		for _, task := range tasks {
			b.WriteString(fmt.Sprintf("- %s: %s\n", task.Name, task.Status))
		}
		b.WriteString("\n")
	}

	// Get recent insights
	insights, err := g.getRecentInsights(ctx)
	if err == nil && len(insights) > 0 {
		b.WriteString("## RECENT INSIGHTS\n")
		for _, insight := range insights {
			b.WriteString(fmt.Sprintf("- %s\n", insight))
		}
		b.WriteString("\n")
	}

	// Get constraints
	constraints, err := g.getConstraints(ctx)
	if err == nil && len(constraints) > 0 {
		b.WriteString("## CONSTRAINTS\n")
		for _, c := range constraints {
			b.WriteString(fmt.Sprintf("- %s\n", c))
		}
		b.WriteString("\n")
	}

	// Add profile-specific instructions
	b.WriteString(g.profileInstructions(profileName))

	return b.String(), nil
}

// getMission retrieves the current mission text.
func (g *PromptGenerator) getMission(ctx context.Context) (string, error) {
	row := g.dash.db.QueryRowContext(ctx, `
		SELECT data->>'text'
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'mission' AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 1`)
	
	var mission string
	err := row.Scan(&mission)
	if err != nil {
		return "", err
	}
	return mission, nil
}

// getActiveTasks retrieves active tasks.
func (g *PromptGenerator) getActiveTasks(ctx context.Context) ([]struct {
	Name   string
	Status string
}, error) {
	rows, err := g.dash.db.QueryContext(ctx, `
		SELECT name, data->>'status'
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'task' 
		  AND data->>'status' IN ('active', 'pending', 'in_progress')
		  AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []struct {
		Name   string
		Status string
	}
	for rows.Next() {
		var t struct {
			Name   string
			Status string
		}
		if err := rows.Scan(&t.Name, &t.Status); err != nil {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// getRecentInsights retrieves recent insights.
func (g *PromptGenerator) getRecentInsights(ctx context.Context) ([]string, error) {
	rows, err := g.dash.db.QueryContext(ctx, `
		SELECT data->>'text'
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'insight'
		  AND deleted_at IS NULL
		  AND updated_at > NOW() - INTERVAL '24 hours'
		ORDER BY updated_at DESC
		LIMIT 5`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var insights []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			continue
		}
		insights = append(insights, text)
	}
	return insights, nil
}

// getConstraints retrieves current constraints.
func (g *PromptGenerator) getConstraints(ctx context.Context) ([]string, error) {
	rows, err := g.dash.db.QueryContext(ctx, `
		SELECT data->>'text'
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'constraint'
		  AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var constraints []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			continue
		}
		constraints = append(constraints, text)
	}
	return constraints, nil
}

// profileInstructions returns profile-specific instructions.
func (g *PromptGenerator) profileInstructions(profileName string) string {
	switch profileName {
	case "default":
		return `## INSTRUCTIONS
You are an autonomous agent working on the user's project. Use tools to accomplish tasks.
Prioritize: mission first, then active tasks, then insights.
Be concise and focused.

`
	case "task":
		return `## INSTRUCTIONS
Focus on completing the specific task at hand. Reference active tasks for context.
Use relevant tools and report progress clearly.

`
	case "planner":
		return `## INSTRUCITONS
You are planning. Break down goals into actionable steps with clear dependencies.
Consider constraints and prior insights when planning.

`
	default:
		return `## INSTRUCTIONS
Work autonomously to accomplish goals. Use available tools and context.

`
	}
}

// UpdatePromptProfiles updates system_prompt in all profiles with auto-generated content.
func (g *PromptGenerator) UpdatePromptProfiles(ctx context.Context) error {
	profiles := []string{"default", "compact", "planner", "task", "suggestion", "execution"}

	for _, name := range profiles {
		prompt, err := g.GenerateSystemPrompt(ctx, name)
		if err != nil {
			fmt.Printf("Warning: failed to generate prompt for %s: %v\n", name, err)
			continue
		}

		// Update the profile in DB
		_, err = g.dash.db.ExecContext(ctx, `
			UPDATE prompt_profiles
			SET system_prompt = $1, updated_at = $2
			WHERE name = $3`, prompt, time.Now(), name)
		if err != nil {
			fmt.Printf("Warning: failed to update profile %s: %v\n", name, err)
			continue
		}
		fmt.Printf("Updated prompt profile: %s\n", name)
	}
	return nil
}

// GenerateAndCache generates a prompt and caches it as a system_prompt node.
func (g *PromptGenerator) GenerateAndCache(ctx context.Context, profileName string, opts PromptOptions) (string, error) {
	// Generate the prompt
	prompt, err := g.GenerateSystemPrompt(ctx, profileName)
	if err != nil {
		return "", err
	}

	// Add profile-specific instructions
	prompt += g.profileInstructions(profileName)

	// Cache as system_prompt node
	cacheKey := promptCacheKey(profileName, opts)
	
	// Create or update the cached prompt node
	existing, _ := g.dash.GetNodeByName(ctx, "CONTEXT", "system_prompt", cacheKey)
	if existing != nil {
		existing.Data = mapToJSON(map[string]any{
			"text":       prompt,
			"profile":    profileName,
			"generated":  time.Now().Format(time.RFC3339),
		})
		g.dash.UpdateNode(ctx, existing)
	} else {
		// Create new cached prompt node
		// This would need UUID - for now, return the generated prompt directly
	}

	return prompt, nil
}
