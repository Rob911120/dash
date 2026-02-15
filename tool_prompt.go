package dash

import (
	"context"
	"fmt"
)

func defPrompt() *ToolDef {
	return &ToolDef{
		Name:        "prompt",
		Description: "Get an assembled prompt for a named profile. Returns the full system prompt text with dynamic context.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"profile"},
			"properties": map[string]any{
				"profile":   map[string]any{"type": "string", "description": "Profile name (e.g. 'default', 'task', 'planner', 'suggestion', 'execution')"},
				"task_name": map[string]any{"type": "string", "description": "Task name (for 'task' profile)"},
				"sugg_name": map[string]any{"type": "string", "description": "Suggestion name (for 'suggestion' profile)"},
				"plan_name": map[string]any{"type": "string", "description": "Plan name (for 'execution' profile)"},
				"refresh":   map[string]any{"type": "boolean", "description": "Force refresh (skip cache)"},
			},
		},
		Tags: []string{"read"},
		Fn:   toolPrompt,
	}
}

func toolPrompt(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	profileName, _ := args["profile"].(string)
	if profileName == "" {
		return nil, fmt.Errorf("profile is required")
	}

	opts := PromptOptions{
		TaskName: argString(args, "task_name"),
		SuggName: argString(args, "sugg_name"),
		PlanName: argString(args, "plan_name"),
	}
	if refresh, ok := args["refresh"].(bool); ok {
		opts.ForceRefresh = refresh
	}

	text, err := d.GetPrompt(ctx, profileName, opts)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"profile": profileName,
		"text":    text,
		"cached":  !opts.ForceRefresh,
	}, nil
}

func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}
