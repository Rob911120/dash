package dash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func defLs() *ToolDef {
	return &ToolDef{
		Name:        "ls",
		Description: "List directory contents. Shows files and directories with size, mode, and modification time.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":           map[string]any{"type": "string", "description": "Directory to list (default: file root)"},
				"recursive":      map[string]any{"type": "boolean", "description": "List recursively (default: false)"},
				"include_hidden": map[string]any{"type": "boolean", "description": "Include hidden files starting with . (default: false)"},
			},
		},
		Tags: []string{"read", "fs"},
		Fn:   toolLs,
	}
}

func toolLs(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	dirPath := strings.TrimSuffix(d.fileConfig.AllowedRoot, "/")
	if p, ok := args["path"].(string); ok && p != "" {
		validated, err := d.fileConfig.ValidatePath(p)
		if err != nil {
			return nil, fmt.Errorf("invalid path: %w", err)
		}
		dirPath = validated
	}

	recursive, _ := args["recursive"].(bool)
	includeHidden, _ := args["include_hidden"].(bool)

	type entry struct {
		Name    string `json:"name"`
		IsDir   bool   `json:"is_dir"`
		Size    int64  `json:"size"`
		Mode    string `json:"mode"`
		ModTime string `json:"mod_time"`
	}

	var entries []entry
	maxEntries := 5000

	if recursive {
		err := filepath.WalkDir(dirPath, func(path string, de os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if path == dirPath {
				return nil
			}

			name := de.Name()
			if !includeHidden && strings.HasPrefix(name, ".") {
				if de.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			if de.IsDir() && (name == "node_modules" || name == ".git") {
				return filepath.SkipDir
			}

			if len(entries) >= maxEntries {
				return filepath.SkipAll
			}

			relPath, _ := filepath.Rel(dirPath, path)
			info, err := de.Info()
			if err != nil {
				return nil
			}

			entries = append(entries, entry{
				Name:    relPath,
				IsDir:   de.IsDir(),
				Size:    info.Size(),
				Mode:    info.Mode().String(),
				ModTime: info.ModTime().Format("2006-01-02T15:04:05Z"),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		dirEntries, err := os.ReadDir(dirPath)
		if err != nil {
			return nil, fmt.Errorf("readdir: %w", err)
		}

		for _, de := range dirEntries {
			if !includeHidden && strings.HasPrefix(de.Name(), ".") {
				continue
			}
			if len(entries) >= maxEntries {
				break
			}

			info, err := de.Info()
			if err != nil {
				continue
			}

			entries = append(entries, entry{
				Name:    de.Name(),
				IsDir:   de.IsDir(),
				Size:    info.Size(),
				Mode:    info.Mode().String(),
				ModTime: info.ModTime().Format("2006-01-02T15:04:05Z"),
			})
		}
	}

	return map[string]any{
		"path":    dirPath,
		"entries": entries,
		"count":   len(entries),
	}, nil
}
