package dash

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

func defRead() *ToolDef {
	return &ToolDef{
		Name:        "read",
		Description: "Read the contents of a file. Returns lines with line numbers. Detects binary files.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"path"},
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Absolute path to the file to read"},
				"offset": map[string]any{"type": "integer", "description": "Line number to start from (1-based, default: 1)"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum lines to return (default: 2000)"},
			},
		},
		Tags: []string{"read", "fs"},
		Fn:   toolRead,
	}
}

func toolRead(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	validated, err := d.fileConfig.ValidatePath(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	offset := 1
	if o, ok := args["offset"].(float64); ok && o > 0 {
		offset = int(o)
	}

	limit := 2000
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 10000 {
			limit = 10000
		}
	}

	f, err := os.Open(validated)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	// Binary detection: check first 8KB for null bytes
	header := make([]byte, 8192)
	n, _ := f.Read(header)
	for i := 0; i < n; i++ {
		if header[i] == 0 {
			return map[string]any{
				"path":   validated,
				"binary": true,
				"error":  "file appears to be binary",
			}, nil
		}
	}
	// Seek back to start
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer

	var lines []string
	lineNum := 0
	totalLines := 0
	truncated := false

	for scanner.Scan() {
		totalLines++
		lineNum++

		if lineNum < offset {
			continue
		}

		if len(lines) >= limit {
			truncated = true
			// Count remaining lines
			for scanner.Scan() {
				totalLines++
			}
			break
		}

		line := scanner.Text()
		if len(line) > 2000 {
			line = line[:2000] + "..."
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	content := strings.Join(lines, "\n")

	return map[string]any{
		"path":        validated,
		"content":     content,
		"lines":       len(lines),
		"total_lines": totalLines,
		"offset":      offset,
		"truncated":   truncated,
	}, nil
}
