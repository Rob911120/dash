package dash

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func defEdit() *ToolDef {
	return &ToolDef{
		Name:        "edit",
		Description: "Perform string replacement in a file. Replaces exact matches of old_text with new_text.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"path", "old_text", "new_text"},
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "Absolute path to the file to edit"},
				"old_text":    map[string]any{"type": "string", "description": "The text to find and replace"},
				"new_text":    map[string]any{"type": "string", "description": "The replacement text"},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences (default: false, replaces first only)"},
			},
		},
		Tags: []string{"write", "fs"},
		Fn:   toolEdit,
	}
}

func toolEdit(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	oldText, _ := args["old_text"].(string)
	newText, _ := args["new_text"].(string)
	if oldText == "" {
		return nil, fmt.Errorf("old_text is required")
	}
	if oldText == newText {
		return nil, fmt.Errorf("old_text and new_text are identical")
	}

	validated, err := d.fileConfig.ValidatePath(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	data, err := os.ReadFile(validated)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	content := string(data)
	if !strings.Contains(content, oldText) {
		return nil, fmt.Errorf("old_text not found in file")
	}

	replaceAll, _ := args["replace_all"].(bool)

	var newContent string
	var replacements int
	if replaceAll {
		replacements = strings.Count(content, oldText)
		newContent = strings.ReplaceAll(content, oldText, newText)
	} else {
		replacements = 1
		newContent = strings.Replace(content, oldText, newText, 1)
	}

	if err := os.WriteFile(validated, []byte(newContent), 0644); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	return map[string]any{
		"path":         validated,
		"replacements": replacements,
		"bytes_before": len(data),
		"bytes_after":  len(newContent),
	}, nil
}
