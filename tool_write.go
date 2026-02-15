package dash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func defWrite() *ToolDef {
	return &ToolDef{
		Name:        "write",
		Description: "Write content to a file. Creates or overwrites the file.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"path", "content"},
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "Absolute path to the file to write"},
				"content":     map[string]any{"type": "string", "description": "Content to write to the file"},
				"create_dirs": map[string]any{"type": "boolean", "description": "Create parent directories if they don't exist (default: false)"},
			},
		},
		Tags: []string{"write", "fs"},
		Fn:   toolWrite,
	}
}

func toolWrite(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	content, _ := args["content"].(string)

	validated, err := d.fileConfig.ValidatePath(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	if createDirs, ok := args["create_dirs"].(bool); ok && createDirs {
		if err := os.MkdirAll(filepath.Dir(validated), 0755); err != nil {
			return nil, fmt.Errorf("mkdir: %w", err)
		}
	}

	if err := os.WriteFile(validated, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	return map[string]any{
		"path":          validated,
		"bytes_written": len(content),
	}, nil
}
