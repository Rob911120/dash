package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FailureMatch represents a past failure that matches the current operation.
type FailureMatch struct {
	Tool      string    `json:"tool"`
	Input     any       `json:"input"`
	SessionID string    `json:"session_id"`
	When      time.Time `json:"when"`
	Age       string    `json:"age"`
}

// FailureCheckResult contains the result of checking for past failures.
type FailureCheckResult struct {
	HasFailures bool           `json:"has_failures"`
	Count       int            `json:"count"`
	Failures    []FailureMatch `json:"failures,omitempty"`
	Warning     string         `json:"warning,omitempty"`
}

// CheckPastFailures looks for past failures matching the given tool and input pattern.
func (d *Dash) CheckPastFailures(ctx context.Context, toolName string, toolInput json.RawMessage) (*FailureCheckResult, error) {
	// Extract search patterns from tool input
	patterns := extractSearchPatterns(toolName, toolInput)
	if len(patterns) == 0 {
		return &FailureCheckResult{HasFailures: false}, nil
	}

	// Build query to find matching failures
	query := `
		SELECT
			data->'claude_code'->>'tool_name' as tool,
			data->'claude_code'->'tool_input' as input,
			data->'claude_code'->>'session_id' as session,
			observed_at
		FROM observations
		WHERE type = 'tool_event'
		  AND data->'normalized'->>'event' = 'tool.failure'
		  AND data->'claude_code'->>'tool_name' = $1
		  AND (
		    (data->'claude_code'->'tool_input')::text ILIKE $2
		    OR data->'claude_code'->'tool_input'->>'file_path' ILIKE $2
		    OR data->'claude_code'->'tool_input'->>'command' ILIKE $2
		    OR data->'claude_code'->'tool_input'->>'path' ILIKE $2
		  )
		  AND observed_at > NOW() - INTERVAL '7 days'
		ORDER BY observed_at DESC
		LIMIT 5
	`

	var allFailures []FailureMatch

	// Check each pattern
	for _, pattern := range patterns {
		rows, err := d.db.QueryContext(ctx, query, toolName, "%"+pattern+"%")
		if err != nil {
			continue // Non-fatal, just skip
		}

		for rows.Next() {
			var tool, session string
			var input json.RawMessage
			var observedAt time.Time

			if err := rows.Scan(&tool, &input, &session, &observedAt); err != nil {
				continue
			}

			var inputParsed any
			json.Unmarshal(input, &inputParsed)

			allFailures = append(allFailures, FailureMatch{
				Tool:      tool,
				Input:     inputParsed,
				SessionID: session,
				When:      observedAt,
				Age:       time.Since(observedAt).Round(time.Second).String(),
			})
		}
		rows.Close()
	}

	// Deduplicate (same session + tool + similar time = same failure)
	failures := deduplicateFailures(allFailures)

	result := &FailureCheckResult{
		HasFailures: len(failures) > 0,
		Count:       len(failures),
		Failures:    failures,
	}

	if len(failures) > 0 {
		result.Warning = formatFailureWarning(failures, toolName)
	}

	return result, nil
}

// extractSearchPatterns extracts patterns to search for from tool input.
func extractSearchPatterns(toolName string, input json.RawMessage) []string {
	if input == nil {
		return nil
	}

	var data map[string]any
	if err := json.Unmarshal(input, &data); err != nil {
		return nil
	}

	var patterns []string

	switch toolName {
	case "Read", "Write", "Edit", "View", "MultiEdit":
		if path, ok := data["file_path"].(string); ok {
			// Use filename and partial path
			patterns = append(patterns, extractFilename(path))
			if len(path) > 20 {
				patterns = append(patterns, path[len(path)-20:])
			}
		}
	case "Bash":
		if cmd, ok := data["command"].(string); ok {
			// Extract key parts of command
			patterns = append(patterns, extractCommandKeywords(cmd)...)
		}
	case "Glob", "Grep":
		if pattern, ok := data["pattern"].(string); ok {
			patterns = append(patterns, pattern)
		}
		if path, ok := data["path"].(string); ok {
			patterns = append(patterns, extractFilename(path))
		}
	}

	return patterns
}

// extractFilename gets the filename from a path.
func extractFilename(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return path
}

// extractCommandKeywords extracts searchable keywords from a bash command.
// Returns specific patterns that indicate the same operation, not generic matches.
func extractCommandKeywords(cmd string) []string {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	baseCmd := parts[0]

	// Skip very common commands that would match too broadly
	// These rarely have actionable repeat failures
	skipCommands := map[string]bool{
		"ls": true, "cat": true, "echo": true, "pwd": true,
		"cd": true, "export": true, "source": true, "which": true,
		"whoami": true, "date": true, "env": true, "set": true,
	}
	if skipCommands[baseCmd] {
		return nil
	}

	var keywords []string

	// Build commands - match on the build target/output
	if baseCmd == "go" && len(parts) > 1 {
		if parts[1] == "build" {
			// Match "go build -o <output>" or "go build <path>"
			for i, p := range parts {
				if p == "-o" && i+1 < len(parts) {
					keywords = append(keywords, "go build -o "+parts[i+1])
					break
				}
				if strings.HasSuffix(p, ".go") || strings.Contains(p, "/") {
					keywords = append(keywords, "go build "+p)
				}
			}
			if len(keywords) == 0 {
				// Fallback: use the directory context
				keywords = append(keywords, "go build")
			}
		} else if parts[1] == "test" || parts[1] == "run" {
			// Match specific test/run targets
			for _, p := range parts[2:] {
				if !strings.HasPrefix(p, "-") {
					keywords = append(keywords, "go "+parts[1]+" "+p)
					break
				}
			}
		}
		return keywords
	}

	// npm/yarn/pnpm - match on the script or package
	if baseCmd == "npm" || baseCmd == "yarn" || baseCmd == "pnpm" {
		if len(parts) > 1 {
			action := parts[1]
			if action == "install" || action == "i" || action == "add" {
				// Match specific package installs
				for _, p := range parts[2:] {
					if !strings.HasPrefix(p, "-") {
						keywords = append(keywords, baseCmd+" install "+p)
					}
				}
			} else if action == "run" && len(parts) > 2 {
				keywords = append(keywords, baseCmd+" run "+parts[2])
			} else {
				keywords = append(keywords, baseCmd+" "+action)
			}
		}
		return keywords
	}

	// Docker commands - match on image/container
	if baseCmd == "docker" && len(parts) > 1 {
		action := parts[1]
		if action == "build" || action == "run" || action == "pull" {
			for _, p := range parts[2:] {
				if !strings.HasPrefix(p, "-") {
					keywords = append(keywords, "docker "+action+" "+p)
					break
				}
			}
		}
		return keywords
	}

	// Database commands - match on database + significant query parts
	if baseCmd == "psql" || baseCmd == "mysql" || baseCmd == "sqlite3" {
		// Don't match generic database commands - too variable
		// Only match if there's a specific file or clear error pattern
		for _, p := range parts {
			if strings.HasSuffix(p, ".sql") {
				keywords = append(keywords, baseCmd+" "+extractFilename(p))
			}
		}
		return keywords
	}

	// File operations with specific paths - match on the target file
	fileOps := map[string]bool{
		"rm": true, "mv": true, "cp": true, "chmod": true,
		"chown": true, "mkdir": true, "touch": true,
	}
	if fileOps[baseCmd] {
		// Get the target path (usually last non-flag argument)
		for i := len(parts) - 1; i > 0; i-- {
			p := parts[i]
			if !strings.HasPrefix(p, "-") && (strings.HasPrefix(p, "/") || strings.HasPrefix(p, "./") || strings.Contains(p, "/")) {
				keywords = append(keywords, baseCmd+" "+p)
				break
			}
		}
		return keywords
	}

	// For other commands, only match if there's a specific path involved
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "/") && len(p) > 10 {
			// Specific absolute path - match on command + path
			keywords = append(keywords, baseCmd+" "+p)
			return keywords
		}
	}

	// No specific pattern found - don't try to match
	return nil
}

// deduplicateFailures removes duplicate failures (same operation within 1 minute).
func deduplicateFailures(failures []FailureMatch) []FailureMatch {
	if len(failures) == 0 {
		return failures
	}

	seen := make(map[string]bool)
	var unique []FailureMatch

	for _, f := range failures {
		// Key: tool + session + minute
		key := fmt.Sprintf("%s:%s:%d", f.Tool, f.SessionID, f.When.Unix()/60)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, f)
		}
	}

	return unique
}

// formatFailureWarning creates a human-readable warning message.
func formatFailureWarning(failures []FailureMatch, currentTool string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("⚠️  VARNING: %d tidigare failure(s) med liknande operation!\n", len(failures)))
	sb.WriteString("────────────────────────────────────────\n")

	for i, f := range failures {
		if i >= 3 {
			sb.WriteString(fmt.Sprintf("   ... och %d till\n", len(failures)-3))
			break
		}

		// Format the input nicely
		inputStr := formatInputBrief(f.Input)
		sb.WriteString(fmt.Sprintf("  • %s ago: %s\n", f.Age, inputStr))
		sb.WriteString(fmt.Sprintf("    Session: %s\n", truncateString(f.SessionID, 12)))
	}

	sb.WriteString("────────────────────────────────────────\n")
	sb.WriteString("Överväg att kontrollera innan du fortsätter.\n")

	return sb.String()
}

// formatInputBrief creates a brief description of tool input.
func formatInputBrief(input any) string {
	if input == nil {
		return "(unknown)"
	}

	data, ok := input.(map[string]any)
	if !ok {
		return "(unknown)"
	}

	// Try common fields
	if path, ok := data["file_path"].(string); ok {
		return fmt.Sprintf("Read %s", path)
	}
	if cmd, ok := data["command"].(string); ok {
		if len(cmd) > 50 {
			cmd = cmd[:50] + "..."
		}
		return fmt.Sprintf("Bash: %s", cmd)
	}
	if pattern, ok := data["pattern"].(string); ok {
		return fmt.Sprintf("Pattern: %s", pattern)
	}

	return "(complex input)"
}

// truncateString truncates a string to maxLen characters.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
