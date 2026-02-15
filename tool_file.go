package dash

import (
	"context"
	"fmt"
)

func defFile() *ToolDef {
	return &ToolDef{
		Name:        "file",
		Description: "Get event history for a specific file. Shows which sessions read/modified the file.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"file_path"},
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file",
				},
			},
		},
		Tags: []string{"read"},
		Fn:   toolFile,
	}
}

func toolFile(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return nil, fmt.Errorf("file_path is required")
	}
	return d.FileHistory(ctx, filePath)
}
