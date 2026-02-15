package dash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func defGlob() *ToolDef {
	return &ToolDef{
		Name:        "glob",
		Description: "Find files matching a glob pattern. Supports ** for recursive matching. Returns files sorted by modification time.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"pattern"},
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern to match (e.g. \"**/*.go\", \"*.ts\", \"src/**/*.rs\")"},
				"path":    map[string]any{"type": "string", "description": "Base directory to search in (default: file root)"},
			},
		},
		Tags: []string{"read", "fs"},
		Fn:   toolGlob,
	}
}

func toolGlob(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	basePath := strings.TrimSuffix(d.fileConfig.AllowedRoot, "/")
	if p, ok := args["path"].(string); ok && p != "" {
		validated, err := d.fileConfig.ValidatePath(p)
		if err != nil {
			return nil, fmt.Errorf("invalid path: %w", err)
		}
		basePath = validated
	}

	type fileEntry struct {
		Path    string `json:"path"`
		Size    int64  `json:"size"`
		ModTime string `json:"mod_time"`
		IsDir   bool   `json:"is_dir"`
	}

	var files []fileEntry
	maxResults := 1000
	truncated := false

	// Handle ** patterns via WalkDir
	hasDoublestar := strings.Contains(pattern, "**")

	if hasDoublestar {
		// Extract the part after ** for matching
		err := filepath.WalkDir(basePath, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}

			if entry.IsDir() {
				name := entry.Name()
				if name == ".git" || name == "node_modules" || name == ".svn" {
					return filepath.SkipDir
				}
				return nil
			}

			// Get relative path from basePath
			relPath, err := filepath.Rel(basePath, path)
			if err != nil {
				return nil
			}

			if globMatch(pattern, relPath) {
				if len(files) >= maxResults {
					truncated = true
					return filepath.SkipAll
				}
				info, err := entry.Info()
				if err != nil {
					return nil
				}
				files = append(files, fileEntry{
					Path:    path,
					Size:    info.Size(),
					ModTime: info.ModTime().Format("2006-01-02T15:04:05Z"),
					IsDir:   false,
				})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		// Simple glob without **
		fullPattern := filepath.Join(basePath, pattern)
		matches, err := filepath.Glob(fullPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob: %w", err)
		}
		for _, m := range matches {
			if len(files) >= maxResults {
				truncated = true
				break
			}
			info, err := os.Stat(m)
			if err != nil {
				continue
			}
			files = append(files, fileEntry{
				Path:    m,
				Size:    info.Size(),
				ModTime: info.ModTime().Format("2006-01-02T15:04:05Z"),
				IsDir:   info.IsDir(),
			})
		}
	}

	// Sort by mod_time descending
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime > files[j].ModTime
	})

	return map[string]any{
		"files":     files,
		"count":     len(files),
		"truncated": truncated,
	}, nil
}

// globMatch matches a path against a pattern with ** support.
func globMatch(pattern, path string) bool {
	// Split pattern and path into segments
	patParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")
	return globMatchParts(patParts, pathParts)
}

func globMatchParts(pattern, path []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			// ** matches zero or more path segments
			pattern = pattern[1:]
			if len(pattern) == 0 {
				return true
			}
			for i := 0; i <= len(path); i++ {
				if globMatchParts(pattern, path[i:]) {
					return true
				}
			}
			return false
		}

		if len(path) == 0 {
			return false
		}

		matched, _ := filepath.Match(pattern[0], path[0])
		if !matched {
			return false
		}
		pattern = pattern[1:]
		path = path[1:]
	}
	return len(path) == 0
}
