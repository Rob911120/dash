package dash

import "context"

func defWorkingSet() *ToolDef {
	return &ToolDef{
		Name:        "working_set",
		Description: "Get the current working set: mission, context frame, active tasks, constraints, recent insights/decisions.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Tags: []string{"read"},
		Fn:   toolWorkingSet,
	}
}

func toolWorkingSet(ctx context.Context, d *Dash, _ map[string]any) (any, error) {
	return d.AssembleWorkingSet(ctx)
}
