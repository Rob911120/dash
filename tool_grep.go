package dash

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func defGrep() *ToolDef {
	return &ToolDef{
		Name:        "grep",
		Description: "Search file contents using regular expressions. Walks directories recursively, skipping .git and binary files.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"pattern"},
			"properties": map[string]any{
				"pattern":          map[string]any{"type": "string", "description": "Regular expression pattern to search for"},
				"path":             map[string]any{"type": "string", "description": "Directory or file to search in (default: file root)"},
				"glob_filter":      map[string]any{"type": "string", "description": "Glob pattern to filter files (e.g. \"*.go\", \"*.ts\")"},
				"context_lines":    map[string]any{"type": "integer", "description": "Lines of context before and after each match (default: 0)"},
				"max_results":      map[string]any{"type": "integer", "description": "Maximum number of matches to return (default: 100)"},
				"case_insensitive": map[string]any{"type": "boolean", "description": "Case-insensitive search (default: false)"},
			},
		},
		Tags: []string{"read", "fs"},
		Fn:   toolGrep,
	}
}

func toolGrep(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	if ci, ok := args["case_insensitive"].(bool); ok && ci {
		pattern = "(?i)" + pattern
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	searchPath := strings.TrimSuffix(d.fileConfig.AllowedRoot, "/")
	if p, ok := args["path"].(string); ok && p != "" {
		validated, err := d.fileConfig.ValidatePath(p)
		if err != nil {
			return nil, fmt.Errorf("invalid path: %w", err)
		}
		searchPath = validated
	}

	globFilter, _ := args["glob_filter"].(string)
	contextLines := 0
	if c, ok := args["context_lines"].(float64); ok {
		contextLines = int(c)
		if contextLines > 10 {
			contextLines = 10
		}
	}

	maxResults := 100
	if m, ok := args["max_results"].(float64); ok {
		maxResults = int(m)
		if maxResults > 1000 {
			maxResults = 1000
		}
	}

	type match struct {
		File          string   `json:"file"`
		Line          int      `json:"line"`
		Content       string   `json:"content"`
		ContextBefore []string `json:"context_before,omitempty"`
		ContextAfter  []string `json:"context_after,omitempty"`
	}

	var matches []match
	filesSearched := 0
	totalMatches := 0

	err = filepath.WalkDir(searchPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}

		// Skip .git directories
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == ".svn" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary files
		if !isEmbeddableFile(path) {
			return nil
		}

		// Apply glob filter
		if globFilter != "" {
			matched, _ := filepath.Match(globFilter, filepath.Base(path))
			if !matched {
				return nil
			}
		}

		if filesSearched >= 10000 {
			return filepath.SkipAll
		}
		filesSearched++

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 256*1024), 256*1024)

		var allLines []string
		for scanner.Scan() {
			allLines = append(allLines, scanner.Text())
		}

		for i, line := range allLines {
			if re.MatchString(line) {
				totalMatches++
				if len(matches) < maxResults {
					m := match{
						File:    path,
						Line:    i + 1,
						Content: line,
					}
					if contextLines > 0 {
						start := i - contextLines
						if start < 0 {
							start = 0
						}
						m.ContextBefore = allLines[start:i]

						end := i + 1 + contextLines
						if end > len(allLines) {
							end = len(allLines)
						}
						m.ContextAfter = allLines[i+1 : end]
					}
					matches = append(matches, m)
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return map[string]any{
		"matches":        matches,
		"files_searched": filesSearched,
		"total_matches":  totalMatches,
		"truncated":      totalMatches > len(matches),
	}, nil
}
