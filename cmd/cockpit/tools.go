package main

import (
	"dash"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// toolIcon returns an icon per tool name.
var toolIcon = map[string]string{
	"node": "\u25c6", "link": "\u27f6", "traverse": "\u2933", "query": "\u229e",
	"working_set": "\u25ce", "tasks": "\u2630", "summary": "\u25a4",
	"search": "\u2315", "remember": "\u2726", "suggest": "\u2727", "promote": "\u21d1",
	"patterns": "\u2b21", "activity": "\u25d4", "session": "\u25d1", "file": "\u25c7",
	"gc": "\u267b", "embed": "\u25c8",
	"read": "\u25b8", "write": "\u25b9", "edit": "\u270e", "grep": "\u2315",
	"glob": "\u229b", "ls": "\u25a6", "mkdir": "\u25a3", "exec": "\u26a1",
}

func renderToolBox(tc toolCall, result string, boxWidth int) string {
	innerWidth := boxWidth - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	var lines []string
	icon := toolIcon[tc.Function.Name]
	if icon == "" {
		icon = "\u2022"
	}
	lines = append(lines, toolBoxHeader.Render(icon+" "+tc.Function.Name))

	argSummary := formatToolArgs(tc.Function.Name, tc.Function.Arguments)
	if argSummary != "" {
		lines = append(lines, toolBoxArg.Render(argSummary))
	}

	if result != "" {
		lines = append(lines, "")
		lines = append(lines, formatToolResult(tc.Function.Name, result, innerWidth)...)
	}

	content := strings.Join(lines, "\n")
	return toolBox.Width(innerWidth).Render(content)
}

func renderToolBoxPending(tc toolCall, boxWidth int) string {
	innerWidth := boxWidth - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	var lines []string
	icon := toolIcon[tc.Function.Name]
	if icon == "" {
		icon = "\u2022"
	}
	lines = append(lines, toolBoxHeader.Render(icon+" "+tc.Function.Name))

	argSummary := formatToolArgs(tc.Function.Name, tc.Function.Arguments)
	if argSummary != "" {
		lines = append(lines, toolBoxArg.Render(argSummary))
	}

	lines = append(lines, "", chatStreaming.Render("..."))
	return toolBox.Width(innerWidth).Render(strings.Join(lines, "\n"))
}

func formatToolArgs(toolName, argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}

	switch toolName {
	case "search":
		if q, _ := args["query"].(string); q != "" {
			return fmt.Sprintf("query: %q", q)
		}
	case "query":
		if q, _ := args["query"].(string); q != "" {
			return fmt.Sprintf("sql: %s", truncate(q, 80))
		}
	case "node":
		op, _ := args["op"].(string)
		parts := []string{op}
		for _, k := range []string{"layer", "type", "name"} {
			if v, _ := args[k].(string); v != "" {
				parts = append(parts, v)
			}
		}
		return strings.Join(parts, " ")
	case "link":
		op, _ := args["op"].(string)
		rel, _ := args["relation"].(string)
		if rel != "" {
			return op + " " + rel
		}
		return op
	case "traverse":
		dir, _ := args["direction"].(string)
		if dir == "" {
			dir = "dependencies"
		}
		return dir
	case "remember":
		t, _ := args["type"].(string)
		text, _ := args["text"].(string)
		return fmt.Sprintf("%s: %s", t, truncate(text, 40))
	case "read", "write", "edit", "ls", "mkdir":
		if p, _ := args["path"].(string); p != "" {
			return shortenPath(p)
		}
	case "grep":
		pattern, _ := args["pattern"].(string)
		path, _ := args["path"].(string)
		s := fmt.Sprintf("/%s/", pattern)
		if path != "" {
			s += " in " + shortenPath(path)
		}
		return s
	case "glob":
		pattern, _ := args["pattern"].(string)
		path, _ := args["path"].(string)
		s := pattern
		if path != "" {
			s += " in " + shortenPath(path)
		}
		return s
	case "exec":
		cmd, _ := args["command"].(string)
		return truncate(cmd, 60)
	case "embed":
		op, _ := args["op"].(string)
		return op
	case "patterns":
		t, _ := args["type"].(string)
		if t != "" {
			return t
		}
		return "all"
	}
	return ""
}

func formatToolResult(toolName, result string, maxWidth int) []string {
	switch toolName {
	case "search":
		return formatSearchResult(result, maxWidth)
	case "query":
		return formatQueryResult(result, maxWidth)
	case "read":
		return formatReadResult(result)
	case "write":
		return formatWriteResult(result)
	case "edit":
		return formatEditResult(result)
	case "exec":
		return formatExecResult(result, maxWidth)
	case "grep":
		return formatGrepResult(result, maxWidth)
	case "glob":
		return formatGlobResult(result, maxWidth)
	case "tasks":
		return formatTasksResult(result, maxWidth)
	case "node":
		return formatNodeResult(result, maxWidth)
	default:
		return formatGenericResult(result, maxWidth)
	}
}

func formatSearchResult(result string, maxWidth int) []string {
	var data []any
	if err := json.Unmarshal([]byte(result), &data); err != nil {
		var obj map[string]any
		if err2 := json.Unmarshal([]byte(result), &obj); err2 == nil {
			if r, ok := obj["results"].([]any); ok {
				data = r
			}
		}
	}
	if len(data) == 0 {
		return []string{toolBoxDim.Render("no results")}
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("%d results", len(data)))
	for i, item := range data {
		if i >= 5 {
			lines = append(lines, toolBoxDim.Render(fmt.Sprintf("  +%d more...", len(data)-5)))
			break
		}
		if m, ok := item.(map[string]any); ok {
			name := firstString(m, "file_path", "name", "path")
			if name != "" {
				lines = append(lines, "  "+shortenPath(name))
			}
		}
	}
	return lines
}

func formatQueryResult(result string, maxWidth int) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(result), &obj); err != nil {
		return formatGenericResult(result, maxWidth)
	}
	count, _ := obj["count"].(float64)
	cols, _ := obj["columns"].([]any)
	rows, _ := obj["rows"].([]any)

	colNames := make([]string, 0, len(cols))
	for _, c := range cols {
		if s, ok := c.(string); ok {
			colNames = append(colNames, s)
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("%d rows  [%s]", int(count), strings.Join(colNames, ", ")))
	for i, row := range rows {
		if i >= 3 {
			lines = append(lines, toolBoxDim.Render(fmt.Sprintf("  +%d rows...", len(rows)-3)))
			break
		}
		if m, ok := row.(map[string]any); ok {
			var parts []string
			for _, col := range colNames {
				parts = append(parts, fmt.Sprintf("%v", m[col]))
			}
			lines = append(lines, "  "+truncate(strings.Join(parts, " | "), maxWidth-2))
		}
	}
	return lines
}

func formatReadResult(result string) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(result), &obj); err != nil {
		return []string{textSuccess.Render("\u2713 read")}
	}
	total, _ := obj["total_lines"].(float64)
	return []string{textSuccess.Render("\u2713 ") + fmt.Sprintf("%d lines", int(total))}
}

func formatWriteResult(result string) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(result), &obj); err != nil {
		return []string{textSuccess.Render("\u2713 written")}
	}
	bytes, _ := obj["bytes_written"].(float64)
	return []string{textSuccess.Render("\u2713 ") + fmt.Sprintf("%d bytes", int(bytes))}
}

func formatEditResult(result string) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(result), &obj); err != nil {
		return []string{textSuccess.Render("\u2713 edited")}
	}
	replacements, _ := obj["replacements"].(float64)
	return []string{textSuccess.Render("\u2713 ") + fmt.Sprintf("%d replacements", int(replacements))}
}

func formatExecResult(result string, maxWidth int) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(result), &obj); err != nil {
		return formatGenericResult(result, maxWidth)
	}
	exitCode, _ := obj["exit_code"].(float64)
	durationMs, _ := obj["duration_ms"].(float64)
	stdout, _ := obj["stdout"].(string)
	stderr, _ := obj["stderr"].(string)

	var lines []string
	if int(exitCode) == 0 {
		lines = append(lines, textSuccess.Render("\u2713 ")+toolBoxDim.Render(fmt.Sprintf("%dms", int(durationMs))))
	} else {
		lines = append(lines, textAlert.Render(fmt.Sprintf("\u2717 exit %d", int(exitCode))))
	}
	output := stdout
	if output == "" {
		output = stderr
	}
	if output != "" {
		for i, line := range strings.Split(strings.TrimSpace(output), "\n") {
			if i >= 3 {
				lines = append(lines, toolBoxDim.Render(fmt.Sprintf("  +more...")))
				break
			}
			lines = append(lines, "  "+truncate(line, maxWidth-2))
		}
	}
	return lines
}

func formatGrepResult(result string, maxWidth int) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(result), &obj); err != nil {
		return formatGenericResult(result, maxWidth)
	}
	total, _ := obj["total_matches"].(float64)
	matches, _ := obj["matches"].([]any)

	var lines []string
	lines = append(lines, fmt.Sprintf("%d matches", int(total)))
	fileMatches := map[string]int{}
	var fileOrder []string
	for _, m := range matches {
		if mm, ok := m.(map[string]any); ok {
			f := firstString(mm, "File", "file")
			if _, seen := fileMatches[f]; !seen {
				fileOrder = append(fileOrder, f)
			}
			fileMatches[f]++
		}
	}
	for i, f := range fileOrder {
		if i >= 5 {
			lines = append(lines, toolBoxDim.Render(fmt.Sprintf("  +%d files...", len(fileOrder)-5)))
			break
		}
		lines = append(lines, fmt.Sprintf("  %s %s", shortenPath(f), toolBoxDim.Render(fmt.Sprintf("(%d)", fileMatches[f]))))
	}
	return lines
}

func formatGlobResult(result string, maxWidth int) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(result), &obj); err != nil {
		return formatGenericResult(result, maxWidth)
	}
	count, _ := obj["count"].(float64)
	files, _ := obj["files"].([]any)

	var lines []string
	lines = append(lines, fmt.Sprintf("%d files", int(count)))
	for i, f := range files {
		if i >= 5 {
			lines = append(lines, toolBoxDim.Render(fmt.Sprintf("  +%d more...", len(files)-5)))
			break
		}
		if fm, ok := f.(map[string]any); ok {
			path := firstString(fm, "Path", "path")
			lines = append(lines, "  "+shortenPath(path))
		}
	}
	return lines
}

func formatTasksResult(result string, maxWidth int) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(result), &obj); err != nil {
		return formatGenericResult(result, maxWidth)
	}
	count, _ := obj["count"].(float64)
	tasks, _ := obj["tasks"].([]any)

	var lines []string
	lines = append(lines, fmt.Sprintf("%d tasks", int(count)))
	for i, t := range tasks {
		if i >= 5 {
			lines = append(lines, toolBoxDim.Render(fmt.Sprintf("  +%d more...", len(tasks)-5)))
			break
		}
		if tm, ok := t.(map[string]any); ok {
			name := firstString(tm, "Name", "name")
			status := firstString(tm, "Status", "status")
			lines = append(lines, fmt.Sprintf("  [%s] %s", status, truncate(name, maxWidth-14)))
		}
	}
	return lines
}

func formatNodeResult(result string, maxWidth int) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(result), &obj); err != nil {
		var arr []any
		if err2 := json.Unmarshal([]byte(result), &arr); err2 != nil {
			return formatGenericResult(result, maxWidth)
		}
		return []string{fmt.Sprintf("%d nodes", len(arr))}
	}
	name, _ := obj["name"].(string)
	layer, _ := obj["layer"].(string)
	typ, _ := obj["type"].(string)
	return []string{fmt.Sprintf("%s.%s: %s", layer, typ, name)}
}

func formatGenericResult(result string, maxWidth int) []string {
	if len(result) > 200 {
		result = result[:197] + "..."
	}
	var lines []string
	for _, line := range strings.Split(result, "\n") {
		lines = append(lines, truncate(line, maxWidth))
		if len(lines) >= 4 {
			lines = append(lines, toolBoxDim.Render("..."))
			break
		}
	}
	return lines
}

// --- Helpers ---

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max || max < 4 {
		return s
	}
	return s[:max-3] + "..."
}

func shortenPath(path string) string {
	path = strings.TrimPrefix(path, "/dash/")
	return path
}

func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		if len(paragraph) <= width {
			lines = append(lines, paragraph)
			continue
		}
		words := strings.Fields(paragraph)
		var line string
		for _, w := range words {
			if line == "" {
				line = w
			} else if len(line)+1+len(w) <= width {
				line += " " + w
			} else {
				lines = append(lines, line)
				line = w
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func formatBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fK", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1fM", float64(b)/(1024*1024))
	}
}

// nodeData unmarshals a Node's json.RawMessage Data field into a map.
func nodeData(n *dash.Node) map[string]any {
	if n == nil || len(n.Data) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(n.Data, &m); err != nil {
		return nil
	}
	return m
}

