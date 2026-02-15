// Package main implements dashquery - a CLI for querying the Dash graph database.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"dash"

	_ "github.com/lib/pq"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	db, err := connectDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashquery: db connection failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := os.Args[1]
	args := os.Args[2:]

	var result any
	switch cmd {
	case "sessions":
		result, err = querySessions(ctx, db, args)
	case "files":
		result, err = queryFiles(ctx, db, args)
	case "tools":
		result, err = queryTools(ctx, db, args)
	case "failures":
		result, err = queryFailures(ctx, db, args)
	case "search":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "dashquery search: missing search term")
			os.Exit(1)
		}
		result, err = searchNodes(ctx, db, args[0])
	case "sql":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "dashquery sql: missing query")
			os.Exit(1)
		}
		result, err = executeSQL(ctx, db, strings.Join(args, " "))
	case "node":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "dashquery node: missing node ID or name")
			os.Exit(1)
		}
		result, err = getNode(ctx, db, args[0])
	case "history":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "dashquery history: missing file path")
			os.Exit(1)
		}
		result, err = fileHistory(ctx, db, args[0])
	case "check":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "dashquery check: usage: check <tool> <pattern>")
			fmt.Fprintln(os.Stderr, "  Examples:")
			fmt.Fprintln(os.Stderr, "    dashquery check Read /dash/.claude/hooks/dashhook")
			fmt.Fprintln(os.Stderr, "    dashquery check Bash 'go build'")
			os.Exit(1)
		}
		result, err = checkFailures(ctx, db, args[0], args[1])
	case "warn":
		// Like check but exits with code 1 if failures found (for pre-hooks)
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "dashquery warn: usage: warn <tool> <pattern>")
			os.Exit(1)
		}
		result, err = checkFailures(ctx, db, args[0], args[1])
		if err == nil {
			if res, ok := result.(map[string]any); ok {
				if count, ok := res["count"].(int); ok && count > 0 {
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					enc.Encode(result)
					os.Exit(1) // Signal: past failures found
				}
			}
		}
	case "help", "-h", "--help":
		printUsage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "dashquery: unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "dashquery: %v\n", err)
		os.Exit(1)
	}

	// Output as JSON
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(result)
}

func printUsage() {
	fmt.Println(`dashquery - Query the Dash graph database

Usage: dashquery <command> [args]

Commands:
  sessions [limit]       List recent Claude Code sessions
  files [hours]          List recently touched files (default: 24h)
  tools [hours]          Tool usage statistics (default: 24h)
  failures [limit]       Recent tool failures
  search <term>          Search nodes by name
  node <id|name>         Get node details by ID or name
  history <filepath>     Get history for a file
  check <tool> <pattern> Check if similar operation failed before
  warn <tool> <pattern>  Like check, but exits 1 if failures found
  sql <query>            Execute raw SQL (SELECT only)
  help                   Show this help

Examples:
  dashquery sessions 5
  dashquery files 2
  dashquery tools
  dashquery failures 10
  dashquery search "CLAUDE.md"
  dashquery node "d18a7ca7-80e6-410a-bad3-31bd6942bc36"
  dashquery history "/dash/CLAUDE.md"
  dashquery sql "SELECT COUNT(*) FROM nodes"`)
}

func connectDB() (*sql.DB, error) {
	return dash.ConnectDB()
}

func querySessions(ctx context.Context, db *sql.DB, args []string) (any, error) {
	limit := 10
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d", &limit)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			id,
			name,
			data->>'status' as status,
			data->>'cwd' as cwd,
			created_at,
			updated_at
		FROM nodes
		WHERE layer = 'CONTEXT' AND type = 'session' AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []map[string]any
	for rows.Next() {
		var id, name string
		var status, cwd sql.NullString
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&id, &name, &status, &cwd, &createdAt, &updatedAt); err != nil {
			return nil, err
		}

		sessions = append(sessions, map[string]any{
			"id":         id,
			"name":       name,
			"status":     status.String,
			"cwd":        cwd.String,
			"created_at": createdAt.Format(time.RFC3339),
			"updated_at": updatedAt.Format(time.RFC3339),
			"age":        time.Since(createdAt).Round(time.Second).String(),
		})
	}

	return map[string]any{
		"count":    len(sessions),
		"sessions": sessions,
	}, nil
}

func queryFiles(ctx context.Context, db *sql.DB, args []string) (any, error) {
	hours := 24
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d", &hours)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			n.name as file_path,
			ee.relation,
			ee.occurred_at,
			s.name as session_name
		FROM edge_events ee
		JOIN nodes n ON ee.target_id = n.id
		JOIN nodes s ON ee.source_id = s.id
		WHERE n.type = 'file'
		  AND s.type = 'session'
		  AND ee.occurred_at > NOW() - $1::interval
		ORDER BY ee.occurred_at DESC
		LIMIT 50
	`, fmt.Sprintf("%d hours", hours))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []map[string]any
	for rows.Next() {
		var filePath, relation, sessionName string
		var occurredAt time.Time

		if err := rows.Scan(&filePath, &relation, &occurredAt, &sessionName); err != nil {
			return nil, err
		}

		files = append(files, map[string]any{
			"file":       filePath,
			"relation":   relation,
			"session":    sessionName,
			"when":       occurredAt.Format(time.RFC3339),
			"age":        time.Since(occurredAt).Round(time.Second).String(),
		})
	}

	return map[string]any{
		"hours":  hours,
		"count":  len(files),
		"files":  files,
	}, nil
}

func queryTools(ctx context.Context, db *sql.DB, args []string) (any, error) {
	hours := 24
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d", &hours)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			data->'claude_code'->>'tool_name' as tool,
			COUNT(*) FILTER (WHERE data->'normalized'->>'event' = 'tool.post') as calls,
			COUNT(*) FILTER (WHERE data->'normalized'->>'event' = 'tool.failure') as failures
		FROM observations
		WHERE type = 'tool_event'
		  AND observed_at > NOW() - $1::interval
		  AND data->'claude_code'->>'tool_name' IS NOT NULL
		GROUP BY data->'claude_code'->>'tool_name'
		ORDER BY calls DESC
	`, fmt.Sprintf("%d hours", hours))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tools []map[string]any
	totalCalls := 0
	totalFailures := 0

	for rows.Next() {
		var tool string
		var calls, failures int

		if err := rows.Scan(&tool, &calls, &failures); err != nil {
			return nil, err
		}

		tools = append(tools, map[string]any{
			"tool":     tool,
			"calls":    calls,
			"failures": failures,
		})
		totalCalls += calls
		totalFailures += failures
	}

	return map[string]any{
		"hours":          hours,
		"total_calls":    totalCalls,
		"total_failures": totalFailures,
		"tools":          tools,
	}, nil
}

func queryFailures(ctx context.Context, db *sql.DB, args []string) (any, error) {
	limit := 10
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d", &limit)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			data->'claude_code'->>'tool_name' as tool,
			data->'claude_code'->'tool_input' as input,
			data->'claude_code'->>'session_id' as session,
			observed_at
		FROM observations
		WHERE type = 'tool_event'
		  AND data->'normalized'->>'event' = 'tool.failure'
		ORDER BY observed_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var failures []map[string]any
	for rows.Next() {
		var tool, session sql.NullString
		var input json.RawMessage
		var observedAt time.Time

		if err := rows.Scan(&tool, &input, &session, &observedAt); err != nil {
			return nil, err
		}

		var inputParsed any
		json.Unmarshal(input, &inputParsed)

		failures = append(failures, map[string]any{
			"tool":    tool.String,
			"input":   inputParsed,
			"session": session.String,
			"when":    observedAt.Format(time.RFC3339),
			"age":     time.Since(observedAt).Round(time.Second).String(),
		})
	}

	return map[string]any{
		"count":    len(failures),
		"failures": failures,
	}, nil
}

func searchNodes(ctx context.Context, db *sql.DB, term string) (any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, layer, type, name, created_at
		FROM nodes
		WHERE deleted_at IS NULL
		  AND (name ILIKE $1 OR type ILIKE $1)
		ORDER BY updated_at DESC
		LIMIT 20
	`, "%"+term+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []map[string]any
	for rows.Next() {
		var id, layer, typ, name string
		var createdAt time.Time

		if err := rows.Scan(&id, &layer, &typ, &name, &createdAt); err != nil {
			return nil, err
		}

		nodes = append(nodes, map[string]any{
			"id":         id,
			"layer":      layer,
			"type":       typ,
			"name":       name,
			"created_at": createdAt.Format(time.RFC3339),
		})
	}

	return map[string]any{
		"term":  term,
		"count": len(nodes),
		"nodes": nodes,
	}, nil
}

func getNode(ctx context.Context, db *sql.DB, idOrName string) (any, error) {
	// Try by ID first, then by name
	var id, layer, typ, name string
	var data json.RawMessage
	var createdAt, updatedAt time.Time

	err := db.QueryRowContext(ctx, `
		SELECT id, layer, type, name, data, created_at, updated_at
		FROM nodes
		WHERE deleted_at IS NULL AND (id::text = $1 OR name = $1)
		LIMIT 1
	`, idOrName).Scan(&id, &layer, &typ, &name, &data, &createdAt, &updatedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("node not found: %s", idOrName)
	}
	if err != nil {
		return nil, err
	}

	var dataParsed any
	json.Unmarshal(data, &dataParsed)

	return map[string]any{
		"id":         id,
		"layer":      layer,
		"type":       typ,
		"name":       name,
		"data":       dataParsed,
		"created_at": createdAt.Format(time.RFC3339),
		"updated_at": updatedAt.Format(time.RFC3339),
	}, nil
}

func fileHistory(ctx context.Context, db *sql.DB, filepath string) (any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			ee.relation,
			ee.occurred_at,
			s.name as session_name,
			ee.data
		FROM edge_events ee
		JOIN nodes n ON ee.target_id = n.id
		JOIN nodes s ON ee.source_id = s.id
		WHERE n.name = $1 AND n.type = 'file'
		ORDER BY ee.occurred_at DESC
		LIMIT 30
	`, filepath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []map[string]any
	for rows.Next() {
		var relation, sessionName string
		var data json.RawMessage
		var occurredAt time.Time

		if err := rows.Scan(&relation, &occurredAt, &sessionName, &data); err != nil {
			return nil, err
		}

		var dataParsed any
		json.Unmarshal(data, &dataParsed)

		events = append(events, map[string]any{
			"relation": relation,
			"session":  sessionName,
			"when":     occurredAt.Format(time.RFC3339),
			"age":      time.Since(occurredAt).Round(time.Second).String(),
			"data":     dataParsed,
		})
	}

	return map[string]any{
		"file":   filepath,
		"count":  len(events),
		"events": events,
	}, nil
}

func executeSQL(ctx context.Context, db *sql.DB, query string) (any, error) {
	// Safety: only allow SELECT/WITH
	normalized := strings.TrimSpace(strings.ToUpper(query))
	if !strings.HasPrefix(normalized, "SELECT") && !strings.HasPrefix(normalized, "WITH") {
		return nil, fmt.Errorf("only SELECT queries allowed")
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		row := make(map[string]any)
		for i, col := range columns {
			val := values[i]
			// Convert []byte to string for readability
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		results = append(results, row)
	}

	return map[string]any{
		"columns": columns,
		"count":   len(results),
		"rows":    results,
	}, nil
}

func checkFailures(ctx context.Context, db *sql.DB, tool, pattern string) (any, error) {
	// Search for past failures matching tool and pattern
	rows, err := db.QueryContext(ctx, `
		SELECT
			data->'claude_code'->>'tool_name' as tool,
			data->'claude_code'->'tool_input' as input,
			data->'claude_code'->>'session_id' as session,
			observed_at
		FROM observations
		WHERE type = 'tool_event'
		  AND data->'normalized'->>'event' = 'tool.failure'
		  AND (
		    data->'claude_code'->>'tool_name' = $1
		    OR $1 = '*'
		  )
		  AND (
		    (data->'claude_code'->'tool_input')::text ILIKE $2
		    OR data->'claude_code'->'tool_input'->>'file_path' ILIKE $2
		    OR data->'claude_code'->'tool_input'->>'command' ILIKE $2
		    OR data->'claude_code'->'tool_input'->>'path' ILIKE $2
		  )
		ORDER BY observed_at DESC
		LIMIT 10
	`, tool, "%"+pattern+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var failures []map[string]any
	for rows.Next() {
		var toolName, session sql.NullString
		var input json.RawMessage
		var observedAt time.Time

		if err := rows.Scan(&toolName, &input, &session, &observedAt); err != nil {
			return nil, err
		}

		var inputParsed any
		json.Unmarshal(input, &inputParsed)

		failures = append(failures, map[string]any{
			"tool":    toolName.String,
			"input":   inputParsed,
			"session": session.String,
			"when":    observedAt.Format(time.RFC3339),
			"age":     time.Since(observedAt).Round(time.Second).String(),
		})
	}

	result := map[string]any{
		"tool":     tool,
		"pattern":  pattern,
		"count":    len(failures),
		"failures": failures,
	}

	// Add warning message if failures found
	if len(failures) > 0 {
		result["warning"] = fmt.Sprintf("⚠️  Found %d past failure(s) matching this pattern!", len(failures))
	} else {
		result["ok"] = "✓ No past failures found for this pattern"
	}

	return result, nil
}
