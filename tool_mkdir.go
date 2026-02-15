package dash

import (
	"context"
	"fmt"
	"os"
)

func defMkdir() *ToolDef {
	return &ToolDef{
		Name:        "mkdir",
		Description: "Create a directory and any necessary parent directories.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"path"},
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Absolute path of the directory to create"},
			},
		},
		Tags: []string{"write", "fs"},
		Fn:   toolMkdir,
	}
}

func toolMkdir(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	validated, err := d.fileConfig.ValidatePath(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	// Check if it already exists
	if info, err := os.Stat(validated); err == nil {
		if info.IsDir() {
			return map[string]any{"path": validated, "created": false, "exists": true}, nil
		}
		return nil, fmt.Errorf("path exists but is not a directory")
	}

	if err := os.MkdirAll(validated, 0755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	return map[string]any{"path": validated, "created": true}, nil
}
